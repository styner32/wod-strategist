package sensor

import (
	"errors"
	"math"
	"sort"
)

type SampleStatus string

const (
	StatusValid        SampleStatus = "valid"
	StatusMissing      SampleStatus = "missing"
	StatusInsufficient SampleStatus = "insufficient"
	StatusInvalid      SampleStatus = "invalid"
	StatusPaused       SampleStatus = "paused"
)

type TimelineValue struct {
	Value  *float64     `json:"value"`
	Status SampleStatus `json:"status"`
	Reason string       `json:"reason,omitempty"`
}

type SensorTimelinePoint struct {
	StartMs          int64         `json:"start_ms"`
	EndMs            int64         `json:"end_ms"`
	HeartRateBPM     TimelineValue `json:"heart_rate_bpm"`
	AccMagnitudeStdG TimelineValue `json:"acc_magnitude_std_g"`
}

type TimelineGap struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Channel string `json:"channel"` // "heart_rate" | "acc" | "both"
	Reason  string `json:"reason"`
}

type TimelinePause struct {
	StartMs int64 `json:"start_ms"`
	EndMs   int64 `json:"end_ms"`
}

type TimelineSource struct {
	SensorVersion        string `json:"sensor_version"`
	RequestID            string `json:"request_id"`
	SourceGeneration     string `json:"source_generation"`
	HRCalculationVersion int    `json:"hr_calculation_version"`
}

type SensorTimelineData struct {
	SchemaVersion int                   `json:"schema_version"`
	Clock         string                `json:"clock"` // "capture_clock"
	BucketMs      int64                 `json:"bucket_ms"`
	DurationMs    int64                 `json:"duration_ms"`
	Source        TimelineSource        `json:"source"`
	Points        []SensorTimelinePoint `json:"points"`
	Gaps          []TimelineGap         `json:"gaps"`
	Pauses        []TimelinePause       `json:"pauses"`
}

type accBucketAccumulator struct {
	bucketIdx    int64
	streamID     int64
	hz           float64
	rangeG       int
	sampleCount  int
	firstSampleT float64
	lastSampleT  float64
	maxGapMs     float64
	mean         float64
	m2           float64
	hasError     bool
	errorReason  string
}

// MaxTimelineBuckets bounds result size independently of elapsed time. Empty
// hours are represented as gaps, not allocated as one object per second.
const MaxTimelineBuckets = 100000

// JSON numbers are consumed as JavaScript numbers by the graph. Stay within its
// exact integer range, with room for the final bucket's end calculation.
const maxTimelineTimeMs = float64(1<<53 - 1001)

type timelineAccumulator struct {
	buckets        map[int64]*accBucketAccumulator
	currentStream  int64
	hasStream      bool
	streamHz       map[int64]float64
	streamRange    map[int64]int
	streamLastTime map[int64]float64
	streamChanges  map[int64]bool
	durationMs     float64
	err            error
}

func newTimelineAccumulator(durationMs float64) *timelineAccumulator {
	return &timelineAccumulator{
		buckets:        make(map[int64]*accBucketAccumulator),
		streamHz:       make(map[int64]float64),
		streamRange:    make(map[int64]int),
		streamLastTime: make(map[int64]float64),
		streamChanges:  make(map[int64]bool),
		durationMs:     durationMs,
	}
}

func validTimelineTime(t float64) bool {
	return !math.IsNaN(t) && !math.IsInf(t, 0) && t >= 0 && t <= maxTimelineTimeMs
}

func (tb *timelineAccumulator) HandleStreamStart(sse StreamStartEvent) {
	if !validTimelineTime(sse.Time) {
		tb.err = errors.New("invalid_timeline_timestamp")
		return
	}
	if sse.Time >= tb.durationMs {
		return
	}
	// Only the bucket actually containing a reconfiguration can be affected.
	// A reconnect after a gap must not change an earlier completed bucket.
	if tb.hasStream {
		idx := int64(math.Floor(sse.Time / 1000))
		// Remember a transition even if that second's first ACC packet arrives
		// later. An exact boundary does not straddle two configurations.
		if sse.Time > float64(idx*1000) {
			tb.streamChanges[idx] = true
		}
		if b := tb.buckets[idx]; b != nil {
			b.hasError = true
			b.errorReason = "stream_changed"
		}
	}
	tb.streamHz[sse.StreamID] = sse.Sampling.ACCHz
	tb.streamRange[sse.StreamID] = sse.Sampling.ACCRangeG
	delete(tb.streamLastTime, sse.StreamID)
	tb.currentStream = sse.StreamID
	tb.hasStream = true
}

func (tb *timelineAccumulator) HandleAcc(acce AccEvent) {
	if tb.err != nil {
		return
	}
	if math.IsNaN(acce.Time) || math.IsInf(acce.Time, 0) || acce.Time > maxTimelineTimeMs || math.IsNaN(acce.Dt) || math.IsInf(acce.Dt, 0) {
		tb.err = errors.New("invalid_timeline_timestamp")
		return
	}
	hz, hasHz := tb.streamHz[acce.StreamID]
	hasHz = hasHz && hz > 0 && !math.IsInf(hz, 0) && !math.IsNaN(hz)
	rangeG := tb.streamRange[acce.StreamID]

	dt := acce.Dt
	if dt <= 0 {
		if hasHz && hz > 0 {
			dt = 1000.0 / hz
		} else {
			dt = 0
		}
	}

	for i, sample := range acce.V {
		sampleTMs := acce.Time + float64(i)*dt
		if sampleTMs < 0 {
			continue
		}
		if !validTimelineTime(sampleTMs) {
			tb.err = errors.New("invalid_timeline_timestamp")
			return
		}
		// Apply the authoritative footer cutoff before any statistics or errors.
		if sampleTMs >= tb.durationMs {
			continue
		}
		bucketIdx := int64(math.Floor(sampleTMs / 1000.0))
		previousT, hasPrevious := tb.streamLastTime[acce.StreamID]
		reversed := hasPrevious && sampleTMs <= previousT
		if !hasPrevious || sampleTMs > previousT {
			tb.streamLastTime[acce.StreamID] = sampleTMs
		}

		b, exists := tb.buckets[bucketIdx]
		if !exists {
			if len(tb.buckets) >= MaxTimelineBuckets {
				tb.err = errors.New("timeline_bucket_limit_exceeded")
				return
			}
			b = &accBucketAccumulator{
				bucketIdx:    bucketIdx,
				streamID:     acce.StreamID,
				hz:           hz,
				rangeG:       rangeG,
				firstSampleT: sampleTMs,
				lastSampleT:  sampleTMs,
			}
			if !hasHz || hz <= 0 {
				b.hasError = true
				b.errorReason = "unknown_hz"
			}
			tb.buckets[bucketIdx] = b
		} else {
			if b.streamID != acce.StreamID {
				b.hasError = true
				b.errorReason = "stream_changed"
			}
		}

		if tb.streamChanges[bucketIdx] {
			b.hasError = true
			b.errorReason = "stream_changed"
		}
		if reversed {
			b.hasError = true
			b.errorReason = "timestamp_reversed"
		}
		if tb.hasStream && tb.currentStream != acce.StreamID {
			b.hasError = true
			b.errorReason = "stream_changed"
		}
		if len(sample) < 3 {
			b.hasError = true
			b.errorReason = "invalid_sample"
			continue
		}
		x, y, z := sample[0], sample[1], sample[2]
		if math.IsNaN(x) || math.IsNaN(y) || math.IsNaN(z) || math.IsInf(x, 0) || math.IsInf(y, 0) || math.IsInf(z, 0) {
			b.hasError = true
			b.errorReason = "invalid_sample"
			continue
		}

		if rangeG > 0 {
			maxAllowed := float64(rangeG)
			if math.Abs(x) >= maxAllowed || math.Abs(y) >= maxAllowed || math.Abs(z) >= maxAllowed {
				b.hasError = true
				b.errorReason = "saturation"
			}
		}

		if b.sampleCount > 0 {
			if sampleTMs < b.lastSampleT {
				b.hasError = true
				b.errorReason = "timestamp_reversed"
			} else {
				gap := sampleTMs - b.lastSampleT
				if gap > b.maxGapMs {
					b.maxGapMs = gap
				}
			}
		} else {
			b.firstSampleT = sampleTMs
		}
		b.lastSampleT = sampleTMs

		m := math.Hypot(math.Hypot(x, y), z)
		if math.IsInf(m, 0) {
			b.hasError = true
			b.errorReason = "invalid_sample"
			continue
		}
		b.sampleCount++
		delta := m - b.mean
		b.mean += delta / float64(b.sampleCount)
		delta2 := m - b.mean
		b.m2 += delta * delta2
	}
}

func (tb *timelineAccumulator) Build(source TimelineSource, durationMs float64, pauses []PauseInterval, hrEvents []hrObservation) (*SensorTimelineData, error) {
	if tb.err != nil {
		return nil, tb.err
	}
	totalDurationInt := int64(math.Round(durationMs))
	timeline := &SensorTimelineData{
		SchemaVersion: 1, Clock: "capture_clock", BucketMs: 1000,
		DurationMs: totalDurationInt, Source: source,
		Points: []SensorTimelinePoint{}, Gaps: []TimelineGap{}, Pauses: []TimelinePause{},
	}
	for _, p := range pauses {
		if !validTimelineTime(p.StartOffsetMs) || !validTimelineTime(p.EndOffsetMs) || p.EndOffsetMs < p.StartOffsetMs {
			return nil, errors.New("invalid_timeline_pause")
		}
		start := int64(math.Min(durationMs, math.Round(p.StartOffsetMs)))
		end := int64(math.Min(durationMs, math.Round(p.EndOffsetMs)))
		if end > start {
			timeline.Pauses = append(timeline.Pauses, TimelinePause{StartMs: start, EndMs: end})
		}
	}
	if totalDurationInt <= 0 {
		return timeline, nil
	}
	indices := make(map[int64]struct{}, len(tb.buckets))
	for idx := range tb.buckets {
		if idx*1000 < totalDurationInt {
			indices[idx] = struct{}{}
		}
	}
	type hrBucket struct{ validMs, weightedBPM float64 }
	hrBuckets := make(map[int64]hrBucket)
	_, _, intervals := extractHREvaluationsV2(hrEvents, pauses, durationMs)
	for _, iv := range intervals {
		if iv.Reason != "valid" && iv.Reason != "low" {
			continue
		}
		start, stop := math.Max(0, iv.StartMs), math.Min(float64(totalDurationInt), iv.EndMs)
		if !validTimelineTime(start) || !validTimelineTime(stop) {
			return nil, errors.New("invalid_timeline_timestamp")
		}
		for cursor := start; cursor < stop; {
			idx := int64(math.Floor(cursor / 1000))
			end := math.Min(float64((idx+1)*1000), stop)
			if _, exists := indices[idx]; !exists && len(indices) >= MaxTimelineBuckets {
				return nil, errors.New("timeline_bucket_limit_exceeded")
			}
			indices[idx] = struct{}{}
			hr := hrBuckets[idx]
			hr.validMs += end - cursor
			hr.weightedBPM += float64(iv.BPM) * (end - cursor)
			hrBuckets[idx] = hr
			cursor = end
		}
	}
	ordered := make([]int64, 0, len(indices))
	for idx := range indices {
		ordered = append(ordered, idx)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	// Only observed seconds are evaluated. Empty spans are appended directly as
	// gaps; this stays bounded even when a tiny input claims years of elapsed time.
	appendEmpty := func(start, end int64) error {
		if end <= start {
			return nil
		}
		if end-start >= 5000 {
			timeline.Gaps = append(timeline.Gaps, TimelineGap{StartMs: start, EndMs: end, Channel: "both", Reason: "missing_sensor_data"})
			return nil
		}
		for start < end {
			stop := min(start+1000, end)
			if len(timeline.Points) >= MaxTimelineBuckets {
				return errors.New("timeline_bucket_limit_exceeded")
			}
			point := SensorTimelinePoint{StartMs: start, EndMs: stop,
				HeartRateBPM:     TimelineValue{Status: StatusMissing, Reason: "no_valid_hr"},
				AccMagnitudeStdG: TimelineValue{Status: StatusMissing, Reason: "no_samples"},
			}
			for _, p := range timeline.Pauses {
				if p.StartMs < stop && p.EndMs > start {
					point.HeartRateBPM = TimelineValue{Status: StatusPaused}
					point.AccMagnitudeStdG = TimelineValue{Status: StatusPaused}
					break
				}
			}
			timeline.Points = append(timeline.Points, point)
			start = stop
		}
		return nil
	}
	cursor := int64(0)
	for _, idx := range ordered {
		startMs := idx * 1000
		endMs := min((idx+1)*1000, totalDurationInt)
		if err := appendEmpty(cursor, startMs); err != nil {
			return nil, err
		}
		bucketDurMs := float64(endMs - startMs)
		// 1. Check if bucket overlaps any pause interval
		isPaused := false
		for _, p := range pauses {
			overlapStart := math.Max(float64(startMs), p.StartOffsetMs)
			overlapEnd := math.Min(float64(endMs), p.EndOffsetMs)
			if overlapEnd > overlapStart {
				isPaused = true
				break
			}
		}

		var hrVal TimelineValue
		var accVal TimelineValue

		if isPaused {
			hrVal = TimelineValue{Status: StatusPaused}
			accVal = TimelineValue{Status: StatusPaused}
		} else {
			// ACC evaluation
			b, hasBucket := tb.buckets[idx]
			if hasBucket && b.hasError {
				accVal = TimelineValue{Status: StatusInvalid, Reason: b.errorReason}
			} else if !hasBucket || b.sampleCount == 0 {
				accVal = TimelineValue{Status: StatusMissing, Reason: "no_samples"}
			} else if b.hz <= 0 {
				accVal = TimelineValue{Status: StatusInvalid, Reason: "unknown_hz"}
			} else {
				nominalDt := 1000.0 / b.hz
				expectedSamples := (bucketDurMs / 1000.0) * b.hz
				minRequiredSamples := math.Max(2, math.Ceil(0.80*expectedSamples))
				maxAllowedGap := 3.0 * nominalDt

				startEdgeGap := b.firstSampleT - float64(startMs)
				endEdgeGap := float64(endMs) - b.lastSampleT

				if float64(b.sampleCount) < minRequiredSamples {
					accVal = TimelineValue{Status: StatusInsufficient, Reason: "insufficient_samples"}
				} else if b.maxGapMs > maxAllowedGap {
					accVal = TimelineValue{Status: StatusInsufficient, Reason: "gap_too_large"}
				} else if startEdgeGap > maxAllowedGap {
					accVal = TimelineValue{Status: StatusInsufficient, Reason: "gap_at_start"}
				} else if endEdgeGap < 0 || endEdgeGap > maxAllowedGap {
					accVal = TimelineValue{Status: StatusInsufficient, Reason: "gap_at_end"}
				} else {
					popVariance := b.m2 / float64(b.sampleCount)
					if popVariance < 0 {
						popVariance = 0
					}
					popStdDev := math.Sqrt(popVariance)
					rounded := math.Round(popStdDev*10000) / 10000
					accVal = TimelineValue{Value: &rounded, Status: StatusValid}
				}
			}

			// HR overlaps were integrated once into the same sparse bucket map.
			hr := hrBuckets[idx]
			validOverlapMs, weightedBPMSum := hr.validMs, hr.weightedBPM

			if validOverlapMs >= 0.80*bucketDurMs {
				meanBPM := weightedBPMSum / validOverlapMs
				rounded := math.Round(meanBPM*10) / 10
				hrVal = TimelineValue{Value: &rounded, Status: StatusValid}
			} else if validOverlapMs > 0 {
				hrVal = TimelineValue{Status: StatusInsufficient, Reason: "coverage_below_80_percent"}
			} else {
				hrVal = TimelineValue{Status: StatusMissing, Reason: "no_valid_hr"}
			}
		}

		if len(timeline.Points) >= MaxTimelineBuckets {
			return nil, errors.New("timeline_bucket_limit_exceeded")
		}
		timeline.Points = append(timeline.Points, SensorTimelinePoint{StartMs: startMs, EndMs: endMs, HeartRateBPM: hrVal, AccMagnitudeStdG: accVal})
		cursor = endMs
	}
	if err := appendEmpty(cursor, totalDurationInt); err != nil {
		return nil, err
	}
	return timeline, nil
}

// BuildWithHREvents is a helper for testing or callers that have raw HREvent slices.
func (tb *TimelineBuilder) BuildWithHREvents(
	source TimelineSource,
	durationMs float64,
	pauses []PauseInterval,
	rawHREvents []HREvent,
) *SensorTimelineData {
	obs := make([]hrObservation, len(rawHREvents))
	for i, e := range rawHREvents {
		obs[i] = hrObservation{HREvent: e}
	}
	return tb.Build(source, durationMs, pauses, obs)
}
