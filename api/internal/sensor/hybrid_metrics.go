package sensor

import (
	"math"
	"sort"
	"strings"
)

const (
	hybridHeartWeight  = 0.75
	hybridMotionWeight = 0.25
	hybridRollingSec   = 5
)

// ResolveENMOCalibration returns the frozen ENMO calibration inputs (A_rest, A_ref, version),
// falling back to fixed Polar H10 chest-strap bootstrap constants instead of per-session max.
func ResolveENMOCalibration(opts ParseOptions) (restG, refG float64, version string) {
	if opts.ENMORestG != nil && opts.ENMORefG != nil &&
		!math.IsNaN(*opts.ENMORestG) && !math.IsInf(*opts.ENMORestG, 0) &&
		!math.IsNaN(*opts.ENMORefG) && !math.IsInf(*opts.ENMORefG, 0) &&
		*opts.ENMORestG >= 0 && *opts.ENMORefG > *opts.ENMORestG {
		ver := strings.TrimSpace(opts.CalibrationVersion)
		if ver == "" {
			ver = PersonalCalibrationVersion
		}
		return *opts.ENMORestG, *opts.ENMORefG, ver
	}
	return DefaultBootstrapENMORestG, DefaultBootstrapENMORefG, BootstrapCalibrationVersion
}

type validAccSecond struct {
	streamID    int64
	sampleCount int
	enmoSum     float64
	durSec      float64
}

type validHRSecond struct {
	validMs     float64
	weightedBPM float64
	durSec      float64
}

func clip01(v float64) float64 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	return v
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

func quantileSorted(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo < 0 {
		lo = 0
	}
	if hi >= len(sorted) {
		hi = len(sorted) - 1
	}
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func isBucketPaused(idx int64, totalDurationInt int64, pauses []PauseInterval) bool {
	startMs := float64(idx * 1000)
	endMs := float64(min((idx+1)*1000, totalDurationInt))
	if endMs <= startMs {
		return false
	}
	for _, p := range pauses {
		overlapStart := math.Max(startMs, p.StartOffsetMs)
		overlapEnd := math.Min(endMs, p.EndOffsetMs)
		if overlapEnd > overlapStart {
			return true
		}
	}
	return false
}

// calculateHybridFromAccumulator computes CalculationVersion 3 Hybrid Activity Intensity:
//   - H_k = clip_{0,1}((HR_5s - HR_rest) / (HR_max - HR_rest))
//   - A_k = (1 / N_k) * sum_{i in W_k} max(||a_i|| - 1, 0)  [5-second rolling ENMO in g]
//   - M_k = clip_{0,1}((A_k - A_rest) / (A_ref - A_rest))
//   - X_k = 0.75 * H_k + 0.25 * M_k,  I_k = 100 * X_k
//   - L   = sum_k X_k^2 * dt_{k,min}  (where dt_{k,min} = bucketDurSec / 60, NEVER 5s!)
//   - I_session = 100 * sqrt(L / T_{valid,min})
//
// Missing or invalid HR/ACC buckets are never substituted with 0 or penalized by a
// confidence multiplier; they are excluded and reflected in data_coverage.
func (tb *timelineAccumulator) calculateHybrid(
	timeline *SensorTimelineData,
	durationMs float64,
	pauses []PauseInterval,
	hrEvents []hrObservation,
	hrSummary HRMetrics,
	opts ParseOptions,
) *HybridMetrics {
	enmoRest, enmoRef, calibVer := ResolveENMOCalibration(opts)
	flags := make([]string, 0)
	flagSet := make(map[string]bool)
	addFlag := func(f string) {
		if !flagSet[f] {
			flagSet[f] = true
			flags = append(flags, f)
		}
	}

	if calibVer == BootstrapCalibrationVersion {
		addFlag("bootstrap_motion_calibration")
	}

	hasMaxHR := opts.EstimatedMaxHR != nil && *opts.EstimatedMaxHR > 0
	hasRestHR := opts.RestingHR != nil && *opts.RestingHR >= 30 && *opts.RestingHR <= 120
	validHRReserve := false
	if !hasMaxHR {
		addFlag("missing_max_hr")
	}
	if !hasRestHR {
		addFlag("missing_resting_hr")
	}
	if hasMaxHR && hasRestHR {
		if *opts.EstimatedMaxHR-*opts.RestingHR >= 15 {
			validHRReserve = true
		} else {
			addFlag("invalid_hr_reserve_range")
		}
	}

	peakExceeded := false
	if hasMaxHR && hrSummary.PeakBPM != nil && *hrSummary.PeakBPM > *opts.EstimatedMaxHR {
		peakExceeded = true
		addFlag("peak_hr_exceeded_max")
	}
	if hrSummary.ExcludedByReason["contact_loss"] > 0 {
		addFlag("contact_loss_present")
	}
	if hrSummary.ExcludedByReason["sudden_drop"] > 0 {
		addFlag("hr_sudden_drop_present")
	}

	totalDurationInt := int64(math.Round(durationMs))
	pauseMs := 0.0
	for _, p := range pauses {
		s := math.Max(0, p.StartOffsetMs)
		e := math.Min(durationMs, p.EndOffsetMs)
		if e > s {
			pauseMs += e - s
		}
	}
	effectiveCaptureSec := math.Max(0, durationMs-pauseMs) / 1000.0

	if totalDurationInt <= 0 || tb == nil || tb.err != nil {
		addFlag("no_valid_hybrid_windows")
		return &HybridMetrics{
			PeakExceededMaxHR:  peakExceeded,
			QualityFlags:       flags,
			CalibrationVersion: calibVer,
			AlgorithmVersion:   HybridAlgorithmVersion,
		}
	}

	// 1. Collect valid ACC 1-second buckets using identical quality gates to TimelineBuilder.
	validACC := make(map[int64]validAccSecond, len(tb.buckets))
	candidateIdxSet := make(map[int64]struct{}, len(tb.buckets))
	hasSaturation := false

	for idx, b := range tb.buckets {
		if b == nil || idx < 0 || idx*1000 >= totalDurationInt {
			continue
		}
		if b.hasError && b.errorReason == "saturation" {
			hasSaturation = true
		}
		candidateIdxSet[idx] = struct{}{}
		if isBucketPaused(idx, totalDurationInt, pauses) {
			continue
		}
		if b.hasError || b.sampleCount == 0 || b.hz <= 0 {
			continue
		}
		startMs := idx * 1000
		endMs := min((idx+1)*1000, totalDurationInt)
		bucketDurMs := float64(endMs - startMs)
		if bucketDurMs <= 0 {
			continue
		}
		nominalDt := 1000.0 / b.hz
		expectedSamples := (bucketDurMs / 1000.0) * b.hz
		minRequiredSamples := math.Max(2, math.Ceil(0.80*expectedSamples))
		maxAllowedGap := 3.0 * nominalDt
		startEdgeGap := b.firstSampleT - float64(startMs)
		endEdgeGap := float64(endMs) - b.lastSampleT

		if float64(b.sampleCount) < minRequiredSamples ||
			b.maxGapMs > maxAllowedGap ||
			startEdgeGap > maxAllowedGap ||
			endEdgeGap < 0 || endEdgeGap > maxAllowedGap {
			continue
		}
		validACC[idx] = validAccSecond{
			streamID:    b.streamID,
			sampleCount: b.sampleCount,
			enmoSum:     b.enmoSum,
			durSec:      bucketDurMs / 1000.0,
		}
	}
	if hasSaturation {
		addFlag("acc_saturation_present")
	}

	// 2. Collect valid HR 1-second buckets from evaluated HR intervals.
	type rawHRBucket struct{ validMs, weightedBPM float64 }
	rawHR := make(map[int64]rawHRBucket)
	_, _, intervals := extractHREvaluationsV2(hrEvents, pauses, durationMs)
	for _, iv := range intervals {
		if iv.Reason != "valid" && iv.Reason != "low" {
			continue
		}
		start, stop := math.Max(0, iv.StartMs), math.Min(float64(totalDurationInt), iv.EndMs)
		if !validTimelineTime(start) || !validTimelineTime(stop) {
			continue
		}
		for cursor := start; cursor < stop; {
			idx := int64(math.Floor(cursor / 1000))
			end := math.Min(float64((idx+1)*1000), stop)
			if len(candidateIdxSet) < MaxTimelineBuckets {
				candidateIdxSet[idx] = struct{}{}
			}
			cur := rawHR[idx]
			cur.validMs += end - cursor
			cur.weightedBPM += float64(iv.BPM) * (end - cursor)
			rawHR[idx] = cur
			cursor = end
		}
	}

	validHR := make(map[int64]validHRSecond, len(rawHR))
	for idx, hr := range rawHR {
		if idx < 0 || idx*1000 >= totalDurationInt || isBucketPaused(idx, totalDurationInt, pauses) {
			continue
		}
		startMs := idx * 1000
		endMs := min((idx+1)*1000, totalDurationInt)
		bucketDurMs := float64(endMs - startMs)
		if bucketDurMs > 0 && hr.validMs >= 0.80*bucketDurMs {
			validHR[idx] = validHRSecond{
				validMs:     hr.validMs,
				weightedBPM: hr.weightedBPM,
				durSec:      bucketDurMs / 1000.0,
			}
		}
	}

	ordered := make([]int64, 0, len(candidateIdxSet))
	for idx := range candidateIdxSet {
		ordered = append(ordered, idx)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	var pointIdxByStartMs map[int64]int
	if timeline != nil && len(timeline.Points) > 0 {
		pointIdxByStartMs = make(map[int64]int, len(timeline.Points))
		for i := range timeline.Points {
			pointIdxByStartMs[timeline.Points[i].StartMs] = i
		}
	}

	var (
		validHybridSec       float64
		validACCSec          float64
		sumHybridSqMin       float64
		sumHeartSqMin        float64
		sumMotionHybridSqMin float64
		motionOnlySqMin      float64
		accEnmoValues        []float64
	)

	var hrRestF, hrReserveDenom float64
	if validHRReserve {
		hrRestF = float64(*opts.RestingHR)
		hrReserveDenom = float64(*opts.EstimatedMaxHR - *opts.RestingHR)
	}
	enmoDenom := enmoRef - enmoRest

	for _, idx := range ordered {
		accSec, hasAcc := validACC[idx]
		var mk float64
		if hasAcc {
			winSamples := accSec.sampleCount
			winEnmoSum := accSec.enmoSum
			minIdx := idx - (hybridRollingSec - 1)
			if minIdx < 0 {
				minIdx = 0
			}
			for j := idx - 1; j >= minIdx; j-- {
				if isBucketPaused(j, totalDurationInt, pauses) {
					break
				}
				if prevAcc, ok := validACC[j]; ok {
					if prevAcc.streamID != accSec.streamID {
						break
					}
					winSamples += prevAcc.sampleCount
					winEnmoSum += prevAcc.enmoSum
				}
			}
			ak := winEnmoSum / float64(winSamples)
			mk = clip01((ak - enmoRest) / enmoDenom)
			validACCSec += accSec.durSec
			accEnmoValues = append(accEnmoValues, ak)
			motionOnlySqMin += mk * mk * (accSec.durSec / 60.0)

			if pIdx, ok := pointIdxByStartMs[idx*1000]; ok {
				akRounded := round4(ak)
				timeline.Points[pIdx].EnmoG = &TimelineValue{
					Value:  &akRounded,
					Status: StatusValid,
				}
			}
		}

		hrSec, hasHR := validHR[idx]
		if !hasAcc || !hasHR {
			continue
		}

		// Both HR and ACC are valid in second idx -> valid hybrid step.
		// Step duration added to cumulative load is strictly 1 second = 1/60 min (or partial final bucket).
		stepSec := accSec.durSec
		dtMin := stepSec / 60.0
		validHybridSec += stepSec

		if validHRReserve {
			winHRMs := hrSec.validMs
			winHRWeighted := hrSec.weightedBPM
			minIdx := idx - (hybridRollingSec - 1)
			if minIdx < 0 {
				minIdx = 0
			}
			for j := idx - 1; j >= minIdx; j-- {
				if isBucketPaused(j, totalDurationInt, pauses) {
					break
				}
				if prevHR, ok := validHR[j]; ok {
					winHRMs += prevHR.validMs
					winHRWeighted += prevHR.weightedBPM
				}
			}
			meanHR5s := winHRWeighted / winHRMs
			hk := clip01((meanHR5s - hrRestF) / hrReserveDenom)
			xk := hybridHeartWeight*hk + hybridMotionWeight*mk
			ik := 100.0 * xk

			sumHybridSqMin += xk * xk * dtMin
			sumHeartSqMin += hk * hk * dtMin
			sumMotionHybridSqMin += mk * mk * dtMin

			if pIdx, ok := pointIdxByStartMs[idx*1000]; ok {
				ikRounded := round2(ik)
				timeline.Points[pIdx].HybridIntensity = &TimelineValue{
					Value:  &ikRounded,
					Status: StatusValid,
				}
			}
		}
	}

	dataCoverage := 0.0
	if effectiveCaptureSec > 0 {
		dataCoverage = round4(math.Min(1.0, validHybridSec/effectiveCaptureSec))
	}

	if validHybridSec == 0 {
		addFlag("no_valid_hybrid_windows")
	} else if dataCoverage < 0.50 {
		addFlag("low_hybrid_coverage")
	}

	out := &HybridMetrics{
		ValidHybridSeconds: round2(validHybridSec),
		ValidACCSeconds:    round2(validACCSec),
		DataCoverage:       dataCoverage,
		PeakExceededMaxHR:  peakExceeded,
		QualityFlags:       flags,
		CalibrationVersion: calibVer,
		AlgorithmVersion:   HybridAlgorithmVersion,
	}

	if validHRReserve && validHybridSec > 0 {
		validHybridMin := validHybridSec / 60.0
		loadAU := round4(sumHybridSqMin)
		iSession := round2(100.0 * math.Sqrt(sumHybridSqMin/validHybridMin))
		hSession := round4(math.Sqrt(sumHeartSqMin/validHybridMin))
		mSession := round4(math.Sqrt(sumMotionHybridSqMin/validHybridMin))
		out.HeartIntensity = &hSession
		out.MotionIntensity = &mSession
		out.HybridIntensity = &iSession
		out.HybridLoadAU = &loadAU
	} else if !validHRReserve && validACCSec > 0 {
		// Preserve objective motion intensity even when resting_hr / max_hr is absent,
		// without imputing a synthetic heart-rate reserve.
		validACCMin := validACCSec / 60.0
		mOnly := round4(math.Sqrt(motionOnlySqMin / validACCMin))
		out.MotionIntensity = &mOnly
	}

	if len(accEnmoValues) >= 60 {
		sorted := make([]float64, len(accEnmoValues))
		copy(sorted, accEnmoValues)
		sort.Float64s(sorted)
		p05 := round4(quantileSorted(sorted, 0.05))
		p95 := round4(quantileSorted(sorted, 0.95))
		out.ENMOP05G = &p05
		out.ENMOP95G = &p95
	}

	return out
}
