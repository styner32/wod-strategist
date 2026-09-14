import fixtures from '../../../shared/__fixtures__/heart-rate-quality.json';
import { HeartRateQuality } from '../heartRateQuality';

describe('shared heart rate quality contract', () => {
  it.each(fixtures)('$name', ({ readings }) => {
    const quality = new HeartRateQuality();
    for (const reading of readings) {
      if ('reset' in reading && reading.reset) quality.reset(true);
      const result = quality.update(reading.t, reading.bpm, 'contact' in reading ? reading.contact : undefined);
      expect(result.reason).toBe(reading.reason);
      expect(result.bpm).toBe(['valid', 'low'].includes(reading.reason) ? reading.bpm : null);
    }
  });
  it('resets for a new session and rejects malformed measurements', () => {
    const q = new HeartRateQuality();
    expect(q.update(0, NaN).bpm).toBeNull();
    q.reset();
    expect(q.update(0, 45).reason).toBe('low');
    expect(q.update(0, 150).reason).toBe('invalid');
  });
});
