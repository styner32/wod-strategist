package sensor_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/sensor"
)

func buildHybridNDJSON(
	profileID uint,
	sessionID string,
	totalSec int,
	bpmAtSec func(sec int) (int, *bool),
	accSampleAtSec func(sec int, sampleIdx int) []float64,
) *bytes.Buffer {
	var buf bytes.Buffer
	writeLine := func(v any) {
		b, err := json.Marshal(v)
		Expect(err).NotTo(HaveOccurred())
		buf.Write(b)
		buf.WriteByte('\n')
	}

	writeLine(map[string]any{
		"k":                  "meta",
		"t":                  0,
		"schema_version":     "2.0.0",
		"workout_session_id": sessionID,
		"profile_id":         profileID,
		"clock_source":       "monotonic",
		"base_epoch_ms":      1700000000000,
	})
	writeLine(map[string]any{
		"k":         "stream_start",
		"t":         0,
		"stream_id": 1,
		"sampling": map[string]any{
			"acc_hz":              25,
			"acc_range_g":         8,
			"acc_resolution_bits": 16,
		},
	})

	// Include sample at t = totalSec*1000 so the final 1-second interval [totalSec-1, totalSec) is closed.
	for sec := 0; sec <= totalSec; sec++ {
		bpm, contact := bpmAtSec(sec)
		ev := map[string]any{
			"k":   "hr",
			"t":   sec * 1000,
			"bpm": bpm,
		}
		if contact != nil {
			ev["contact"] = *contact
		}
		writeLine(ev)

		if sec < totalSec {
			samples := make([][]float64, 25)
			for i := 0; i < 25; i++ {
				samples[i] = accSampleAtSec(sec, i)
			}
			writeLine(map[string]any{
				"k":         "acc",
				"t":         sec * 1000,
				"stream_id": 1,
				"dt":        40,
				"v":         samples,
			})
		}
	}

	writeLine(map[string]any{
		"k":               "end",
		"t":               totalSec * 1000,
		"pause_intervals": []any{},
		"summary": map[string]any{
			"hr_samples":  totalSec + 1,
			"acc_samples": totalSec * 25,
		},
	})
	return &buf
}

var _ = Describe("CalculationVersion 3 Hybrid Activity Intensity (HRR + ENMO)", func() {
	const (
		testProfileID uint = 7
		testSessionID      = "WOD-20261025-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
	)

	It("computes exact HRR, ENMO, and 75:25 hybrid intensity matching reference values", func() {
		// HR: rest=60, max=180, current=150 -> H_k = 0.75
		// ACC: ||a|| = 1.25g -> ENMO A_k = 0.25g, rest=0.01g, ref=0.31g -> M_k = 0.80
		// Hybrid: X_k = 0.75*0.75 + 0.25*0.80 = 0.7625 -> I_k = 76.25
		contactTrue := true
		ndjson := buildHybridNDJSON(
			testProfileID,
			testSessionID,
			65,
			func(_ int) (int, *bool) { return 150, &contactTrue },
			func(_ int, _ int) []float64 { return []float64{0, 0, 1.25} },
		)

		age := 40
		maxHR := 180
		restHR := 60
		enmoRest := 0.01
		enmoRef := 0.31
		res, err := sensor.ParseAndProcessWithTimeline(ndjson, sensor.ParseOptions{
			CalculationVersion: 3,
			ExpectedProfileID:  testProfileID,
			ExpectedSessionID:  testSessionID,
			Age:                &age,
			EstimatedMaxHR:     &maxHR,
			RestingHR:          &restHR,
			ENMORestG:          &enmoRest,
			ENMORefG:           &enmoRef,
			CalibrationVersion: sensor.PersonalCalibrationVersion,
		}, sensor.TimelineSource{
			SensorVersion:    "1",
			RequestID:        "req-v3",
			SourceGeneration: "100",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Summary.Metrics.Hybrid).NotTo(BeNil())

		h := res.Summary.Metrics.Hybrid
		Expect(h.AlgorithmVersion).To(Equal(sensor.HybridAlgorithmVersion))
		Expect(h.CalibrationVersion).To(Equal(sensor.PersonalCalibrationVersion))
		Expect(h.DataCoverage).To(Equal(1.0))
		Expect(h.HeartIntensity).NotTo(BeNil())
		Expect(*h.HeartIntensity).To(BeNumerically("~", 0.75, 0.0001))
		Expect(h.MotionIntensity).NotTo(BeNil())
		Expect(*h.MotionIntensity).To(BeNumerically("~", 0.80, 0.0001))
		Expect(h.HybridIntensity).NotTo(BeNil())
		Expect(*h.HybridIntensity).To(BeNumerically("~", 76.25, 0.01))
		Expect(h.ENMOP05G).NotTo(BeNil())
		Expect(*h.ENMOP05G).To(BeNumerically("~", 0.25, 0.0001))
		Expect(h.ENMOP95G).NotTo(BeNil())
		Expect(*h.ENMOP95G).To(BeNumerically("~", 0.25, 0.0001))
		Expect(h.QualityFlags).To(BeEmpty())

		Expect(res.Timeline).NotTo(BeNil())
		Expect(res.Timeline.Points).To(HaveLen(65))
		Expect(res.Timeline.Points[10].EnmoG).NotTo(BeNil())
		Expect(*res.Timeline.Points[10].EnmoG.Value).To(BeNumerically("~", 0.25, 0.0001))
		Expect(res.Timeline.Points[10].HybridIntensity).NotTo(BeNil())
		Expect(*res.Timeline.Points[10].HybridIntensity.Value).To(BeNumerically("~", 76.25, 0.01))
	})

	DescribeTable("integrates cumulative load L with 1/60 min steps without 5x window overcounting",
		func(targetX float64, durationMin int, expectedLoadAU float64) {
			// With restHR=60, maxHR=160 -> BPM = 60 + 100*targetX
			// With enmoRest=0.01, enmoRef=0.31 -> A_k = 0.01 + 0.30*targetX -> ||a|| = 1 + A_k
			bpm := int(60 + 100*targetX)
			normG := 1.0 + 0.01 + 0.30*targetX
			contactTrue := true
			totalSec := durationMin * 60

			ndjson := buildHybridNDJSON(
				testProfileID,
				testSessionID,
				totalSec,
				func(_ int) (int, *bool) { return bpm, &contactTrue },
				func(_ int, _ int) []float64 { return []float64{0, 0, normG} },
			)

			maxHR := 160
			restHR := 60
			enmoRest := 0.01
			enmoRef := 0.31
			summary, err := sensor.ParseAndProcess(ndjson, sensor.ParseOptions{
				CalculationVersion: 3,
				ExpectedProfileID:  testProfileID,
				ExpectedSessionID:  testSessionID,
				EstimatedMaxHR:     &maxHR,
				RestingHR:          &restHR,
				ENMORestG:          &enmoRest,
				ENMORefG:           &enmoRef,
				CalibrationVersion: sensor.PersonalCalibrationVersion,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(summary.Metrics.Hybrid).NotTo(BeNil())
			Expect(summary.Metrics.Hybrid.HybridLoadAU).NotTo(BeNil())
			Expect(*summary.Metrics.Hybrid.HybridLoadAU).To(BeNumerically("~", expectedLoadAU, 0.01),
				fmt.Sprintf("expected load %.2f AU for X=%.2f over %d min", expectedLoadAU, targetX, durationMin))
			Expect(*summary.Metrics.Hybrid.HybridIntensity).To(BeNumerically("~", targetX*100, 0.05))
		},
		Entry("intensity 80 for 20 minutes -> 12.8 AU", 0.80, 20, 12.8),
		Entry("intensity 50 for 20 minutes -> 5.0 AU", 0.50, 20, 5.0),
	)

	It("excludes contact-loss and ACC-saturation windows without zero-filling or confidence discounting", func() {
		contactTrue := true
		contactFalse := false
		// 100s session:
		// [0..59]: valid HR=150, valid ACC=1.25g
		// [60..79]: HR contact loss
		// [80..99]: ACC saturation (>= 8g)
		ndjson := buildHybridNDJSON(
			testProfileID,
			testSessionID,
			100,
			func(sec int) (int, *bool) {
				if sec >= 60 && sec < 80 {
					return 150, &contactFalse
				}
				return 150, &contactTrue
			},
			func(sec int, _ int) []float64 {
				if sec >= 80 {
					return []float64{0, 0, 8.5}
				}
				return []float64{0, 0, 1.25}
			},
		)

		maxHR := 180
		restHR := 60
		enmoRest := 0.01
		enmoRef := 0.31
		summary, err := sensor.ParseAndProcess(ndjson, sensor.ParseOptions{
			CalculationVersion: 3,
			ExpectedProfileID:  testProfileID,
			ExpectedSessionID:  testSessionID,
			EstimatedMaxHR:     &maxHR,
			RestingHR:          &restHR,
			ENMORestG:          &enmoRest,
			ENMORefG:           &enmoRef,
			CalibrationVersion: sensor.PersonalCalibrationVersion,
		})
		Expect(err).NotTo(HaveOccurred())
		h := summary.Metrics.Hybrid
		Expect(h).NotTo(BeNil())
		// Observed valid windows maintain true intensity ~76.25 instead of being dragged toward 0
		Expect(h.HybridIntensity).NotTo(BeNil())
		Expect(*h.HybridIntensity).To(BeNumerically("~", 76.25, 0.05))
		Expect(h.DataCoverage).To(BeNumerically("<", 0.65))
		Expect(h.QualityFlags).To(ContainElements("contact_loss_present", "acc_saturation_present"))
	})

	It("applies 60 bpm default resting_hr with estimated_resting_hr flag when resting_hr is omitted", func() {
		contactTrue := true
		ndjson := buildHybridNDJSON(
			testProfileID,
			testSessionID,
			70,
			func(_ int) (int, *bool) { return 150, &contactTrue },
			func(_ int, _ int) []float64 { return []float64{0, 0, 1.16} }, // A_k = 0.16g -> bootstrap (0.16-0.01)/(0.30-0.01)
		)

		maxHR := 180 // rest=60 default, max=180, current=150 -> H_k = 0.75
		summary, err := sensor.ParseAndProcess(ndjson, sensor.ParseOptions{
			CalculationVersion: 3,
			ExpectedProfileID:  testProfileID,
			ExpectedSessionID:  testSessionID,
			EstimatedMaxHR:     &maxHR,
			RestingHR:          nil,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.CalculationInputs.RestingHR).To(HaveValue(Equal(60)))
		Expect(summary.CalculationInputs.RestingHRSource).To(Equal(sensor.RestingHRSourceDefault))

		h := summary.Metrics.Hybrid
		Expect(h).NotTo(BeNil())
		expectedMotion := (0.16 - 0.01) / (0.30 - 0.01)
		expectedHybrid := 100.0 * (0.75*0.75 + 0.25*expectedMotion)
		Expect(h.HeartIntensity).To(HaveValue(BeNumerically("~", 0.75, 0.0001)))
		Expect(h.MotionIntensity).To(HaveValue(BeNumerically("~", expectedMotion, 0.005)))
		Expect(h.HybridIntensity).To(HaveValue(BeNumerically("~", expectedHybrid, 0.05)))
		Expect(h.HybridLoadAU).NotTo(BeNil())
		Expect(h.ENMOP95G).To(HaveValue(BeNumerically("~", 0.16, 0.001)))
		Expect(h.CalibrationVersion).To(Equal(sensor.BootstrapCalibrationVersion))
		Expect(h.QualityFlags).To(ContainElements("estimated_resting_hr", "bootstrap_motion_calibration"))
		Expect(h.QualityFlags).NotTo(ContainElement("missing_resting_hr"))
	})

	It("withholds HRR/hybrid score when max_hr is missing or explicit resting_hr is out of range while still computing motion_intensity", func() {
		contactTrue := true
		build := func() *bytes.Buffer {
			return buildHybridNDJSON(
				testProfileID,
				testSessionID,
				70,
				func(_ int) (int, *bool) { return 150, &contactTrue },
				func(_ int, _ int) []float64 { return []float64{0, 0, 1.16} },
			)
		}

		noMaxSummary, err := sensor.ParseAndProcess(build(), sensor.ParseOptions{
			CalculationVersion: 3,
			ExpectedProfileID:  testProfileID,
			ExpectedSessionID:  testSessionID,
			EstimatedMaxHR:     nil,
			RestingHR:          nil,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(noMaxSummary.Metrics.Hybrid.HeartIntensity).To(BeNil())
		Expect(noMaxSummary.Metrics.Hybrid.HybridIntensity).To(BeNil())
		Expect(noMaxSummary.Metrics.Hybrid.HybridLoadAU).To(BeNil())
		Expect(noMaxSummary.Metrics.Hybrid.MotionIntensity).To(HaveValue(BeNumerically("~", (0.16-0.01)/(0.30-0.01), 0.005)))
		Expect(noMaxSummary.Metrics.Hybrid.QualityFlags).To(ContainElements("missing_max_hr", "estimated_resting_hr", "bootstrap_motion_calibration"))

		maxHR := 185
		invalidRest := 20
		badRestSummary, err := sensor.ParseAndProcess(build(), sensor.ParseOptions{
			CalculationVersion: 3,
			ExpectedProfileID:  testProfileID,
			ExpectedSessionID:  testSessionID,
			EstimatedMaxHR:     &maxHR,
			RestingHR:          &invalidRest,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(badRestSummary.CalculationInputs.RestingHR).To(BeNil())
		Expect(badRestSummary.Metrics.Hybrid.HeartIntensity).To(BeNil())
		Expect(badRestSummary.Metrics.Hybrid.HybridIntensity).To(BeNil())
		Expect(badRestSummary.Metrics.Hybrid.MotionIntensity).To(HaveValue(BeNumerically("~", (0.16-0.01)/(0.30-0.01), 0.005)))
		Expect(badRestSummary.Metrics.Hybrid.QualityFlags).To(ContainElements("missing_resting_hr", "bootstrap_motion_calibration"))
	})

	It("keeps motion_intensity independent of HR reserve availability and resting HR value when HR contact drops in the hardest seconds", func() {
		contactTrue := true
		contactFalse := false
		build := func() *bytes.Buffer {
			return buildHybridNDJSON(
				testProfileID,
				testSessionID,
				120,
				func(sec int) (int, *bool) {
					if sec < 40 {
						return 170, &contactFalse
					}
					return 120, &contactTrue
				},
				func(sec int, _ int) []float64 {
					if sec < 40 {
						return []float64{0, 0, 1.31}
					}
					return []float64{0, 0, 1.06}
				},
			)
		}

		maxHR := 180
		restHR50 := 50
		enmoRest := 0.01
		enmoRef := 0.31
		opts := sensor.ParseOptions{
			CalculationVersion: 3,
			ExpectedProfileID:  testProfileID,
			ExpectedSessionID:  testSessionID,
			ENMORestG:          &enmoRest,
			ENMORefG:           &enmoRef,
			CalibrationVersion: sensor.PersonalCalibrationVersion,
		}
		withoutMax, err := sensor.ParseAndProcess(build(), opts)
		Expect(err).NotTo(HaveOccurred())

		opts.EstimatedMaxHR = &maxHR
		defaultRest, err := sensor.ParseAndProcess(build(), opts)
		Expect(err).NotTo(HaveOccurred())

		opts.RestingHR = &restHR50
		profileRest, err := sensor.ParseAndProcess(build(), opts)
		Expect(err).NotTo(HaveOccurred())

		Expect(withoutMax.Metrics.Hybrid.HybridIntensity).To(BeNil())
		Expect(defaultRest.Metrics.Hybrid.HybridIntensity).NotTo(BeNil())
		Expect(defaultRest.Metrics.Hybrid.QualityFlags).To(ContainElement("estimated_resting_hr"))
		Expect(profileRest.Metrics.Hybrid.HybridIntensity).NotTo(BeNil())
		Expect(profileRest.Metrics.Hybrid.QualityFlags).NotTo(ContainElement("estimated_resting_hr"))
		Expect(profileRest.Metrics.Hybrid.MotionIntensity).NotTo(BeNil())
		Expect(*defaultRest.Metrics.Hybrid.MotionIntensity).To(Equal(*withoutMax.Metrics.Hybrid.MotionIntensity))
		Expect(*profileRest.Metrics.Hybrid.MotionIntensity).To(Equal(*withoutMax.Metrics.Hybrid.MotionIntensity))
		// RMS over all 120 valid ACC seconds, including the 40 hard seconds without HR.
		Expect(*profileRest.Metrics.Hybrid.MotionIntensity).To(BeNumerically(">", 0.5))
	})

	It("reports unreadable sensor data instead of missing windows when the ACC replay fails", func() {
		contactTrue := true
		ndjson := buildHybridNDJSON(
			testProfileID,
			testSessionID,
			65,
			func(_ int) (int, *bool) { return 150, &contactTrue },
			func(_ int, _ int) []float64 { return []float64{0, 0, 1.25} },
		)
		lines := strings.Split(strings.TrimRight(ndjson.String(), "\n"), "\n")
		// A timestamp beyond the graph's exact JavaScript range fails the replay.
		corrupt := `{"k":"acc","t":100000000000000000,"stream_id":1,"dt":40,"v":[[0,0,1.25]]}`
		lines = append(lines[:len(lines)-1], corrupt, lines[len(lines)-1])

		maxHR := 180
		restHR := 60
		summary, err := sensor.ParseAndProcess(strings.NewReader(strings.Join(lines, "\n")+"\n"), sensor.ParseOptions{
			CalculationVersion: 3,
			ExpectedProfileID:  testProfileID,
			ExpectedSessionID:  testSessionID,
			EstimatedMaxHR:     &maxHR,
			RestingHR:          &restHR,
		})
		Expect(err).NotTo(HaveOccurred())
		h := summary.Metrics.Hybrid
		Expect(h).NotTo(BeNil())
		Expect(h.HybridIntensity).To(BeNil())
		Expect(h.QualityFlags).To(ContainElements("sensor_data_unreadable", "invalid_timeline_timestamp"))
		Expect(h.QualityFlags).NotTo(ContainElement("no_valid_hybrid_windows"))
	})

	It("clips H_k to 1.0, preserves raw PeakBPM, and flags peak_hr_exceeded_max when HR exceeds frozen MaxHR", func() {
		contactTrue := true
		ndjson := buildHybridNDJSON(
			testProfileID,
			testSessionID,
			30,
			func(_ int) (int, *bool) { return 192, &contactTrue },
			func(_ int, _ int) []float64 { return []float64{0, 0, 1.31} },
		)

		maxHR := 180
		restHR := 60
		enmoRest := 0.01
		enmoRef := 0.31
		summary, err := sensor.ParseAndProcess(ndjson, sensor.ParseOptions{
			CalculationVersion: 3,
			ExpectedProfileID:  testProfileID,
			ExpectedSessionID:  testSessionID,
			EstimatedMaxHR:     &maxHR,
			RestingHR:          &restHR,
			ENMORestG:          &enmoRest,
			ENMORefG:           &enmoRef,
			CalibrationVersion: sensor.PersonalCalibrationVersion,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Metrics.HR.PeakBPM).NotTo(BeNil())
		Expect(*summary.Metrics.HR.PeakBPM).To(Equal(192))

		h := summary.Metrics.Hybrid
		Expect(h).NotTo(BeNil())
		Expect(h.PeakExceededMaxHR).To(BeTrue())
		Expect(h.QualityFlags).To(ContainElement("peak_hr_exceeded_max"))
		Expect(*h.HeartIntensity).To(Equal(1.0))
		Expect(*h.MotionIntensity).To(Equal(1.0))
		Expect(*h.HybridIntensity).To(Equal(100.0))
	})
})
