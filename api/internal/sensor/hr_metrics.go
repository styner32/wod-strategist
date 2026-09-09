package sensor

import "math"

type hrObservation struct {
	HREvent
	reset bool
}

// Only compact HR observations are retained; high-frequency ACC stays streaming.
// Delaying integration until end allows pause intervals to be applied correctly.
func calculateHRV2(events []hrObservation, pauses []PauseInterval, end float64, complete bool, opts ParseOptions) (HRMetrics, float64) {
	m := HRMetrics{ExcludedByReason: map[string]float64{}}
	duration := func(start, stop float64) float64 {
		start = math.Max(0, start)
		stop = math.Min(end, stop)
		if stop <= start {
			return 0
		}
		d := stop - start
		for _, p := range pauses {
			d -= math.Max(0, math.Min(stop, p.EndOffsetMs)-math.Max(start, p.StartOffsetMs))
		}
		return math.Max(0, d) / 1000
	}
	total := duration(0, end)
	q := HRQuality{}
	weighted, contactSeconds := 0.0, 0.0
	zones := [5]float64{}
	for i, e := range events {
		if e.Time < 0 || e.Time > end {
			continue
		}
		if e.reset {
			q.Reset(i > 0)
			continue
		}
		paused := false
		for _, p := range pauses {
			if e.Time >= p.StartOffsetMs && e.Time < p.EndOffsetMs {
				paused = true
				break
			}
		}
		if paused {
			q.Reset(false)
			continue
		}
		// A pause boundary must not carry an old baseline into the resumed workout.
		if i > 0 {
			for _, p := range pauses {
				if p.EndOffsetMs > events[i-1].Time && p.EndOffsetMs <= e.Time {
					q.Reset(false)
					break
				}
			}
		}
		reason := q.Evaluate(e.HREvent)
		// Instantaneous extrema include the final accepted sample; only timed
		// intervals contribute to averages, coverage and zones.
		if reason == "valid" || reason == "low" {
			if m.MinBPM == nil || e.BPM < *m.MinBPM {
				v := e.BPM
				m.MinBPM = &v
			}
			if m.PeakBPM == nil || e.BPM > *m.PeakBPM {
				v := e.BPM
				m.PeakBPM = &v
			}
		}

		stop := end
		if i+1 < len(events) {
			stop = events[i+1].Time
		}
		// Never bridge a dropout. The final sample has no following measurement,
		// so its tail remains unknown rather than assuming it lasted to recording end.
		if i+1 == len(events) || stop-e.Time >= 5000 {
			continue
		}
		seconds := duration(e.Time, stop)
		if seconds <= 0 {
			continue
		}
		if e.Contact != nil {
			contactSeconds += seconds
		}
		if reason != "valid" && reason != "low" {
			m.ExcludedSeconds += seconds
			m.ExcludedByReason[reason] += seconds
			continue
		}
		m.ValidSeconds += seconds
		weighted += float64(e.BPM) * seconds
		if reason == "low" {
			m.LowBPMSeconds += seconds
		}
		if opts.EstimatedMaxHR != nil && *opts.EstimatedMaxHR > 0 {
			ratio := float64(e.BPM) / float64(*opts.EstimatedMaxHR)
			z := 4
			for j, cutoff := range []float64{0.6, 0.7, 0.8, 0.9} {
				if ratio < cutoff {
					z = j
					break
				}
			}
			zones[z] += seconds
		}
	}
	m.UnknownSeconds = math.Max(0, total-m.ValidSeconds-m.ExcludedSeconds)
	if total > 0 {
		m.Coverage = m.ValidSeconds / total
		v := contactSeconds / total
		m.ContactCoverage = &v
	}
	m.ValidHR = complete && m.Coverage >= 0.5
	m.HasHRZones = opts.EstimatedMaxHR != nil && *opts.EstimatedMaxHR > 0
	m.EstimatedMaxHR = opts.EstimatedMaxHR
	if m.ValidSeconds > 0 {
		avg := weighted / m.ValidSeconds
		m.WeightedMeanBPM = &avg
		if m.HasHRZones {
			m.Zones = &HeartRateZones{
				Zone1Seconds: zones[0], Zone2Seconds: zones[1], Zone3Seconds: zones[2], Zone4Seconds: zones[3], Zone5Seconds: zones[4],
				Zone1Ratio: zones[0] / m.ValidSeconds, Zone2Ratio: zones[1] / m.ValidSeconds, Zone3Ratio: zones[2] / m.ValidSeconds, Zone4Ratio: zones[3] / m.ValidSeconds, Zone5Ratio: zones[4] / m.ValidSeconds,
			}
		}
	}
	bonus := 0.0
	if m.ValidHR && m.WeightedMeanBPM != nil {
		bonus = math.Max(0, math.Min(20, (*m.WeightedMeanBPM-140)*0.5))
	}
	return m, bonus
}
