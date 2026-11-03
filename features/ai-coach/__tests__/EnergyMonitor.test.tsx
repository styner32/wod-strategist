import React from 'react';
import { act, render } from '@testing-library/react-native';
import { AppState, type AppStateStatus } from 'react-native';
import * as Battery from 'expo-battery';
import { EnergyMonitor } from '../ui/EnergyMonitor';

let mockFocused = true;
jest.mock('expo-router/react-navigation', () => ({ useIsFocused: () => mockFocused }));
jest.mock('expo-battery', () => ({ getBatteryLevelAsync: jest.fn() }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

describe('EnergyMonitor battery polling', () => {
  let notifyAppState: (state: AppStateStatus) => void;
  let appStateSpy: jest.SpyInstance;
  let timeoutSpy: jest.SpyInstance;
  let clearTimeoutSpy: jest.SpyInstance;
  let removeListener: jest.Mock;
  const appStateDescriptor = Object.getOwnPropertyDescriptor(AppState, 'currentState');
  const readBattery = jest.mocked(Battery.getBatteryLevelAsync);
  const flush = () => act(async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); });
  const advance = (ms: number) => act(async () => { await jest.advanceTimersByTimeAsync(ms); });
  const pollTimers = () => timeoutSpy.mock.calls.filter(([, delay]) => delay === 10_000);
  const lastPollTimer = () => {
    const index = timeoutSpy.mock.calls.findLastIndex(([, delay]) => delay === 10_000);
    return timeoutSpy.mock.results[index].value;
  };

  beforeEach(() => {
    jest.useFakeTimers();
    jest.setSystemTime(100_000);
    mockFocused = true;
    readBattery.mockReset().mockResolvedValue(0.8);
    Object.defineProperty(AppState, 'currentState', { configurable: true, value: 'active' });
    removeListener = jest.fn();
    appStateSpy = jest.spyOn(AppState, 'addEventListener').mockImplementation((_type, callback) => {
      notifyAppState = callback;
      return { remove: removeListener };
    });
    timeoutSpy = jest.spyOn(global, 'setTimeout');
    clearTimeoutSpy = jest.spyOn(global, 'clearTimeout');
  });

  afterEach(() => {
    appStateSpy.mockRestore();
    timeoutSpy.mockRestore();
    clearTimeoutSpy.mockRestore();
    if (appStateDescriptor) Object.defineProperty(AppState, 'currentState', appStateDescriptor);
    jest.clearAllTimers();
    jest.useRealTimers();
  });

  it('does not create a timer after an initial battery read resolves following unmount', async () => {
    const pending = deferred<number>();
    readBattery.mockReturnValueOnce(pending.promise);
    const view = render(<EnergyMonitor label="Preview" />);
    expect(readBattery).toHaveBeenCalledTimes(1);
    view.unmount();
    await act(async () => pending.resolve(0.9));
    await flush();
    await advance(60_000);
    expect(readBattery).toHaveBeenCalledTimes(1);
    expect(pollTimers()).toHaveLength(0);
    expect(removeListener).toHaveBeenCalledTimes(1);
  });

  it('does no native work while unfocused and starts a single read when focused', async () => {
    mockFocused = false;
    const view = render(<EnergyMonitor label="Preview" />);
    await flush();
    await advance(30_000);
    expect(readBattery).not.toHaveBeenCalled();
    expect(pollTimers()).toHaveLength(0);
    mockFocused = true;
    view.rerender(<EnergyMonitor label="Preview" />);
    await flush();
    expect(readBattery).toHaveBeenCalledTimes(1);
    expect(view.getByText('80.0%')).toBeTruthy();
    const timer = lastPollTimer();
    view.unmount();
    expect(clearTimeoutSpy).toHaveBeenCalledWith(timer);
  });

  it.each<AppStateStatus>(['inactive', 'background'])('stops in %s and resumes on activation', async inactive => {
    Object.defineProperty(AppState, 'currentState', { configurable: true, value: inactive });
    const view = render(<EnergyMonitor label="Preview" />);
    await flush();
    expect(readBattery).not.toHaveBeenCalled();
    act(() => notifyAppState('active'));
    await flush();
    expect(readBattery).toHaveBeenCalledTimes(1);
    const timer = lastPollTimer();
    act(() => notifyAppState(inactive));
    expect(clearTimeoutSpy).toHaveBeenCalledWith(timer);
    await advance(30_000);
    expect(readBattery).toHaveBeenCalledTimes(1);
    act(() => notifyAppState('active'));
    await flush();
    expect(readBattery).toHaveBeenCalledTimes(2);
    view.unmount();
  });

  it('never overlaps a slow native read and polls ten seconds after it completes', async () => {
    const pending = deferred<number>();
    readBattery.mockReturnValueOnce(pending.promise);
    const view = render(<EnergyMonitor label="Preview" />);
    await advance(60_000);
    expect(readBattery).toHaveBeenCalledTimes(1);
    expect(pollTimers()).toHaveLength(0);
    await act(async () => pending.resolve(0.8));
    await flush();
    expect(pollTimers()).toHaveLength(1);
    await advance(9999);
    expect(readBattery).toHaveBeenCalledTimes(1);
    await advance(1);
    expect(readBattery).toHaveBeenCalledTimes(2);
    view.unmount();
  });

  it('continues after an inactive-to-active notification pair is batched into one render', async () => {
    const view = render(<EnergyMonitor label="Preview" />);
    await flush();
    act(() => { notifyAppState('inactive'); notifyAppState('active'); });
    await advance(10_000);
    expect(readBattery).toHaveBeenCalledTimes(2);
    view.unmount();
  });

  it.each<AppStateStatus>(['inactive', 'background'])('discards an initial read that finishes while %s', async inactive => {
    const pending = deferred<number>();
    readBattery.mockReturnValueOnce(pending.promise);
    const view = render(<EnergyMonitor label="Preview" />);
    act(() => notifyAppState(inactive));
    await act(async () => pending.resolve(0.9));
    await flush();
    await advance(30_000);
    expect(view.getByText('--%')).toBeTruthy();
    expect(view.queryByText('90.0%')).toBeNull();
    expect(readBattery).toHaveBeenCalledTimes(1);
    expect(pollTimers()).toHaveLength(0);
    view.unmount();
  });

  it('drops an in-flight poll on blur without updating its value or restarting its timer', async () => {
    const pending = deferred<number>();
    readBattery.mockResolvedValueOnce(0.8).mockReturnValueOnce(pending.promise).mockResolvedValueOnce(0.79);
    const view = render(<EnergyMonitor label="Preview" />);
    await flush();
    await advance(10_000);
    expect(readBattery).toHaveBeenCalledTimes(2);
    mockFocused = false;
    view.rerender(<EnergyMonitor label="Preview" />);
    await act(async () => pending.resolve(0.1));
    await flush();
    expect(view.getByText('80.0%')).toBeTruthy();
    expect(view.queryByText('10.0%')).toBeNull();
    expect(pollTimers()).toHaveLength(1);
    await advance(30_000);
    expect(readBattery).toHaveBeenCalledTimes(2);
    mockFocused = true;
    view.rerender(<EnergyMonitor label="Preview" />);
    await flush();
    expect(readBattery).toHaveBeenCalledTimes(3);
    expect(view.getByText('79.0%')).toBeTruthy();
    expect(view.getByText('0:40')).toBeTruthy();
    expect(view.getByText('90.0')).toBeTruthy();
    view.unmount();
  });

  it('waits for an old read on rapid refocus and ignores its stale result', async () => {
    const pending = deferred<number>();
    readBattery.mockReturnValueOnce(pending.promise).mockResolvedValueOnce(0.7);
    const view = render(<EnergyMonitor label="Preview" />);
    mockFocused = false;
    view.rerender(<EnergyMonitor label="Preview" />);
    mockFocused = true;
    view.rerender(<EnergyMonitor label="Preview" />);
    await flush();
    expect(readBattery).toHaveBeenCalledTimes(1);
    await act(async () => pending.resolve(0.9));
    await flush();
    expect(readBattery).toHaveBeenCalledTimes(2);
    expect(view.queryByText('90.0%')).toBeNull();
    expect(view.getByText('70.0%')).toBeTruthy();
    expect(pollTimers()).toHaveLength(1);
    view.unmount();
  });

  it('catches an initial native rejection and retries without duplicate initial reads', async () => {
    readBattery.mockRejectedValueOnce(new Error('Battery unavailable'));
    const view = render(<EnergyMonitor label="Preview" />);
    await flush();
    expect(readBattery).toHaveBeenCalledTimes(1);
    expect(view.getByText('--%')).toBeTruthy();
    expect(pollTimers()).toHaveLength(1);
    await advance(10_000);
    expect(readBattery).toHaveBeenCalledTimes(2);
    expect(view.getByText('80.0%')).toBeTruthy();
    view.unmount();
  });

  it('keeps the existing elapsed-time and percent-per-hour calculation', async () => {
    readBattery.mockResolvedValueOnce(0.8).mockResolvedValue(0.79);
    const view = render(<EnergyMonitor label="Preview" />);
    await flush();
    await advance(40_000);
    expect(view.getByText('79.0%')).toBeTruthy();
    expect(view.getByText('0:40')).toBeTruthy();
    expect(view.getByText('90.0')).toBeTruthy();
    view.unmount();
  });
});
