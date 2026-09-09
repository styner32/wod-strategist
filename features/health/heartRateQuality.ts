/** App heuristic, not a medical confidence score. Keep in sync with sensor/hr_quality.go. */
export type HeartRateQualityReason = "valid" | "low" | "contact_loss" | "invalid" | "sudden_drop" | "recovering" | "missing";
export interface HeartRateReading {
  bpm: number | null;
  reason: HeartRateQualityReason;
  receivedAt: number;
}
export class HeartRateQuality {
  private history: { t: number; bpm: number }[] = [];
  private last: number | null = null;
  private recoveryStart: number | null = null;
  private recovering = false;
  private baseline = 0;

  reset(recovering = false) {
    this.history = [];
    this.last = null;
    this.recoveryStart = null;
    this.recovering = recovering;
    this.baseline = 0;
  }

  markMissing() {
    // A dropout restarts recovery, but cannot bypass a frozen drop baseline.
    this.history = [];
    this.last = null;
    this.recoveryStart = null;
    this.recovering = true;
  }

  update(t: number, bpm: number, contact?: boolean): HeartRateReading {
    const reject = (reason: HeartRateQualityReason): HeartRateReading => {
      this.recovering = true;
      this.recoveryStart = null;
      return { bpm: null, reason, receivedAt: t };
    };
    if (!Number.isFinite(t) || (this.last !== null && t <= this.last)) return reject("invalid");
    if (this.last !== null && t - this.last >= 5000) this.markMissing();
    this.last = t;
    this.history = this.history.filter((p) => t - p.t <= 10000);
    if (contact === false) return reject("contact_loss");
    if (!Number.isFinite(bpm) || bpm < 30 || bpm > 240) return reject("invalid");
    if (!this.baseline && this.history.length >= 3) {
      const sorted = this.history.map((p) => p.bpm).sort((a, b) => a - b);
      const mid = Math.floor(sorted.length / 2);
      const median = sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
      const recent = this.history[this.history.length - 1];
      if (t - recent.t <= 5000 && median - bpm >= 30 && bpm <= median * 0.7) {
        this.baseline = median;
        return reject("sudden_drop");
      }
    }
    if (this.recovering) {
      if (this.baseline && bpm < this.baseline * 0.7) return reject("sudden_drop");
      if (this.recoveryStart === null) this.recoveryStart = t;
      if (t - this.recoveryStart < 5000) return { bpm: null, reason: "recovering", receivedAt: t };
      this.recovering = false;
      this.baseline = 0;
      this.history = [];
    }
    this.history.push({ t, bpm });
    return { bpm, reason: bpm < 55 ? "low" : "valid", receivedAt: t };
  }
}
