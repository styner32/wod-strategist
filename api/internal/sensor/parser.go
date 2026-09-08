package sensor

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	MaxFileSize = 20 * 1024 * 1024 // 20 MiB
	MaxLineSize = 1024 * 1024      // 1 MiB
)

type CountingReader struct {
	reader io.Reader
	count  int64
}

func (cr *CountingReader) Read(p []byte) (int, error) {
	n, err := cr.reader.Read(p)
	cr.count += int64(n)
	return n, err
}

type accWindow struct {
	windowIndex int64
	streamID    int64
	hz          float64
	sampleCount int
	lastSampleT float64
	maxGapMs    float64
	sumDev      float64
	sumSqDev    float64
	hasError    bool
	isPaused    bool
}

// ParseAndProcess reads the sensor NDJSON stream in a single pass, computing
// metrics, validating quality, and calculating the HR bonus without accumulating
// the entire file or large arrays in memory.
func ParseAndProcess(r io.Reader, opts ParseOptions) (*SensorSummaryResult, error) {
	hasher := sha256.New()
	limitedReader := io.LimitReader(r, MaxFileSize+1)
	countingReader := &CountingReader{reader: limitedReader}
	teeReader := io.TeeReader(countingReader, hasher)

	scanner := bufio.NewScanner(teeReader)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, MaxLineSize)

	var (
		lineNum       int
		seenMeta      bool
		seenEnd       bool
		metaEvent     MetaEvent
		endEvent      EndEvent
		parseErrors   []string
		parseWarnings []string

		// Pause management
		pauseIntervals []PauseInterval

		// Stream info: stream_id -> acc_hz
		streamHzMap = make(map[int64]float64)

		// HR aggregation
		prevHRTimeMs      float64
		prevBPM           int
		hasPrevHR         bool
		minBPM            int = 9999
		peakBPM           int = 0
		validHRDurationMs float64
		hrWeightedSum     float64
		zone1DurationMs   float64
		zone2DurationMs   float64
		zone3DurationMs   float64
		zone4DurationMs   float64
		zone5DurationMs   float64

		// ACC aggregation
		activeWindow       *accWindow
		lowMovementSec     float64
		otherMovementSec   float64
		unknownMovementSec float64
		totalWindows       int
	)

	mergePauseIntervals := func(intervals []PauseInterval) []PauseInterval {
		if len(intervals) == 0 {
			return nil
		}
		sorted := make([]PauseInterval, len(intervals))
		copy(sorted, intervals)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].StartOffsetMs < sorted[j].StartOffsetMs
		})
		var merged []PauseInterval
		cur := sorted[0]
		for i := 1; i < len(sorted); i++ {
			next := sorted[i]
			if next.StartOffsetMs <= cur.EndOffsetMs {
				if next.EndOffsetMs > cur.EndOffsetMs {
					cur.EndOffsetMs = next.EndOffsetMs
				}
			} else {
				merged = append(merged, cur)
				cur = next
			}
		}
		merged = append(merged, cur)
		return merged
	}

	// Helper to calculate unpaused duration in [start, end)
	calcUnpausedDurationMs := func(start, end float64, intervals []PauseInterval) float64 {
		if end <= start {
			return 0
		}
		duration := end - start
		for _, p := range intervals {
			overlapStart := math.Max(start, p.StartOffsetMs)
			overlapEnd := math.Min(end, p.EndOffsetMs)
			if overlapEnd > overlapStart {
				duration -= (overlapEnd - overlapStart)
			}
		}
		if duration < 0 {
			return 0
		}
		return duration
	}

	flushWindow := func(w *accWindow) {
		if w == nil {
			return
		}
		totalWindows++
		if w.hasError || w.isPaused || w.sampleCount == 0 || w.hz <= 0 {
			unknownMovementSec += 1.0
			return
		}
		expectedSamples := w.hz
		minReq := int(0.80 * expectedSamples)
		maxAllowedGap := 3000.0 / w.hz
		if w.sampleCount < minReq || w.maxGapMs > maxAllowedGap {
			unknownMovementSec += 1.0
			return
		}

		n := float64(w.sampleCount)
		mean := w.sumDev / n
		variance := (w.sumSqDev / n) - (mean * mean)
		if variance < 0 {
			variance = 0
		}

		if variance < 0.05 {
			lowMovementSec += 1.0
		} else {
			otherMovementSec += 1.0
		}
	}

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if seenEnd {
			parseErrors = append(parseErrors, fmt.Sprintf("line %d: non-empty line after end event", lineNum))
			break
		}

		var header EventHeader
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			parseErrors = append(parseErrors, fmt.Sprintf("line %d: invalid JSON: %v", lineNum, err))
			continue
		}

		if lineNum == 1 {
			if header.Kind != "meta" {
				parseErrors = append(parseErrors, "first event must be meta")
				break
			}
			if err := json.Unmarshal([]byte(line), &metaEvent); err != nil {
				parseErrors = append(parseErrors, fmt.Sprintf("line 1: failed to parse meta: %v", err))
				break
			}
			seenMeta = true

			// Validate meta
			if metaEvent.SchemaVersion != "2.0.0" {
				parseErrors = append(parseErrors, fmt.Sprintf("unsupported schema version: %s (expected 2.0.0)", metaEvent.SchemaVersion))
			}
			if opts.ExpectedProfileID > 0 && metaEvent.ProfileID != opts.ExpectedProfileID {
				parseErrors = append(parseErrors, fmt.Sprintf("meta profile_id %d does not match expected %d", metaEvent.ProfileID, opts.ExpectedProfileID))
			}
			if opts.ExpectedSessionID != "" && metaEvent.WorkoutSessionID != opts.ExpectedSessionID {
				parseErrors = append(parseErrors, fmt.Sprintf("meta session_id %s does not match expected %s", metaEvent.WorkoutSessionID, opts.ExpectedSessionID))
			}
			continue
		}

		if !seenMeta {
			parseErrors = append(parseErrors, "missing meta event at beginning")
			break
		}

		switch header.Kind {
		case "meta":
			parseErrors = append(parseErrors, fmt.Sprintf("line %d: duplicate meta event", lineNum))

		case "end":
			seenEnd = true
			if err := json.Unmarshal([]byte(line), &endEvent); err != nil {
				parseErrors = append(parseErrors, fmt.Sprintf("line %d: failed to parse end: %v", lineNum, err))
			}
			// End pause intervals
			if len(endEvent.PauseIntervals) > 0 {
				pauseIntervals = append(pauseIntervals, endEvent.PauseIntervals...)
			}
			pauseIntervals = mergePauseIntervals(pauseIntervals)

		case "pause":
			// We track pause events if needed; merged with endEvent.PauseIntervals
			var pe struct {
				Time int64 `json:"t"`
			}
			_ = json.Unmarshal([]byte(line), &pe)
			// Track as half interval if end event is missing, otherwise endEvent has authoritative list

		case "resume":
			// Handled in pause intervals

		case "stream_start":
			var sse StreamStartEvent
			if err := json.Unmarshal([]byte(line), &sse); err != nil {
				parseWarnings = append(parseWarnings, fmt.Sprintf("line %d: failed to parse stream_start: %v", lineNum, err))
			} else {
				if sse.Sampling.ACCHz > 0 {
					streamHzMap[sse.StreamID] = sse.Sampling.ACCHz
				}
				// If active window was for this stream, reset anchor
				if activeWindow != nil && activeWindow.streamID == sse.StreamID {
					activeWindow.hasError = true
				}
			}

		case "hr":
			var hre HREvent
			if err := json.Unmarshal([]byte(line), &hre); err != nil {
				parseWarnings = append(parseWarnings, fmt.Sprintf("line %d: invalid hr event: %v", lineNum, err))
				continue
			}

			// Validate BPM bounds: 30 to 240
			if hre.BPM < 30 || hre.BPM > 240 {
				// Invalid sample, resets continuity
				hasPrevHR = false
				continue
			}

			if hre.BPM < minBPM {
				minBPM = hre.BPM
			}
			if hre.BPM > peakBPM {
				peakBPM = hre.BPM
			}

			if hasPrevHR {
				dtMs := hre.Time - prevHRTimeMs
				if dtMs <= 0 {
					parseErrors = append(parseErrors, fmt.Sprintf("line %d: HR timestamp reversed or duplicate (%f <= %f)", lineNum, hre.Time, prevHRTimeMs))
				} else if dtMs <= 5000 {
					// Continuous valid interval [prevHRTimeMs, hre.Time)
					effectiveDtMs := calcUnpausedDurationMs(prevHRTimeMs, hre.Time, pauseIntervals)
					if effectiveDtMs > 0 {
						validHRDurationMs += effectiveDtMs
						hrWeightedSum += float64(prevBPM) * (effectiveDtMs / 1000.0)

						// HR Zones (based on frozen MaxHR)
						if opts.EstimatedMaxHR != nil && *opts.EstimatedMaxHR > 0 {
							maxHR := float64(*opts.EstimatedMaxHR)
							bpmRatio := float64(prevBPM) / maxHR
							switch {
							case bpmRatio < 0.60:
								zone1DurationMs += effectiveDtMs
							case bpmRatio < 0.70:
								zone2DurationMs += effectiveDtMs
							case bpmRatio < 0.80:
								zone3DurationMs += effectiveDtMs
							case bpmRatio < 0.90:
								zone4DurationMs += effectiveDtMs
							default:
								zone5DurationMs += effectiveDtMs
							}
						}
					}
				}
				// if dtMs > 5000: interval is unknown
			}
			prevHRTimeMs = hre.Time
			prevBPM = hre.BPM
			hasPrevHR = true

		case "acc":
			var acce AccEvent
			if err := json.Unmarshal([]byte(line), &acce); err != nil {
				parseWarnings = append(parseWarnings, fmt.Sprintf("line %d: invalid acc event: %v", lineNum, err))
				continue
			}
			hz, hasHz := streamHzMap[acce.StreamID]
			if !hasHz || hz <= 0 {
				hz = 50.0 // fallback
			}
			dt := acce.Dt
			if dt <= 0 {
				dt = 1000.0 / hz
			}

			for i, sample := range acce.V {
				if len(sample) < 3 {
					continue
				}
				x, y, z := sample[0], sample[1], sample[2]
				if math.IsNaN(x) || math.IsNaN(y) || math.IsNaN(z) || math.IsInf(x, 0) || math.IsInf(y, 0) || math.IsInf(z, 0) {
					if activeWindow != nil {
						activeWindow.hasError = true
					}
					continue
				}
				sampleTMs := acce.Time + int64(math.Round(float64(i)*dt))
				windowIdx := sampleTMs / 1000

				if activeWindow == nil {
					activeWindow = &accWindow{
						windowIndex: windowIdx,
						streamID:    acce.StreamID,
						hz:          hz,
						lastSampleT: sampleTMs,
					}
				} else if windowIdx != activeWindow.windowIndex {
					flushWindow(activeWindow)
					activeWindow = &accWindow{
						windowIndex: windowIdx,
						streamID:    acce.StreamID,
						hz:          hz,
						lastSampleT: sampleTMs,
					}
				}

				gap := float64(sampleTMs - activeWindow.lastSampleT)
				if gap > activeWindow.maxGapMs {
					activeWindow.maxGapMs = gap
				}
				if sampleTMs < activeWindow.lastSampleT {
					activeWindow.hasError = true
				}
				activeWindow.lastSampleT = sampleTMs

				norm := math.Sqrt(x*x + y*y + z*z)
				dev := math.Abs(norm - 1.0)
				activeWindow.sampleCount++
				activeWindow.sumDev += dev
				activeWindow.sumSqDev += dev * dev
			}

		default:
			// Known optional or unknown events: record warning if necessary, continue
		}
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			parseErrors = append(parseErrors, "line length exceeds 1 MiB limit")
		} else {
			parseErrors = append(parseErrors, fmt.Sprintf("scanner error: %v", err))
		}
	}

	// Check 20 MiB limit
	if countingReader.count > MaxFileSize {
		parseErrors = append(parseErrors, fmt.Sprintf("file size exceeds %d bytes limit", MaxFileSize))
	}

	// Verify SHA-256
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if opts.ExpectedSHA256 != "" && !strings.EqualFold(actualHash, opts.ExpectedSHA256) {
		parseErrors = append(parseErrors, fmt.Sprintf("SHA-256 mismatch: expected %s, got %s", opts.ExpectedSHA256, actualHash))
	}

	// Check file size match if provided
	if opts.ExpectedSizeBytes > 0 && countingReader.count != opts.ExpectedSizeBytes && countingReader.count <= MaxFileSize {
		parseWarnings = append(parseWarnings, fmt.Sprintf("file size %d does not match expected %d", countingReader.count, opts.ExpectedSizeBytes))
	}

	// Finalize ACC windows
	// Note: The very last partial window (< 1s) is treated as unknown per §8.3
	if activeWindow != nil {
		unknownMovementSec += 1.0
		totalWindows++
	}

	// Calculate capture and pause duration
	var captureDurationMs int64
	var pauseDurationMs int64
	if seenEnd {
		captureDurationMs = endEvent.Time
		for _, p := range pauseIntervals {
			if p.EndOffsetMs > p.StartOffsetMs {
				pauseDurationMs += (p.EndOffsetMs - p.StartOffsetMs)
			}
		}
	}

	effectiveCaptureMs := captureDurationMs - pauseDurationMs
	if effectiveCaptureMs < 0 {
		effectiveCaptureMs = 0
	}

	var coverage float64
	var validHRSec float64 = float64(validHRDurationMs) / 1000.0
	var unknownHRSec float64
	if effectiveCaptureMs > 0 {
		effectiveCaptureSec := float64(effectiveCaptureMs) / 1000.0
		coverage = validHRSec / effectiveCaptureSec
		if coverage > 1.0 {
			coverage = 1.0
		}
		unknownHRSec = math.Max(0, effectiveCaptureSec-validHRSec)
	}

	isComplete := seenMeta && seenEnd && len(parseErrors) == 0
	validHR := isComplete && (coverage >= 0.50)

	var weightedMeanBPM *float64
	if validHRSec > 0 {
		mean := hrWeightedSum / validHRSec
		weightedMeanBPM = &mean
	}

	var minBPMPtr *int
	var peakBPMPtr *int
	if minBPM <= 240 && minBPM >= 30 {
		minBPMPtr = &minBPM
	}
	if peakBPM >= 30 && peakBPM <= 240 {
		peakBPMPtr = &peakBPM
	}

	// Construct HR Zones
	var zones *HeartRateZones
	hasHRZones := opts.EstimatedMaxHR != nil && *opts.EstimatedMaxHR > 0
	if hasHRZones && validHRSec > 0 {
		z1Sec := float64(zone1DurationMs) / 1000.0
		z2Sec := float64(zone2DurationMs) / 1000.0
		z3Sec := float64(zone3DurationMs) / 1000.0
		z4Sec := float64(zone4DurationMs) / 1000.0
		z5Sec := float64(zone5DurationMs) / 1000.0
		zones = &HeartRateZones{
			Zone1Seconds: z1Sec,
			Zone2Seconds: z2Sec,
			Zone3Seconds: z3Sec,
			Zone4Seconds: z4Sec,
			Zone5Seconds: z5Sec,
			Zone1Ratio:   z1Sec / validHRSec,
			Zone2Ratio:   z2Sec / validHRSec,
			Zone3Ratio:   z3Sec / validHRSec,
			Zone4Ratio:   z4Sec / validHRSec,
			Zone5Ratio:   z5Sec / validHRSec,
		}
	}

	// HR Bonus (single formula):
	// valid_hr ? clamp((weighted_mean_bpm - 140) * 0.5, 0, 20) : 0
	var hrBonus float64 = 0.0
	if validHR && weightedMeanBPM != nil {
		bonus := (*weightedMeanBPM - 140.0) * 0.5
		if bonus < 0.0 {
			bonus = 0.0
		} else if bonus > 20.0 {
			bonus = 20.0
		}
		hrBonus = bonus
	}

	status := "ok"
	if len(parseErrors) > 0 {
		status = "corrupt"
	} else if !seenEnd {
		status = "incomplete"
	}

	var maxHRSrc *string
	if hasHRZones {
		src := "estimated_220_minus_age"
		maxHRSrc = &src
	}

	result := &SensorSummaryResult{
		CalculationVersion: 1,
		CalculationInputs: CalculationInputs{
			CalculationVersion: 1,
			Age:                opts.Age,
			MaxHR:              opts.EstimatedMaxHR,
			MaxHRSource:        maxHRSrc,
			HasHRZones:         hasHRZones,
		},
		Quality: QualityReport{
			Status:     status,
			IsComplete: isComplete,
			HRCoverage: coverage,
			ValidHR:    validHR,
			Errors:     parseErrors,
			Warnings:   parseWarnings,
		},
		Metrics: SensorMetrics{
			DurationSeconds: float64(effectiveCaptureMs) / 1000.0,
			PauseSeconds:    float64(pauseDurationMs) / 1000.0,
			HR: HRMetrics{
				ValidHR:         validHR,
				WeightedMeanBPM: weightedMeanBPM,
				MinBPM:          minBPMPtr,
				PeakBPM:         peakBPMPtr,
				ValidSeconds:    validHRSec,
				UnknownSeconds:  unknownHRSec,
				Coverage:        coverage,
				HasHRZones:      hasHRZones,
				EstimatedMaxHR:  opts.EstimatedMaxHR,
				Zones:           zones,
			},
			ACC: ACCMetrics{
				LowMovementSeconds:     lowMovementSec,
				OtherMovementSeconds:   otherMovementSec,
				UnknownMovementSeconds: unknownMovementSec,
				TotalWindows:           totalWindows,
			},
		},
		HRBonus:      hrBonus,
		CalculatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	return result, nil
}
