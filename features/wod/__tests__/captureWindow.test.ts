import { CaptureWindow } from '../captureWindow';

it('uses the capture interval even when later observations precede the export callback', () => {
  const window = new CaptureWindow();
  window.add(1000, 110);
  window.add(9999, 150);
  window.add(10000, 180);
  window.add(15000, 190);
  expect(window.peak(0, 10000)).toBe(150);
  expect(window.peak(10000, 20000)).toBe(190);
  expect(window.peak(20000, 20001)).toBeUndefined();
  window.clear();
  expect(window.peak(0, 20000)).toBeUndefined();
});

it('does not assign observations from a pause or another interval to a short tail', () => {
  const window = new CaptureWindow();
  window.add(100, 1);
  window.add(1000, 0);
  window.add(1080, 1);
  expect(window.mean(1000, 1080)).toBe(0);
  expect(window.mean(1080, 1100)).toBe(1);
});
