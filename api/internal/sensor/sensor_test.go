package sensor_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/wod-strategist/api/internal/sensor"
)

func computeHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

func TestSensorParser_FloatingPointTimestamps(t *testing.T) {
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
	if err != nil {
		t.Fatalf("ParseAndProcess failed: %v", err)
	}
	if !res.Quality.IsComplete {
		t.Fatalf("expected complete, got errors: %v", res.Quality.Errors)
	}
	if res.Quality.Status != "ok" {
		t.Errorf("expected status 'ok', got %s", res.Quality.Status)
	}
}


func TestSensorParser_P1_QualityAndLimits(t *testing.T) {
	opts := sensor.ParseOptions{
		ExpectedProfileID: 42,
		ExpectedSessionID: "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF",
	}

	t.Run("End missing results in incomplete status and no valid HR", func(t *testing.T) {
		content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"hr","t":1000,"bpm":150}
{"k":"hr","t":2000,"bpm":155}
`
		opts.ExpectedSHA256 = computeHash(content)
		res, err := sensor.ParseAndProcess(strings.NewReader(content), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Quality.IsComplete {
			t.Errorf("expected IsComplete=false")
		}
		if res.Quality.Status != "incomplete" {
			t.Errorf("expected status=incomplete, got %s", res.Quality.Status)
		}
		if res.Quality.ValidHR {
			t.Errorf("expected ValidHR=false")
		}
		if res.HRBonus != 0.0 {
			t.Errorf("expected HRBonus=0, got %f", res.HRBonus)
		}
	})

	t.Run("Identity mismatch (profile_id)", func(t *testing.T) {
		content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":99,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"end","t":5000,"pause_intervals":[],"device":{"battery_percent_end":80},"summary":{"hr_samples":0,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
`
		opts.ExpectedSHA256 = computeHash(content)
		res, err := sensor.ParseAndProcess(strings.NewReader(content), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Quality.IsComplete {
			t.Errorf("expected IsComplete=false")
		}
		if res.Quality.Status != "corrupt" {
			t.Errorf("expected status=corrupt, got %s", res.Quality.Status)
		}
		if len(res.Quality.Errors) == 0 || !strings.Contains(res.Quality.Errors[0], "profile_id") {
			t.Errorf("expected profile_id error, got %v", res.Quality.Errors)
		}
	})

	t.Run("SHA-256 hash mismatch", func(t *testing.T) {
		content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"end","t":5000,"pause_intervals":[],"device":{"battery_percent_end":80},"summary":{"hr_samples":0,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
`
		mismatchOpts := opts
		mismatchOpts.ExpectedSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		res, err := sensor.ParseAndProcess(strings.NewReader(content), mismatchOpts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Quality.IsComplete {
			t.Errorf("expected IsComplete=false")
		}
		if res.Quality.Status != "corrupt" {
			t.Errorf("expected status=corrupt, got %s", res.Quality.Status)
		}
		if len(res.Quality.Errors) == 0 || !strings.Contains(res.Quality.Errors[0], "SHA-256 mismatch") {
			t.Errorf("expected SHA-256 mismatch error, got %v", res.Quality.Errors)
		}
	})

	t.Run("Line exceeding 1 MiB rejected", func(t *testing.T) {
		hugeLine := strings.Repeat("A", 1024*1024+10)
		content := fmt.Sprintf(`{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"comment","data":"%s"}
{"k":"end","t":5000,"pause_intervals":[],"device":{"battery_percent_end":80},"summary":{"hr_samples":0,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
`, hugeLine)
		opts.ExpectedSHA256 = computeHash(content)
		res, err := sensor.ParseAndProcess(strings.NewReader(content), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Quality.IsComplete {
			t.Errorf("expected IsComplete=false")
		}
		if res.Quality.Status != "corrupt" {
			t.Errorf("expected status=corrupt, got %s", res.Quality.Status)
		}
		if len(res.Quality.Errors) == 0 || !strings.Contains(res.Quality.Errors[0], "1 MiB limit") {
			t.Errorf("expected 1 MiB limit error, got %v", res.Quality.Errors)
		}
	})

	t.Run("Non-empty line after end event rejected", func(t *testing.T) {
		content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF","profile_id":42,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"end","t":5000,"pause_intervals":[],"device":{"battery_percent_end":80},"summary":{"hr_samples":0,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
{"k":"hr","t":6000,"bpm":150}
`
		opts.ExpectedSHA256 = computeHash(content)
		res, err := sensor.ParseAndProcess(strings.NewReader(content), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Quality.IsComplete {
			t.Errorf("expected IsComplete=false")
		}
		if res.Quality.Status != "corrupt" {
			t.Errorf("expected status=corrupt, got %s", res.Quality.Status)
		}
		if len(res.Quality.Errors) == 0 || !strings.Contains(res.Quality.Errors[0], "after end event") {
			t.Errorf("expected after end event error, got %v", res.Quality.Errors)
		}
	})
}

func TestSensorParser_P2_StreamAndPauses(t *testing.T) {
	opts := sensor.ParseOptions{
		ExpectedProfileID: 42,
		ExpectedSessionID: "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF",
	}

	t.Run("Numeric stream_id and pause interval subtraction", func(t *testing.T) {
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
		opts.ExpectedSHA256 = computeHash(content)
		res, err := sensor.ParseAndProcess(strings.NewReader(content), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Quality.IsComplete {
			t.Errorf("expected IsComplete=true")
		}
		if res.Quality.Status != "ok" {
			t.Errorf("expected status=ok, got %s", res.Quality.Status)
		}
		if res.Metrics.DurationSeconds != 7.0 {
			t.Errorf("expected duration=7.0, got %f", res.Metrics.DurationSeconds)
		}
		if res.Metrics.PauseSeconds != 3.0 {
			t.Errorf("expected pause=3.0, got %f", res.Metrics.PauseSeconds)
		}
	})
}

func TestSensorParser_P3_HRMetricsAndBonus(t *testing.T) {
	age := 30
	maxHR := 220 - age // 190
	opts := sensor.ParseOptions{
		ExpectedProfileID: 42,
		ExpectedSessionID: "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF",
		Age:               &age,
		EstimatedMaxHR:    &maxHR,
	}

	t.Run("Coverage 50% threshold: 49% invalid, 50% valid", func(t *testing.T) {
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
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if math.Abs(res49.Metrics.HR.Coverage-0.49) > 0.01 {
			t.Errorf("expected coverage ~0.49, got %f", res49.Metrics.HR.Coverage)
		}
		if res49.Quality.ValidHR {
			t.Errorf("expected ValidHR=false for 49%% coverage")
		}
		if res49.HRBonus != 0.0 {
			t.Errorf("expected HRBonus=0, got %f", res49.HRBonus)
		}

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
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if math.Abs(res50.Metrics.HR.Coverage-0.50) > 0.01 {
			t.Errorf("expected coverage ~0.50, got %f", res50.Metrics.HR.Coverage)
		}
		if !res50.Quality.ValidHR {
			t.Errorf("expected ValidHR=true for 50%% coverage")
		}
		if math.Abs(res50.HRBonus-10.0) > 0.01 {
			t.Errorf("expected HRBonus=10.0, got %f", res50.HRBonus)
		}
		if res50.Metrics.HR.WeightedMeanBPM == nil || math.Abs(*res50.Metrics.HR.WeightedMeanBPM-160.0) > 0.01 {
			t.Errorf("expected weighted mean BPM ~160, got %v", res50.Metrics.HR.WeightedMeanBPM)
		}
	})

	t.Run("BPM bounds check (29 ignored, 30 accepted, 240 accepted, 241 ignored)", func(t *testing.T) {
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
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Metrics.HR.MinBPM == nil || *res.Metrics.HR.MinBPM != 30 {
			t.Errorf("expected min BPM=30, got %v", res.Metrics.HR.MinBPM)
		}
		if res.Metrics.HR.PeakBPM == nil || *res.Metrics.HR.PeakBPM != 240 {
			t.Errorf("expected peak BPM=240, got %v", res.Metrics.HR.PeakBPM)
		}
	})

	t.Run("HR bonus clamps to [0, 20]", func(t *testing.T) {
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
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resLow.HRBonus != 0.0 {
			t.Errorf("expected HRBonus=0, got %f", resLow.HRBonus)
		}

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
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resHigh.HRBonus != 20.0 {
			t.Errorf("expected HRBonus=20.0, got %f", resHigh.HRBonus)
		}
	})
}

func TestCaptureToMediaMapping(t *testing.T) {
	chunks := []sensor.ChunkTimeline{
		{CaptureStartMs: 0, CaptureEndMs: 10000, MediaStartMs: 0.0, MediaEndMs: 10.0, IsFinal: false},
		{CaptureStartMs: 12000, CaptureEndMs: 22000, MediaStartMs: 10.0, MediaEndMs: 20.0, IsFinal: true},
	}

	media, ok := sensor.CaptureToMedia(5000, chunks)
	if !ok || math.Abs(media-5.0) > 0.001 {
		t.Errorf("expected 5.0, got %f, ok=%v", media, ok)
	}

	_, ok = sensor.CaptureToMedia(11000, chunks)
	if ok {
		t.Errorf("expected false for gap")
	}

	media, ok = sensor.CaptureToMedia(15000, chunks)
	if !ok || math.Abs(media-13.0) > 0.001 {
		t.Errorf("expected 13.0, got %f, ok=%v", media, ok)
	}

	media, ok = sensor.CaptureToMedia(22000, chunks)
	if !ok || math.Abs(media-20.0) > 0.001 {
		t.Errorf("expected 20.0, got %f, ok=%v", media, ok)
	}

	badChunks := []sensor.ChunkTimeline{
		{CaptureStartMs: 0, CaptureEndMs: 10000, MediaStartMs: 0.0, MediaEndMs: 10.0, IsFinal: false},
		{CaptureStartMs: 8000, CaptureEndMs: 15000, MediaStartMs: 10.0, MediaEndMs: 17.0, IsFinal: true},
	}
	_, ok = sensor.CaptureToMedia(5000, badChunks)
	if ok {
		t.Errorf("expected false for overlapping chunks")
	}
}
