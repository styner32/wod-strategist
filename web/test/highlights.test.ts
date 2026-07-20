import {
  formatHighlightTimestamp,
  getHighlightSeekTime,
  parseHighlightSegments,
  parseHighlightTimestamp,
} from '../src/history/highlights';

describe('highlight parsing', () => {
  it('normalizes canonical highlights and fractional timestamps', () => {
    expect(parseHighlightSegments(JSON.stringify([
      {
        start: '04:52.125',
        end: '5:03',
        type: 'best_form',
        movement: 'Power Snatch',
        reason: 'Stable receiving position',
      },
    ]))).toEqual([
      {
        startSeconds: 292.125,
        endSeconds: 303,
        startLabel: '4:52.125',
        endLabel: '5:03',
        type: 'best_form',
        movement: 'Power Snatch',
        reason: 'Stable receiving position',
      },
    ]);
  });

  it('supports legacy numeric timestamp fields', () => {
    expect(parseHighlightSegments(JSON.stringify([
      { start_time: 1.5, end_time: 4.5, description: 'Good rep' },
      { start: 10, end: 12, type: 'key_moment' },
    ]))).toEqual([
      expect.objectContaining({
        startSeconds: 1.5,
        endSeconds: 4.5,
        type: 'highlight',
        reason: 'Good rep',
      }),
      expect.objectContaining({
        startSeconds: 10,
        endSeconds: 12,
        type: 'key_moment',
      }),
    ]);
  });

  it('drops malformed, negative, and non-positive intervals', () => {
    expect(parseHighlightSegments('')).toEqual([]);
    expect(parseHighlightSegments('[]')).toEqual([]);
    expect(parseHighlightSegments('{not json')).toEqual([]);
    expect(parseHighlightSegments(JSON.stringify([
      { start: '-1', end: '0:02' },
      { start: '0:05', end: '0:05' },
      { start: '0:10', end: '0:09' },
      { start: '0:61', end: '1:05' },
      { start: '1.5:05', end: '2:00' },
      null,
    ]))).toEqual([]);
  });

  it('parses long clocks and formats exact seconds', () => {
    expect(parseHighlightTimestamp('1:02:03.5')).toBe(3723.5);
    expect(formatHighlightTimestamp(3723.5)).toBe('1:02:03.5');
  });

  it('applies a five-second lead-in without seeking before zero', () => {
    expect(getHighlightSeekTime(12.5)).toBe(7.5);
    expect(getHighlightSeekTime(3)).toBe(0);
    expect(getHighlightSeekTime(30, 20)).toBe(20);
  });
});
