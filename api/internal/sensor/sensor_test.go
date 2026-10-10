package sensor_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wod-strategist/api/internal/sensor"
)

func computeHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

var _ = Describe("Sensor Parser", func() {
	It("handles floating point timestamps", func() {
		opts := sensor.ParseOptions{
			ExpectedProfileID: 1,
			ExpectedSessionID: "WARMUP-20260908-01M1Z6RVEJZJ9HSWFAR9NWX926",
		}

		content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WARMUP-20260908-01M1Z6RVEJZJ9HSWFAR9NWX926","profile_id":1,"clock_source":"capture_clock","base_epoch_ms":1757302488832}
{"k":"stream_start","t":4400.45,"stream_id":1,"sampling":{"acc_hz":52,"acc_range_g":8,"ecg_hz":130,"delta_compressed":false},"clock_anchor":{"device_timestamp_ns":1625902167906250,"capture_offset_ms":4400.45,"method":"bluetooth_notification"}}
{"k":"acc","t":4400.45,"stream_id":1,"dt":19.53,"v":[[0.05,0.98,0.02],[0.06,0.97,0.03]]}
{"k":"hr","t":4500.25,"bpm":150}
{"k":"hr","t":5500.75,"bpm":155}
{"k":"end","t":6000.5,"pause_intervals":[{"start_offset_ms":5000.1,"end_offset_ms":5100.2}],"device":{"battery_percent_end":85}}
`
		opts.ExpectedSHA256 = computeHash(content)
		opts.ExpectedSizeBytes = int64(len(content))

		res, err := sensor.ParseAndProcess(strings.NewReader(content), opts)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Quality.IsComplete).To(BeTrue(), "expected complete, got errors: %v", res.Quality.Errors)
		Expect(res.Quality.Status).To(Equal("ok"))
	})

	Context("P1 Quality and Limits", func() {
		opts := sensor.ParseOptions{
			ExpectedProfileID: 42,
			ExpectedSessionID: "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF",
		}

		It("marks incomplete and no valid HR when end is missing", func() {
			content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"hr","t":1000,"bpm":150}
{"k":"hr","t":2000,"bpm":155}
`
			currentOpts := opts
			currentOpts.ExpectedSHA256 = computeHash(content)
			res, err := sensor.ParseAndProcess(strings.NewReader(content), currentOpts)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Quality.IsComplete).To(BeFalse())
			Expect(res.Quality.Status).To(Equal("incomplete"))
			Expect(res.Quality.ValidHR).To(BeFalse())
			Expect(res.HRBonus).To(Equal(0.0))
		})

		It("rejects identity mismatch on profile_id", func() {
			content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":99,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"end","t":5000,"pause_intervals":[],"device":{"battery_percent_end":80},"summary":{"hr_samples":0,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
`
			currentOpts := opts
			currentOpts.ExpectedSHA256 = computeHash(content)
			res, err := sensor.ParseAndProcess(strings.NewReader(content), currentOpts)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Quality.IsComplete).To(BeFalse())
			Expect(res.Quality.Status).To(Equal("corrupt"))
			Expect(res.Quality.Errors).NotTo(BeEmpty())
			Expect(res.Quality.Errors[0]).To(ContainSubstring("profile_id"))
		})

		It("rejects SHA-256 hash mismatch", func() {
			content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"end","t":5000,"pause_intervals":[],"device":{"battery_percent_end":80},"summary":{"hr_samples":0,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
`
			mismatchOpts := opts
			mismatchOpts.ExpectedSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
			res, err := sensor.ParseAndProcess(strings.NewReader(content), mismatchOpts)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Quality.IsComplete).To(BeFalse())
			Expect(res.Quality.Status).To(Equal("corrupt"))
			Expect(res.Quality.Errors).NotTo(BeEmpty())
			Expect(res.Quality.Errors[0]).To(ContainSubstring("SHA-256 mismatch"))
		})

		It("rejects line exceeding 1 MiB", func() {
			hugeLine := strings.Repeat("A", 1024*1024+10)
			content := fmt.Sprintf(`{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"comment","data":"%s"}
{"k":"end","t":5000,"pause_intervals":[],"device":{"battery_percent_end":80},"summary":{"hr_samples":0,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
`, hugeLine)
			currentOpts := opts
			currentOpts.ExpectedSHA256 = computeHash(content)
			res, err := sensor.ParseAndProcess(strings.NewReader(content), currentOpts)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Quality.IsComplete).To(BeFalse())
			Expect(res.Quality.Status).To(Equal("corrupt"))
			Expect(res.Quality.Errors).NotTo(BeEmpty())
			Expect(res.Quality.Errors[0]).To(ContainSubstring("1 MiB limit"))
		})

		It("rejects non-empty line after end event", func() {
			content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"end","t":5000,"pause_intervals":[],"device":{"battery_percent_end":80},"summary":{"hr_samples":0,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
{"k":"hr","t":6000,"bpm":150}
`
			currentOpts := opts
			currentOpts.ExpectedSHA256 = computeHash(content)
			res, err := sensor.ParseAndProcess(strings.NewReader(content), currentOpts)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Quality.IsComplete).To(BeFalse())
			Expect(res.Quality.Status).To(Equal("corrupt"))
			Expect(res.Quality.Errors).NotTo(BeEmpty())
			Expect(res.Quality.Errors[0]).To(ContainSubstring("after end event"))
		})
	})

	Context("P2 Stream and Pauses", func() {
		opts := sensor.ParseOptions{
			ExpectedProfileID: 42,
			ExpectedSessionID: "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF",
		}

		It("handles numeric stream_id and pause interval subtraction", func() {
			content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"stream_start","t":0,"stream_id":1,"sampling":{"acc_hz":50,"acc_range_g":8,"acc_resolution_bits":16,"frame_type":1,"delta_compressed":false},"clock_anchor":{"device_timestamp_ns":100000,"capture_offset_ms":0,"method":"first_packet"}}
{"k":"hr","t":1000,"bpm":150}
{"k":"hr","t":3000,"bpm":150}
{"k":"hr","t":5000,"bpm":150}
{"k":"pause","t":5000}
{"k":"resume","t":8000}
{"k":"hr","t":8000,"bpm":150}
{"k":"hr","t":10000,"bpm":150}
{"k":"end","t":10000,"pause_intervals":[{"start_offset_ms":5000,"end_offset_ms":8000}],"device":{"battery_percent_end":85},"summary":{"hr_samples":5,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
`
			currentOpts := opts
			currentOpts.ExpectedSHA256 = computeHash(content)
			res, err := sensor.ParseAndProcess(strings.NewReader(content), currentOpts)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Quality.IsComplete).To(BeTrue())
			Expect(res.Quality.Status).To(Equal("ok"))
			Expect(res.Metrics.DurationSeconds).To(Equal(7.0))
			Expect(res.Metrics.PauseSeconds).To(Equal(3.0))
		})
	})

	Context("P3 HR Metrics and Bonus", func() {
		age := 30
		maxHR := 220 - age // 190
		opts := sensor.ParseOptions{
			ExpectedProfileID: 42,
			ExpectedSessionID: "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF",
			Age:               &age,
			EstimatedMaxHR:    &maxHR,
		}

		It("evaluates coverage 50% threshold: 49% invalid, 50% valid", func() {
			var buf bytes.Buffer
			buf.WriteString(`{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}` + "\n")
			for tMs := 0; tMs <= 49000; tMs += 1000 {
				buf.WriteString(fmt.Sprintf(`{"k":"hr","t":%d,"bpm":160}`+"\n", tMs))
			}
			buf.WriteString(`{"k":"end","t":100000,"pause_intervals":[],"device":{"battery_percent_end":90},"summary":{"hr_samples":50,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}` + "\n")

			content := buf.String()
			opts49 := opts
			opts49.ExpectedSHA256 = computeHash(content)
			res49, err := sensor.ParseAndProcess(strings.NewReader(content), opts49)
			Expect(err).NotTo(HaveOccurred())
			Expect(res49.Metrics.HR.Coverage).To(BeNumerically("~", 0.49, 0.01))
			Expect(res49.Quality.ValidHR).To(BeFalse())
			Expect(res49.HRBonus).To(Equal(0.0))

			// 50s valid HR: coverage = 50% -> valid_hr = true, bonus = (160 - 140) * 0.5 = 10.0
			buf.Reset()
			buf.WriteString(`{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}` + "\n")
			for tMs := 0; tMs <= 50000; tMs += 1000 {
				buf.WriteString(fmt.Sprintf(`{"k":"hr","t":%d,"bpm":160}`+"\n", tMs))
			}
			buf.WriteString(`{"k":"end","t":100000,"pause_intervals":[],"device":{"battery_percent_end":90},"summary":{"hr_samples":51,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}` + "\n")

			content50 := buf.String()
			opts50 := opts
			opts50.ExpectedSHA256 = computeHash(content50)
			res50, err := sensor.ParseAndProcess(strings.NewReader(content50), opts50)
			Expect(err).NotTo(HaveOccurred())
			Expect(res50.Metrics.HR.Coverage).To(BeNumerically("~", 0.50, 0.01))
			Expect(res50.Quality.ValidHR).To(BeTrue())
			Expect(res50.HRBonus).To(BeNumerically("~", 10.0, 0.01))
			Expect(res50.Metrics.HR.WeightedMeanBPM).NotTo(BeNil())
			Expect(*res50.Metrics.HR.WeightedMeanBPM).To(BeNumerically("~", 160.0, 0.01))
		})

		It("checks BPM bounds (29 ignored, 30 accepted, 240 accepted, 241 ignored)", func() {
			content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"hr","t":1000,"bpm":29}
{"k":"hr","t":2000,"bpm":30}
{"k":"hr","t":3000,"bpm":240}
{"k":"hr","t":4000,"bpm":241}
{"k":"end","t":5000,"pause_intervals":[],"device":{"battery_percent_end":90},"summary":{"hr_samples":4,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
`
			optsBounds := opts
			optsBounds.ExpectedSHA256 = computeHash(content)
			res, err := sensor.ParseAndProcess(strings.NewReader(content), optsBounds)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Metrics.HR.MinBPM).NotTo(BeNil())
			Expect(*res.Metrics.HR.MinBPM).To(Equal(30))
			Expect(res.Metrics.HR.PeakBPM).NotTo(BeNil())
			Expect(*res.Metrics.HR.PeakBPM).To(Equal(240))
		})

		It("clamps HR bonus to [0, 20]", func() {
			var buf bytes.Buffer
			buf.WriteString(`{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}` + "\n")
			for tMs := 0; tMs <= 10000; tMs += 1000 {
				buf.WriteString(fmt.Sprintf(`{"k":"hr","t":%d,"bpm":130}`+"\n", tMs))
			}
			buf.WriteString(`{"k":"end","t":10000,"pause_intervals":[],"device":{"battery_percent_end":90},"summary":{"hr_samples":11,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}` + "\n")

			c1 := buf.String()
			optsLow := opts
			optsLow.ExpectedSHA256 = computeHash(c1)
			resLow, err := sensor.ParseAndProcess(strings.NewReader(c1), optsLow)
			Expect(err).NotTo(HaveOccurred())
			Expect(resLow.HRBonus).To(Equal(0.0))

			buf.Reset()
			buf.WriteString(`{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}` + "\n")
			for tMs := 0; tMs <= 10000; tMs += 1000 {
				buf.WriteString(fmt.Sprintf(`{"k":"hr","t":%d,"bpm":190}`+"\n", tMs))
			}
			buf.WriteString(`{"k":"end","t":10000,"pause_intervals":[],"device":{"battery_percent_end":90},"summary":{"hr_samples":11,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}` + "\n")

			c2 := buf.String()
			optsHigh := opts
			optsHigh.ExpectedSHA256 = computeHash(c2)
			resHigh, err := sensor.ParseAndProcess(strings.NewReader(c2), optsHigh)
			Expect(err).NotTo(HaveOccurred())
			Expect(resHigh.HRBonus).To(Equal(20.0))
		})
	})

	Describe("CaptureToMedia", func() {
		chunks := []sensor.ChunkTimeline{
			{CaptureStartMs: 0, CaptureEndMs: 10000, MediaStartMs: 0.0, MediaEndMs: 10.0, IsFinal: false},
			{CaptureStartMs: 12000, CaptureEndMs: 22000, MediaStartMs: 10.0, MediaEndMs: 20.0, IsFinal: true},
		}

		It("maps timestamps correctly", func() {
			media, ok := sensor.CaptureToMedia(5000, chunks)
			Expect(ok).To(BeTrue())
			Expect(media).To(BeNumerically("~", 5.0, 0.001))

			_, ok = sensor.CaptureToMedia(11000, chunks)
			Expect(ok).To(BeFalse(), "expected false for gap")

			media, ok = sensor.CaptureToMedia(15000, chunks)
			Expect(ok).To(BeTrue())
			Expect(media).To(BeNumerically("~", 13.0, 0.001))

			media, ok = sensor.CaptureToMedia(22000, chunks)
			Expect(ok).To(BeTrue())
			Expect(media).To(BeNumerically("~", 20.0, 0.001))
		})

		It("returns false for overlapping chunks", func() {
			badChunks := []sensor.ChunkTimeline{
				{CaptureStartMs: 0, CaptureEndMs: 10000, MediaStartMs: 0.0, MediaEndMs: 10.0, IsFinal: false},
				{CaptureStartMs: 8000, CaptureEndMs: 15000, MediaStartMs: 10.0, MediaEndMs: 17.0, IsFinal: true},
			}
			_, ok := sensor.CaptureToMedia(5000, badChunks)
			Expect(ok).To(BeFalse(), "expected false for overlapping chunks")
		})
	})

	Describe("MediaToCapture", func() {
		chunks := []sensor.ChunkTimeline{
			{CaptureStartMs: 0, CaptureEndMs: 10000, MediaStartMs: 0.0, MediaEndMs: 10.0, IsFinal: false},
			{CaptureStartMs: 12000, CaptureEndMs: 22000, MediaStartMs: 10.0, MediaEndMs: 20.0, IsFinal: true},
		}

		It("maps timestamps correctly", func() {
			capMs, ok := sensor.MediaToCapture(5.0, chunks)
			Expect(ok).To(BeTrue())
			Expect(capMs).To(Equal(int64(5000)))

			capMs, ok = sensor.MediaToCapture(13.0, chunks)
			Expect(ok).To(BeTrue())
			Expect(capMs).To(Equal(int64(15000)))

			capMs, ok = sensor.MediaToCapture(20.0, chunks)
			Expect(ok).To(BeTrue())
			Expect(capMs).To(Equal(int64(22000)))

			_, ok = sensor.MediaToCapture(25.0, chunks)
			Expect(ok).To(BeFalse(), "expected false for out-of-range media time")
		})
	})

	Describe("Duplicate HR Timestamps", func() {
		It("processes duplicate timestamps without error", func() {
			optsV2 := sensor.ParseOptions{
				CalculationVersion: 2,
				ExpectedProfileID:  1,
				ExpectedSessionID:  "WOD-20260917-01M2PERXWM5NAA8ABX5Z44GH4B",
			}

			content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260917-01M2PERXWM5NAA8ABX5Z44GH4B","profile_id":1,"clock_source":"capture_clock","base_epoch_ms":1789607573396}
{"k":"stream_start","t":1000,"stream_id":1,"sampling":{"acc_hz":50,"acc_range_g":8}}
{"k":"hr","t":1000,"bpm":100,"rr":[600]}
{"k":"hr","t":2000,"bpm":110,"rr":[550]}
{"k":"hr","t":2000,"bpm":112,"rr":[540]}
{"k":"hr","t":3000,"bpm":120,"rr":[500]}
{"k":"end","t":4000,"pause_intervals":[]}
`
			optsV2.ExpectedSHA256 = computeHash(content)
			optsV2.ExpectedSizeBytes = int64(len(content))

			timelineSource := sensor.TimelineSource{
				SensorVersion:        "1",
				RequestID:            "test-req",
				SourceGeneration:     "100",
				HRCalculationVersion: 2,
			}

			res, err := sensor.ParseAndProcessWithTimeline(strings.NewReader(content), optsV2, timelineSource)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Summary.Quality.IsComplete).To(BeTrue(), "expected IsComplete=true, got errors: %v", res.Summary.Quality.Errors)
			Expect(res.Summary.Quality.Status).To(Equal("ok"))
			Expect(res.Summary.Quality.ValidHR).To(BeTrue())
			Expect(res.Timeline).NotTo(BeNil())

			// Also test CalculationVersion 1
			optsV1 := optsV2
			optsV1.CalculationVersion = 1
			resV1, err := sensor.ParseAndProcess(strings.NewReader(content), optsV1)
			Expect(err).NotTo(HaveOccurred())
			Expect(resV1.Quality.IsComplete).To(BeTrue(), "expected V1 IsComplete=true, got errors: %v", resV1.Quality.Errors)

			// Verify real session file if available on local disk
			if data, err := os.ReadFile("/tmp/sensor_failed_20260917.ndjson"); err == nil {
				h := sha256.Sum256(data)
				realOpts := sensor.ParseOptions{
					CalculationVersion: 2,
					ExpectedProfileID:  1,
					ExpectedSessionID:  "WOD-20260917-01M2PERXWM5NAA8ABX5Z44GH4B",
					ExpectedSHA256:     hex.EncodeToString(h[:]),
					ExpectedSizeBytes:  int64(len(data)),
				}
				realSource := sensor.TimelineSource{
					SensorVersion:        "1",
					RequestID:            "664fda62-bf90-4b69-a3f3-26b5d202f89a",
					SourceGeneration:     "1",
					HRCalculationVersion: 2,
				}
				realRes, err := sensor.ParseAndProcessWithTimeline(bytes.NewReader(data), realOpts, realSource)
				Expect(err).NotTo(HaveOccurred())
				Expect(realRes.Summary.Quality.IsComplete).To(BeTrue(), "expected real file IsComplete=true, got: %v", realRes.Summary.Quality.Errors)
				Expect(realRes.Summary.Quality.Status).To(Equal("ok"))
				Expect(realRes.Summary.Quality.ValidHR).To(BeTrue())
				Expect(realRes.Timeline).NotTo(BeNil())
			}
		})
	})
})
