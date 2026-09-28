import { advanceAppleAiProtection as next, initialAppleAiProtection as initial } from '../appleAiProtection';

describe('Apple AI power protection', () => {
  const normal = { battery: 0.8, lowPower: false, thermal: 0 };
  it('uses a 15% entry threshold, 20% recovery threshold and 30 seconds of recovery', () => {
    let state = next(initial, { ...normal, battery: 0.15 }, 0);
    expect(state.reason).toBe('power');
    state = next(state, { ...normal, battery: 0.19 }, 5000);
    expect(state.reason).toBe('power');
    state = next(state, { ...normal, battery: 0.2 }, 10_000);
    expect(state.reason).toBe('recovering');
    expect(next(state, normal, 39_999).reason).toBe('recovering');
    expect(next(state, normal, 40_000).reason).toBeNull();
  });
  it('does not interpret unknown battery as empty or clear an established protection on read failure', () => {
    const unknown = { battery: -1, lowPower: null, thermal: -1 };
    expect(next(initial, unknown, 0).reason).toBeNull();
    const blocked = next(initial, { battery: 0.1, lowPower: true, thermal: 2 }, 0);
    expect(next(blocked, unknown, 60_000)).toMatchObject({ reason: 'thermal', batteryLimited: true, lowPowerLimited: true });
  });
  it('keeps OS power saving independent of battery level and restarts recovery after reheating', () => {
    let state = next(initial, { ...normal, lowPower: true }, 0);
    expect(state.reason).toBe('power');
    state = next(state, normal, 5000);
    state = next(state, { ...normal, thermal: 3 }, 34_000);
    expect(state.reason).toBe('thermal');
    state = next(state, normal, 35_000);
    expect(next(state, normal, 64_999).reason).toBe('recovering');
    expect(next(state, normal, 65_000).reason).toBeNull();
  });
});
