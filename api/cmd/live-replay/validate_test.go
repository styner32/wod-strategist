package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
)

func validationNumber(v float64) *float64 { return &v }

func validationTelemetry() []byte {
	var lines bytes.Buffer
	write := func(value any) { Expect(json.NewEncoder(&lines).Encode(value)).To(Succeed()) }
	write(map[string]any{"k": "meta", "schema_version": "2.0.0", "workout_session_id": "WOD-20261026-VALIDATION", "profile_id": 10, "clock_source": "capture_clock", "base_epoch_ms": int64(1793000000000)})
	write(map[string]any{"k": "device_ready", "t": 0, "device_name": "Polar H10"})
	write(map[string]any{"k": "stream_start", "t": 0, "stream_id": 1, "sampling": map[string]any{"acc_hz": 50, "acc_range_g": 8}, "clock_anchor": map[string]any{"device_timestamp_ns": "9007199254740993", "capture_offset_ms": 0}})
	samples := make([][]float64, 100)
	for i := range samples {
		samples[i] = []float64{0, 0, 1}
	}
	write(map[string]any{"k": "acc", "t": 0, "dt": 20, "stream_id": 1, "v": samples})
	write(map[string]any{"k": "hr", "t": 0, "bpm": 120})
	write(map[string]any{"k": "hr", "t": 1000, "bpm": 125})
	write(map[string]any{"k": "end", "t": 2000, "pause_intervals": []any{}, "summary": map[string]any{"hr_samples": 2, "acc_samples": 100, "gaps": 0, "write_failures": 0, "dropped_lines": 0}})
	return lines.Bytes()
}

func validationManifest(raw []byte) FixtureManifest {
	stats, err := telemetryStats(raw)
	Expect(err).NotTo(HaveOccurred())
	return FixtureManifest{
		Version: 1, SourceSessionID: defaultSourceSession, SourceProfileID: 1, Bucket: "replay-bucket", FPS: 30,
		Video:          ObjectReference{Object: "videos/1/source/merged.mp4", Generation: "1", SHA256: strings.Repeat("0", 64), SizeBytes: 5},
		Sensor:         ObjectReference{Object: "videos/1/source/sensor.ndjson", Generation: "2", SHA256: digest(raw), SizeBytes: int64(len(raw))},
		MediaStartSecs: 5, MediaEndSecs: 7, CaptureStartMs: 10000, CaptureEndMs: 12000,
		Mapping:    []MappingSegment{{MediaStartMs: 5000, MediaEndMs: 7000, CaptureStartMs: 10000, CaptureEndMs: 12000}},
		SensorFile: "sensor.ndjson", SensorSHA256: digest(raw), SensorSizeBytes: int64(len(raw)), Stats: stats,
		Chunks: []ReplayChunk{{File: "chunks/chunk_0001.mp4", SizeBytes: 5, SHA256: digest([]byte("video")), MediaStartSecs: 0, MediaEndSecs: 2, CaptureStartSecs: 0, CaptureEndSecs: 2, HeartRateBPM: 125}},
	}
}

func validationRow() db.ChunkAnalysisResult {
	return db.ChunkAnalysisResult{ID: 1, SessionID: "WOD-20261026-VALIDATION", ProfileID: 10, FilePath: "gs://replay-bucket/videos/10/WOD-20261026-VALIDATION/chunk_0001.mp4", Status: "COMPLETED", HeartRateBPM: 125, StartSecs: validationNumber(0), EndSecs: validationNumber(2), MediaStartSecs: validationNumber(0), MediaEndSecs: validationNumber(2), MovementObservations: db.NullableJSONDocument(`{"version":1,"duration_secs":2,"target_state":"identified","activity_state":"rest","events":[],"unassessed":[]}`)}
}

func validationSensor(raw []byte) (*sensor.SensorSummaryResult, *sensor.SensorTimelineData) {
	age, maxHR, restingHR := 42, 178, 60
	source := sensor.TimelineSource{SensorVersion: "1", RequestID: "request", SourceGeneration: "42", HRCalculationVersion: 3}
	result, err := sensor.ParseAndProcessWithTimeline(bytes.NewReader(raw), sensor.ParseOptions{CalculationVersion: 3, ExpectedProfileID: 10, ExpectedSessionID: "WOD-20261026-VALIDATION", ExpectedSHA256: digest(raw), ExpectedSizeBytes: int64(len(raw)), Age: &age, EstimatedMaxHR: &maxHR, RestingHR: &restingHR}, source)
	Expect(err).NotTo(HaveOccurred())
	Expect(result.TimelineError).NotTo(HaveOccurred())
	Expect(result.Timeline).NotTo(BeNil())
	result.Summary.Version = 1
	result.Summary.RequestID = "request"
	result.Summary.SourceGeneration = "42"
	return result.Summary, result.Timeline
}

var _ = Describe("Live replay fixture validation", func() {
	It("checks file integrity, raw sample bounds, chunk HR and original time mapping", func() {
		raw := validationTelemetry()
		m := validationManifest(raw)
		dir := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(dir, "chunks"), 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, m.SensorFile), raw, 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, m.Chunks[0].File), []byte("video"), 0600)).To(Succeed())
		got, err := validateFixture(dir, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(raw))
		Expect(os.WriteFile(filepath.Join(dir, m.SensorFile), append(raw, '\n'), 0600)).To(Succeed())
		_, err = validateFixture(dir, m)
		Expect(err).To(MatchError(ContainSubstring("integrity")))
	})

	It("rejects a plausible-looking manifest whose capture offsets no longer match its verified media mapping", func() {
		m := validationManifest(validationTelemetry())
		m.CaptureStartMs += 100
		_, err := validateFixture(GinkgoT().TempDir(), m)
		Expect(err).To(MatchError(ContainSubstring("boundaries")))
	})

	It("preserves a source capture gap at a media seam without assigning it to the preceding chunk", func() {
		raw := validationTelemetry()
		m := validationManifest(raw)
		m.ChunkSeconds = 1
		m.Mapping = []MappingSegment{
			{MediaStartMs: 5000, MediaEndMs: 6000, CaptureStartMs: 10000, CaptureEndMs: 10999.5},
			{MediaStartMs: 6000, MediaEndMs: 7000, CaptureStartMs: 11000, CaptureEndMs: 12000},
		}
		m.Chunks[0].MediaEndSecs, m.Chunks[0].CaptureEndSecs, m.Chunks[0].HeartRateBPM = 1, 0.9995, 120
		second := m.Chunks[0]
		second.File = "chunks/chunk_0002.mp4"
		second.MediaStartSecs, second.MediaEndSecs, second.CaptureStartSecs, second.CaptureEndSecs, second.HeartRateBPM = 1, 2, 1, 2, 125
		m.Chunks = append(m.Chunks, second)
		dir := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(dir, "chunks"), 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, m.SensorFile), raw, 0600)).To(Succeed())
		for _, chunk := range m.Chunks {
			Expect(os.WriteFile(filepath.Join(dir, chunk.File), []byte("video"), 0600)).To(Succeed())
		}
		_, err := validateFixture(dir, m)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("Live replay chunk result validation", func() {
	var m FixtureManifest
	BeforeEach(func() { m = validationManifest(validationTelemetry()) })
	It("checks returned sensor/video mapping values instead of just the segment count", func() {
		rows := []db.ChunkAnalysisResult{validationRow()}
		segments := []MappingSegment{{CaptureStartMs: 0, CaptureEndMs: 2000, MediaStartMs: 0, MediaEndMs: 2000}}
		Expect(validateTimelineMapping(rows, segments)).To(Succeed())
		segments[0].MediaStartMs = 10
		Expect(validateTimelineMapping(rows, segments)).To(MatchError(ContainSubstring("disagrees")))
	})

	It("waits for missing chunks and verified media mapping", func() {
		ready, err := validateChunkResults(m, nil, "WOD-20261026-VALIDATION", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		row := validationRow()
		row.MediaStartSecs, row.MediaEndSecs = nil, nil
		ready, err = validateChunkResults(m, []db.ChunkAnalysisResult{row}, row.SessionID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
	})

	It("rejects duplicate results instead of letting a successful duplicate hide a failure", func() {
		row := validationRow()
		duplicate := row
		duplicate.ID = 2
		_, err := validateChunkResults(m, []db.ChunkAnalysisResult{row, duplicate}, row.SessionID, false)
		Expect(err).To(MatchError(ContainSubstring("duplicate")))
	})

	It("rejects a matching basename from another session's GCS prefix", func() {
		row := validationRow()
		row.FilePath = "gs://replay-bucket/videos/10/WOD-OTHER/chunk_0001.mp4"
		_, err := validateChunkResults(m, []db.ChunkAnalysisResult{row}, row.SessionID, false)
		Expect(err).To(MatchError(ContainSubstring("ownership")))
	})

	It("rejects changed peak HR or capture intervals", func() {
		row := validationRow()
		row.HeartRateBPM = 120
		_, err := validateChunkResults(m, []db.ChunkAnalysisResult{row}, row.SessionID, false)
		Expect(err).To(MatchError(ContainSubstring("peak HR")))
		row = validationRow()
		row.StartSecs = validationNumber(0.01)
		_, err = validateChunkResults(m, []db.ChunkAnalysisResult{row}, row.SessionID, false)
		Expect(err).To(MatchError(ContainSubstring("capture")))
	})

	It("rejects empty or malformed live observations while allowing explicit unassessed evidence", func() {
		row := validationRow()
		row.MovementObservations = db.NullableJSONDocument(`{}`)
		_, err := validateChunkResults(m, []db.ChunkAnalysisResult{row}, row.SessionID, false)
		Expect(err).To(MatchError(ContainSubstring("observations")))
		row.MovementObservations = db.NullableJSONDocument(`{"version":1,"duration_secs":2,"target_state":"not_visible","activity_state":"unknown","events":[],"unassessed":[{"start_secs":0,"end_secs":2,"reason":"occlusion"}]}`)
		ready, err := validateChunkResults(m, []db.ChunkAnalysisResult{row}, row.SessionID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
	})

	It("allows one-frame media rounding while preserving capture intervals", func() {
		row := validationRow()
		row.MediaEndSecs = validationNumber(2.02)
		ready, err := validateChunkResults(m, []db.ChunkAnalysisResult{row}, row.SessionID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
		row.MediaEndSecs = validationNumber(2.1)
		_, err = validateChunkResults(m, []db.ChunkAnalysisResult{row}, row.SessionID, true)
		Expect(err).To(MatchError(ContainSubstring("mapping")))
	})
})

var _ = Describe("Live replay sensor result validation", func() {
	It("validates the uploaded measured data against fresh summary and timeline identities", func() {
		raw := validationTelemetry()
		summary, timeline := validationSensor(raw)
		Expect(validateSensorResult(validationManifest(raw), raw, *summary, timeline, "request", "1")).To(Succeed())
		timeline.Source.SourceGeneration = "stale-generation"
		Expect(validateSensorResult(validationManifest(raw), raw, *summary, timeline, "request", "1")).To(MatchError(ContainSubstring("identity")))
	})

	It("detects missing measured samples and a changed ACC bucket despite successful processing", func() {
		raw := validationTelemetry()
		m := validationManifest(raw)
		summary, timeline := validationSensor(raw)
		m.Stats.ACCSamples++
		Expect(validateSensorResult(m, raw, *summary, timeline, "request", "1")).To(MatchError(ContainSubstring("sample counts")))
		m = validationManifest(raw)
		Expect(timeline.Points).NotTo(BeEmpty())
		timeline.Points[0].AccMagnitudeStdG.Value = validationNumber(0.99)
		Expect(validateSensorResult(m, raw, *summary, timeline, "request", "1")).To(MatchError(ContainSubstring("bucket")))
	})

	It("rejects incorrect summary peak, zones and hybrid load even when all timeline buckets are correct", func() {
		raw := validationTelemetry()
		m := validationManifest(raw)
		summary, timeline := validationSensor(raw)
		wrongPeak := 199
		summary.Metrics.HR.PeakBPM = &wrongPeak
		Expect(validateSensorResult(m, raw, *summary, timeline, "request", "1")).To(MatchError(ContainSubstring("HR/ACC metrics")))

		summary, timeline = validationSensor(raw)
		Expect(summary.Metrics.HR.Zones).NotTo(BeNil())
		summary.Metrics.HR.Zones.Zone5Seconds += 1
		Expect(validateSensorResult(m, raw, *summary, timeline, "request", "1")).To(MatchError(ContainSubstring("HR/ACC metrics")))

		summary, timeline = validationSensor(raw)
		Expect(summary.Metrics.Hybrid).NotTo(BeNil())
		summary.Metrics.Hybrid.HybridLoadAU = validationNumber(999)
		Expect(validateSensorResult(m, raw, *summary, timeline, "request", "1")).To(MatchError(ContainSubstring("hybrid")))

		summary, timeline = validationSensor(raw)
		summary.Metrics.Hybrid.QualityFlags = append(summary.Metrics.Hybrid.QualityFlags, "unexpected")
		Expect(validateSensorResult(m, raw, *summary, timeline, "request", "1")).To(MatchError(ContainSubstring("hybrid")))
	})
})

var _ = Describe("Live replay activity review validation", func() {
	var rows []db.ChunkAnalysisResult
	var summary activity.Summary
	BeforeEach(func() {
		rows = []db.ChunkAnalysisResult{validationRow()}
		summary = activity.Summary{Version: activity.Version, ReviewVersion: activity.Version, Available: true, ReviewState: "partial", CoverageScope: "recorded_chunks", MediaGeneration: "42", SourceVersion: activity.SourceVersion(rows), Reviews: []activity.ReviewedChunk{{ChunkID: 1, State: "completed"}}, Unassessed: []activity.Gap{{ChunkID: 1, Reason: "occlusion"}}}
	})

	It("keeps unassessable visual evidence while requiring completed processing for each chunk", func() {
		ready, err := validateActivityResult(rows, summary)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
		summary.Reviews[0].State = "failed"
		_, err = validateActivityResult(rows, summary)
		Expect(err).To(MatchError(ContainSubstring("processing failed")))
	})

	It("does not pass disabled, omitted or stale reviews", func() {
		summary.ReviewState = "disabled"
		_, err := validateActivityResult(rows, summary)
		Expect(err).To(MatchError(ContainSubstring("not verified")))
		summary.ReviewState = "completed"
		summary.SourceVersion = "old"
		_, err = validateActivityResult(rows, summary)
		Expect(err).To(MatchError(ContainSubstring("stale")))
		summary.SourceVersion = activity.SourceVersion(rows)
		summary.Reviews = nil
		_, err = validateActivityResult(rows, summary)
		Expect(err).To(MatchError(ContainSubstring("omitted")))
	})
})
