import {
  CHUNK_SEEK_EPSILON_SECS,
  chunkInitialSeek,
  chunkPlaySeek,
  chunkTimeUpdateAction,
  reachableChunkStart,
  type ChunkMediaState,
} from '../src/history/chunkPlayback';

const media = (overrides: Partial<ChunkMediaState>): ChunkMediaState => ({
  currentTime: 0,
  duration: 2286.9,
  paused: false,
  seeking: false,
  ...overrides,
});

describe('chunk interval playback', () => {
  describe('seek-loop regressions', () => {
    // Production trace: seek to 2280.9 read back as 2280.89999 and every
    // timeupdate re-issued the same seek (~10k seeks/s) while paused.
    it('does not re-seek when the browser reports a position just below start', () => {
      const readBack = media({ currentTime: 2280.89999, paused: true });
      expect(chunkTimeUpdateAction(readBack, 2280.9, 2286.9)).toEqual({ kind: 'none' });
      expect(chunkTimeUpdateAction({ ...readBack, paused: false }, 2280.9, 2286.9)).toEqual({ kind: 'none' });
      expect(chunkInitialSeek(readBack, 2280.9)).toBeNull();
      expect(chunkPlaySeek({ ...readBack, paused: false }, 2280.9, 2286.9)).toBeNull();
    });

    it('pauses at end without seeking, then stays idle while paused', () => {
      expect(chunkTimeUpdateAction(media({ currentTime: 10 }), 0, 10)).toEqual({ kind: 'pause' });
      expect(chunkTimeUpdateAction(media({ currentTime: 10, paused: true }), 0, 10)).toEqual({ kind: 'none' });
    });

    it('never corrects while the element is seeking', () => {
      expect(chunkTimeUpdateAction(media({ currentTime: 1, seeking: true }), 5, 10)).toEqual({ kind: 'none' });
      expect(chunkTimeUpdateAction(media({ currentTime: 20, seeking: true }), 5, 10)).toEqual({ kind: 'none' });
    });

    it('ignores a start the media cannot reach instead of seeking forever', () => {
      expect(reachableChunkStart(2300, 2286.9)).toBeNull();
      expect(chunkInitialSeek(media({ currentTime: 2286.9 }), 2300)).toBeNull();
      expect(chunkTimeUpdateAction(media({ currentTime: 100 }), 2300, null)).toEqual({ kind: 'none' });
    });

    it('converges: applying any seek action never yields another seek', () => {
      const cases: [number, number | null, number | null][] = [
        [0, 2280.9, 2286.9], [2286.9, 5, 10], [3, 3.3333333333, 7.77777777], [0, 0.1 + 0.2, null],
      ];
      for (const [currentTime, start, end] of cases) {
        const first = chunkTimeUpdateAction(media({ currentTime }), start, end);
        if (first.kind !== 'seek') continue;
        // Simulate a browser that reads the target back up to 1ms low.
        const after = media({ currentTime: first.to - 0.001 });
        expect(chunkTimeUpdateAction(after, start, end).kind).not.toBe('seek');
      }
    });
  });

  it('keeps active playback inside the interval', () => {
    expect(chunkTimeUpdateAction(media({ currentTime: 1 }), 5, 10)).toEqual({ kind: 'seek', to: 5 });
    expect(chunkTimeUpdateAction(media({ currentTime: 7 }), 5, 10)).toEqual({ kind: 'none' });
    expect(chunkTimeUpdateAction(media({ currentTime: 10 - CHUNK_SEEK_EPSILON_SECS / 2 }), 5, 10))
      .toEqual({ kind: 'pause' });
    expect(chunkTimeUpdateAction(media({ currentTime: 1 }), null, null)).toEqual({ kind: 'none' });
  });

  it('seeks to start once metadata loads, unless already there', () => {
    expect(chunkInitialSeek(media({ currentTime: 0 }), 5)).toBe(5);
    expect(chunkInitialSeek(media({ currentTime: 0, duration: Number.NaN }), 5)).toBe(5);
    expect(chunkInitialSeek(media({ currentTime: 5 }), 5)).toBeNull();
    expect(chunkInitialSeek(media({ currentTime: 0 }), null)).toBeNull();
  });

  it('rewinds to start when playing from outside the interval', () => {
    expect(chunkPlaySeek(media({ currentTime: 10 }), 5, 10)).toBe(5);
    expect(chunkPlaySeek(media({ currentTime: 2 }), 5, 10)).toBe(5);
    expect(chunkPlaySeek(media({ currentTime: 7 }), 5, 10)).toBeNull();
  });
});
