package sensor

import "sort"

// HRQuality is an app heuristic, not a medical confidence score.
// Keep in sync with features/health/heartRateQuality.ts and the shared fixtures.
type HRQuality struct {
	history       []HREvent
	last          *float64
	recoveryStart *float64
	recovering    bool
	baseline      float64
}

func (q *HRQuality) Reset(recovering bool) { *q = HRQuality{recovering: recovering} }

// MarkMissing restarts recovery without clearing a frozen sudden-drop baseline.
func (q *HRQuality) MarkMissing() {
	q.history = nil
	q.last = nil
	q.recoveryStart = nil
	q.recovering = true
}
func (q *HRQuality) Evaluate(e HREvent) string {
	reject := func(reason string) string { q.recovering = true; q.recoveryStart = nil; return reason }
	if q.last != nil && e.Time <= *q.last {
		return reject("invalid")
	}
	if q.last != nil && e.Time-*q.last >= 5000 {
		q.MarkMissing()
	}
	t := e.Time
	q.last = &t
	kept := q.history[:0]
	for _, p := range q.history {
		if t-p.Time <= 10000 {
			kept = append(kept, p)
		}
	}
	q.history = kept
	if e.Contact != nil && !*e.Contact {
		return reject("contact_loss")
	}
	if e.BPM < 30 || e.BPM > 240 {
		return reject("invalid")
	}
	if q.baseline == 0 && len(q.history) >= 3 {
		values := make([]float64, len(q.history))
		for i, p := range q.history {
			values[i] = float64(p.BPM)
		}
		sort.Float64s(values)
		mid := len(values) / 2
		median := values[mid]
		if len(values)%2 == 0 {
			median = (values[mid-1] + values[mid]) / 2
		}
		if t-q.history[len(q.history)-1].Time <= 5000 && median-float64(e.BPM) >= 30 && float64(e.BPM) <= median*0.7 {
			q.baseline = median
			return reject("sudden_drop")
		}
	}
	if q.recovering {
		if q.baseline > 0 && float64(e.BPM) < q.baseline*0.7 {
			return reject("sudden_drop")
		}
		if q.recoveryStart == nil {
			q.recoveryStart = &t
		}
		if t-*q.recoveryStart < 5000 {
			return "recovering"
		}
		q.recovering = false
		q.baseline = 0
		q.history = nil
	}
	q.history = append(q.history, e)
	if e.BPM < 55 {
		return "low"
	}
	return "valid"
}
