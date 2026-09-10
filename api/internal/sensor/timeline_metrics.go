package sensor

import (
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

type TimelineBuilder struct {
	buckets       map[int64]*accBucketAccumulator
	maxBucketIdx  int64
	currentStream int64
	streamHz      map[int64]float64
	streamRange   map[int64]int
}

func NewTimelineBuilder() *TimelineBuilder {
	return &TimelineBuilder{
		buckets:     make(map[int64]*accBucketAccumulator),
		streamHz:    make(map[int64]float64),
		streamRange: make(map[int64]int),
	}
}

func (tb *TimelineBuilder) HandleStreamStart(sse StreamStartEvent) {
	if sse.Sampling.ACCHz > 0 {
		tb.streamHz[sse.StreamID] = sse.Sampling.ACCHz
	}
	if sse.Sampling.ACCRangeG > 0 {
		tb.streamRange[sse.StreamID] = sse.Sampling.ACCRangeG
	}
	tb.currentStream = sse.StreamID

	// If a bucket has already received samples from a previous stream within this current second,
	// mark that 1-second bucket as invalid due to stream reconfiguration.
	for _, b := range tb.buckets {
		if b.bucketIdx == tb.maxBucketIdx && b.streamID != 0 && b.streamID != sse.StreamID {
			b.hasError = true
			b.errorReason = "stream_changed"
		}
	}
}

func (tb *TimelineBuilder) HandleAcc(acce AccEvent) {
	hz, hasHz := tb.streamHz[acce.StreamID]
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
		if len(sample) < 3 {
			continue
		}
		sampleTMs := acce.Time + float64(i)*dt
		if sampleTMs < 0 {
			continue
		}

		bucketIdx := int64(math.Floor(sampleTMs / 1000.0))
		if bucketIdx > tb.maxBucketIdx {
			tb.maxBucketIdx = bucketIdx
		}

		b, exists := tb.buckets[bucketIdx]
		if !exists {
			b = &accBucketAccumulator{
				bucketIdx:   bucketIdx,
				streamID:    acce.StreamID,
				hz:          hz,
				rangeG:      rangeG,
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

		m := math.Sqrt(x*x + y*y + z*z)
		b.sampleCount++
		delta := m - b.mean
		b.mean += delta / float64(b.sampleCount)
		delta2 := m - b.mean
		b.m2 += delta * delta2
	}
}

func (tb *TimelineBuilder) Build(
	source TimelineSource,
	durationMs float64,
	pauses []PauseInterval,
	hrEvents []hrObservation,
) *SensorTimelineData {
	if durationMs <= 0 {
		return &SensorTimelineData{
			SchemaVersion: 1,
			Clock:         "capture_clock",
			BucketMs:      1000,
			DurationMs:    0,
			Source:        source,
			Points:        []SensorTimelinePoint{},
			Gaps:          []TimelineGap{},
			Pauses:        []TimelinePause{},
		}
	}

	totalDurationInt := int64(math.Round(durationMs))
	numBuckets := int64(math.Ceil(durationMs / 1000.0))

	// Pre-extract HR evaluated intervals using V2 quality rules
	_, _, hrIntervals := extractHREvaluationsV2(hrEvents, pauses, durationMs)

	// Keep only valid and low reason intervals for BPM integration
	var validHRIntervals []EvaluatedHRInterval
	for _, iv := range hrIntervals {
		if iv.Reason == "valid" || iv.Reason == "low" {
			validHRIntervals = append(validHRIntervals, iv)
		}
	}

	timelinePauses := make([]TimelinePause, 0, len(pauses))
	for _, p := range pauses {
		s := int64(math.Max(0, math.Round(p.StartOffsetMs)))
		e := int64(math.Min(float64(totalDurationInt), math.Round(p.EndOffsetMs)))
		if e > s {
			timelinePauses = append(timelinePauses, TimelinePause{StartMs: s, EndMs: e})
		}
	}

	points := make([]SensorTimelinePoint, 0, numBuckets)
	gaps := make([]TimelineGap, 0)

	type bucketData struct {
		startMs int64
		endMs   int64
		hrVal   TimelineValue
		accVal  TimelineValue
	}
	allBuckets := make([]bucketData, 0, numBuckets)

	for idx := int64(0); idx < numBuckets; idx++ {
		startMs := idx * 1000
		endMs := (idx + 1) * 1000
		if endMs > totalDurationInt {
			endMs = totalDurationInt
		}
		bucketDurMs := float64(endMs - startMs)
		if bucketDurMs <= 0 {
			continue
		}

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
			if !hasBucket || b.sampleCount == 0 {
				accVal = TimelineValue{Status: StatusMissing, Reason: "no_samples"}
			} else if b.hasError {
				accVal = TimelineValue{Status: StatusInvalid, Reason: b.errorReason}
			} else if b.hz <= 0 {
				accVal = TimelineValue{Status: StatusInvalid, Reason: "unknown_hz"}
			} else {
				nominalDt := 1000.0 / b.hz
				expectedSamples := (bucketDurMs / 1000.0) * b.hz
				minRequiredSamples := int(math.Ceil(0.80 * expectedSamples))
				if minRequiredSamples < 2 {
					minRequiredSamples = 2
				}
				maxAllowedGap := 3.0 * nominalDt

				startEdgeGap := b.firstSampleT - float64(startMs)
				endEdgeGap := float64(endMs) - b.lastSampleT

				if b.sampleCount < minRequiredSamples {
					accVal = TimelineValue{Status: StatusInsufficient, Reason: "insufficient_samples"}
				} else if b.maxGapMs > maxAllowedGap {
					accVal = TimelineValue{Status: StatusInsufficient, Reason: "gap_too_large"}
				} else if startEdgeGap > maxAllowedGap {
					accVal = TimelineValue{Status: StatusInsufficient, Reason: "gap_at_start"}
				} else if endEdgeGap > maxAllowedGap {
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

			// HR evaluation
			validOverlapMs := 0.0
			weightedBPMSum := 0.0
			for _, iv := range validHRIntervals {
				overlapStart := math.Max(float64(startMs), iv.StartMs)
				overlapEnd := math.Min(float64(endMs), iv.EndMs)
				if overlapEnd > overlapStart {
					overlap := overlapEnd - overlapStart
					validOverlapMs += overlap
					weightedBPMSum += float64(iv.BPM) * overlap
				}
			}

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

		allBuckets = append(allBuckets, bucketData{
			startMs: startMs,
			endMs:   endMs,
			hrVal:   hrVal,
			accVal:  accVal,
		})
	}

	// 2. Build points and detect long gaps (>= 5 consecutive seconds with no active data)
	inBothGap := false
	gapStartMs := int64(0)
	gapCount := 0

	for i := 0; i < len(allBuckets); i++ {
		bd := allBuckets[i]
		bothMissing := (bd.hrVal.Status == StatusMissing || bd.hrVal.Status == StatusInsufficient) &&
			(bd.accVal.Status == StatusMissing || bd.accVal.Status == StatusInsufficient)

		if bothMissing {
			if !inBothGap {
				inBothGap = true
				gapStartMs = bd.startMs
				gapCount = 1
			} else {
				gapCount++
			}
		} else {
			if inBothGap {
				if gapCount >= 5 {
					gaps = append(gaps, TimelineGap{
						StartMs: gapStartMs,
						EndMs:   allBuckets[i-1].endMs,
						Channel: "both",
						Reason:  "missing_sensor_data",
					})
				} else {
					// Add back the points for short missing intervals
					for j := i - gapCount; j < i; j++ {
						points = append(points, SensorTimelinePoint{
							StartMs:          allBuckets[j].startMs,
							EndMs:            allBuckets[j].endMs,
							HeartRateBPM:     allBuckets[j].hrVal,
							AccMagnitudeStdG: allBuckets[j].accVal,
						})
					}
				}
				inBothGap = false
				gapCount = 0
			}
			points = append(points, SensorTimelinePoint{
				StartMs:          bd.startMs,
				EndMs:            bd.endMs,
				HeartRateBPM:     bd.hrVal,
				AccMagnitudeStdG: bd.accVal,
			})
		}
	}

	if inBothGap {
		if gapCount >= 5 {
			gaps = append(gaps, TimelineGap{
				StartMs: gapStartMs,
				EndMs:   allBuckets[len(allBuckets)-1].endMs,
				Channel: "both",
				Reason:  "missing_sensor_data",
			})
		} else {
			for j := len(allBuckets) - gapCount; j < len(allBuckets); j++ {
				points = append(points, SensorTimelinePoint{
					StartMs:          allBuckets[j].startMs,
					EndMs:            allBuckets[j].endMs,
					HeartRateBPM:     allBuckets[j].hrVal,
					AccMagnitudeStdG: allBuckets[j].accVal,
				})
			}
		}
	}

	sort.Slice(gaps, func(i, j int) bool {
		return gaps[i].StartMs < gaps[j].StartMs
	})

	return &SensorTimelineData{
		SchemaVersion: 1,
		Clock:         "capture_clock",
		BucketMs:      1000,
		DurationMs:    totalDurationInt,
		Source:        source,
		Points:        points,
		Gaps:          gaps,
		Pauses:        timelinePauses,
	}
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

