/** Capture-time observations; delayed exports must not use the current chunk's counters. */
export class CaptureWindow {
  private samples: { time: number; value: number }[] = [];

  add(time: number, value: number) {
    if (!Number.isFinite(time) || !Number.isFinite(value)) return;
    this.samples.push({ time, value });
    if (this.samples.length > 60000) this.samples.splice(0, 1000);
  }

  clear() { this.samples = []; }

  between(start: number, end: number) {
    return this.samples.filter((sample) => sample.time >= start && sample.time < end);
  }

  mean(start: number, end: number): number {
    const samples = this.between(start, end);
    return samples.length ? samples.reduce((sum, sample) => sum + sample.value, 0) / samples.length : 0;
  }

  peak(start: number, end: number): number | undefined {
    const samples = this.between(start, end);
    return samples.length ? samples.reduce((max, sample) => Math.max(max, sample.value), -Infinity) : undefined;
  }
}
