package sensor

import (
	"math"
	"regexp"
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

var timelineErrorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// timelineErrorCode returns a timeline error's snake_case code, or "" for
// free-text errors (e.g. JSON decoding).
func timelineErrorCode(err error) string {
	if err != nil && timelineErrorCodePattern.MatchString(err.Error()) {
		return err.Error()
	}
	return ""
}

// calculateHybrid computes CalculationVersion 3 Hybrid Activity Intensity:
//   - H_k = clip_{0,1}((HR_5s - HR_rest) / (HR_max - HR_rest))
//   - A_k = (1 / N_k) * sum_{i in W_k} max(||a_i|| - 1, 0)  [5-second rolling ENMO in g]
//   - M_k = clip_{0,1}((A_k - A_rest) / (A_ref - A_rest))
//   - X_k = 0.75 * H_k + 0.25 * M_k,  I_k = 100 * X_k
//   - L   = sum_k X_k^2 * dt_{k,min}  (where dt_{k,min} = bucketDurSec / 60, NEVER 5s!)
//   - I_session = 100 * sqrt(L / T_{valid,min})
//
// Missing or invalid HR/ACC buckets are never substituted with 0 or penalized by a
// confidence multiplier; they are excluded and reflected in data_coverage.
// motion_intensity covers every valid ACC second, so it does not change with
// resting HR or HR contact; heart/hybrid intensity cover seconds where HR and
// ACC are both valid.
//
// A nil receiver means the ACC replay failed. sensorErr (the replay error, or
// an HR bucketing error with hrBuckets nil) is flagged as
// "sensor_data_unreadable" plus its code, so unreadable data is not reported
// as missing data.
func (tb *timelineAccumulator) calculateHybrid(
	timeline *SensorTimelineData,
	durationMs float64,
	pauses []PauseInterval,
	hrBuckets map[int64]hrSecond,
	hrSummary HRMetrics,
	opts ParseOptions,
	sensorErr error,
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

	unavailable := func() *HybridMetrics {
		return &HybridMetrics{
			PeakExceededMaxHR:  peakExceeded,
			QualityFlags:       flags,
			CalibrationVersion: calibVer,
			AlgorithmVersion:   HybridAlgorithmVersion,
		}
	}
	if tb != nil && tb.err != nil && sensorErr == nil {
		sensorErr = tb.err
	}
	if sensorErr != nil {
		addFlag("sensor_data_unreadable")
		if code := timelineErrorCode(sensorErr); code != "" {
			addFlag(code)
		}
	}
	totalDurationInt := int64(math.Round(durationMs))
	if tb == nil || tb.err != nil {
		return unavailable()
	}
	if totalDurationInt <= 0 {
		addFlag("no_valid_hybrid_windows")
		return unavailable()
	}

	pauseMs := 0.0
	for _, p := range pauses {
		s := math.Max(0, p.StartOffsetMs)
		e := math.Min(durationMs, p.EndOffsetMs)
		if e > s {
			pauseMs += e - s
		}
	}
	effectiveCaptureSec := math.Max(0, durationMs-pauseMs) / 1000.0
	paused := newPauseIndex(pauses)
	bucketEnd := func(idx int64) int64 { return min((idx+1)*1000, totalDurationInt) }
	isPaused := func(idx int64) bool { return paused.overlaps(idx*1000, bucketEnd(idx)) }

	// 1. Valid ACC seconds: the timeline graph's ACC gate, outside pauses.
	validACC := make(map[int64]validAccSecond, len(tb.buckets))
	hasSaturation := false
	for idx, b := range tb.buckets {
		if b == nil || idx < 0 || idx*1000 >= totalDurationInt {
			continue
		}
		if b.hasError && b.errorReason == "saturation" {
			hasSaturation = true
		}
		if isPaused(idx) {
			continue
		}
		startMs, endMs := idx*1000, bucketEnd(idx)
		if status, _ := accBucketQuality(b, startMs, endMs); status != StatusValid {
			continue
		}
		validACC[idx] = validAccSecond{
			streamID:    b.streamID,
			sampleCount: b.sampleCount,
			enmoSum:     b.enmoSum,
			durSec:      float64(endMs-startMs) / 1000.0,
		}
	}
	if hasSaturation {
		addFlag("acc_saturation_present")
	}

	// 2. Valid HR seconds: the timeline graph's 80% coverage rule, outside pauses.
	validHR := make(map[int64]hrSecond, len(hrBuckets))
	for idx, hr := range hrBuckets {
		if idx < 0 || idx*1000 >= totalDurationInt || isPaused(idx) {
			continue
		}
		if hr.valid(float64(bucketEnd(idx) - idx*1000)) {
			validHR[idx] = hr
		}
	}

	// Every contribution needs a valid ACC second, so only those are visited.
	ordered := make([]int64, 0, len(validACC))
	for idx := range validACC {
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
		validHybridSec  float64
		validACCSec     float64
		sumHybridSqMin  float64
		sumHeartSqMin   float64
		motionOnlySqMin float64
		accEnmoValues   []float64
	)

	var hrRestF, hrReserveDenom float64
	if validHRReserve {
		hrRestF = float64(*opts.RestingHR)
		hrReserveDenom = float64(*opts.EstimatedMaxHR - *opts.RestingHR)
	}
	enmoDenom := enmoRef - enmoRest

	for _, idx := range ordered {
		accSec := validACC[idx]
		winSamples := accSec.sampleCount
		winEnmoSum := accSec.enmoSum
		minIdx := max(idx-(hybridRollingSec-1), 0)
		for j := idx - 1; j >= minIdx; j-- {
			if isPaused(j) {
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
		mk := clip01((ak - enmoRest) / enmoDenom)
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

		hrSec, hasHR := validHR[idx]
		if !hasHR {
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
			for j := idx - 1; j >= minIdx; j-- {
				if isPaused(j) {
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

	if validACCSec > 0 {
		mOnly := round4(math.Sqrt(motionOnlySqMin / (validACCSec / 60.0)))
		out.MotionIntensity = &mOnly
	}
	if validHRReserve && validHybridSec > 0 {
		validHybridMin := validHybridSec / 60.0
		loadAU := round4(sumHybridSqMin)
		iSession := round2(100.0 * math.Sqrt(sumHybridSqMin/validHybridMin))
		hSession := round4(math.Sqrt(sumHeartSqMin / validHybridMin))
		out.HeartIntensity = &hSession
		out.HybridIntensity = &iSession
		out.HybridLoadAU = &loadAU
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
