package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

const (
	runTestAPI       = "https://live.test"
	runTestStorage   = "https://storage.test"
	runTestSessionID = "WOD-20261026-01M3QZH8TPXYMBGP9ER5W6SMBB"
	runTestRequestID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

type runTestClock struct {
	now time.Time
}

func (c *runTestClock) current() time.Time { return c.now }
func (c *runTestClock) wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = c.now.Add(delay)
	return nil
}

func runTestFixture(realMedia bool) (string, FixtureManifest, []byte) {
	dir := GinkgoT().TempDir()
	raw := validationTelemetry()
	m := validationManifest(raw)
	m.FPS, m.WODDescription, m.AppearanceHints = 25, "Replay reference workout", "black shirt"
	m.ChunkSeconds = 1
	m.Movements = []string{"Power Clean"}
	m.Chunks = []ReplayChunk{
		{File: "chunks/chunk_0000.mp4", MediaStartSecs: 0, MediaEndSecs: 1, CaptureStartSecs: 0, CaptureEndSecs: 1, HeartRateBPM: 120},
		{File: "chunks/chunk_0001.mp4", MediaStartSecs: 1, MediaEndSecs: 2, CaptureStartSecs: 1, CaptureEndSecs: 2, HeartRateBPM: 125},
	}
	Expect(os.MkdirAll(filepath.Join(dir, "chunks"), 0700)).To(Succeed())
	var merged []byte
	if realMedia {
		for _, binary := range []string{"ffmpeg", "ffprobe"} {
			if _, err := exec.LookPath(binary); err != nil {
				Skip(binary + " is unavailable")
			}
		}
		source := filepath.Join(dir, "merged-reference.mp4")
		output, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=25", "-t", "2", "-an", "-c:v", "libx264", "-force_key_frames", "expr:gte(t,n_forced*1)", source).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		merged, err = os.ReadFile(source)
		Expect(err).NotTo(HaveOccurred())
		output, err = exec.Command("ffmpeg", "-v", "error", "-i", source, "-c", "copy", "-f", "segment", "-segment_time", "1", "-reset_timestamps", "1", filepath.Join(dir, "chunks", "chunk_%04d.mp4")).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
	}
	for i := range m.Chunks {
		filename := filepath.Join(dir, m.Chunks[i].File)
		data := []byte(fmt.Sprintf("chunk-%d", i))
		if realMedia {
			var err error
			data, err = os.ReadFile(filename)
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(os.WriteFile(filename, data, 0600)).To(Succeed())
		}
		m.Chunks[i].SHA256, m.Chunks[i].SizeBytes = digest(data), int64(len(data))
	}
	Expect(os.WriteFile(filepath.Join(dir, m.SensorFile), raw, 0600)).To(Succeed())
	Expect(writeJSON(filepath.Join(dir, "manifest.json"), m)).To(Succeed())
	return dir, m, merged
}

func runTestOptions(dir string, clock *runTestClock) RunOptions {
	return RunOptions{
		APIBase: runTestAPI + "/api/v1", Token: "private-test-token", FixtureDir: dir, OutputDir: GinkgoT().TempDir(),
		PollInterval: time.Second, SensorTimeout: 5 * time.Second, AnalysisTimeout: 5 * time.Second,
		Now: clock.current, Wait: clock.wait,
		NewSessionID: func(time.Time) string { return runTestSessionID },
		NewRequestID: func() string { return runTestRequestID },
	}
}

func runTestRows(m FixtureManifest) []db.ChunkAnalysisResult {
	rows := make([]db.ChunkAnalysisResult, 0, len(m.Chunks))
	for i, chunk := range m.Chunks {
		row := validationRow()
		row.ID, row.SessionID = uint(i+1), runTestSessionID
		row.FilePath = "gs://" + m.Bucket + "/videos/10/" + runTestSessionID + "/" + filepath.Base(chunk.File)
		row.HeartRateBPM = chunk.HeartRateBPM
		row.StartSecs, row.EndSecs = validationNumber(chunk.CaptureStartSecs), validationNumber(chunk.CaptureEndSecs)
		row.MediaStartSecs, row.MediaEndSecs = validationNumber(chunk.MediaStartSecs), validationNumber(chunk.MediaEndSecs)
		row.MovementObservations = db.NullableJSONDocument(`{"version":1,"duration_secs":1,"target_state":"identified","activity_state":"rest","events":[],"unassessed":[]}`)
		rows = append(rows, row)
	}
	return rows
}

func runTestPreflight(transport *testhelpers.MockTransport) {
	transport.New(runTestAPI).Get("/api/v1/profiles/10").Reply(200).JSON(map[string]any{"id": 10, "name": "Live test"})
	transport.New(runTestAPI).Get("/health").Reply(200).JSON(map[string]string{"commit": "release-under-test"})
}

func runTestPUT(transport *testhelpers.MockTransport, path string) {
	expectation := transport.New(runTestStorage).Post(path).Reply(200)
	expectation.Method = http.MethodPut
}

func runTestChunkUpload(transport *testhelpers.MockTransport, m FixtureManifest) {
	for i, chunk := range m.Chunks {
		transport.New(runTestAPI).Post("/api/v1/upload-url").MatchBodyContains(`"filename":"` + filepath.Base(chunk.File) + `"`).Reply(200).JSON(map[string]string{"upload_url": fmt.Sprintf("%s/chunk-%d?signature=private-signature", runTestStorage, i), "gcs_uri": "gs://" + m.Bucket + "/videos/10/" + runTestSessionID + "/" + filepath.Base(chunk.File)})
		runTestPUT(transport, fmt.Sprintf("/chunk-%d", i))
		transport.New(runTestAPI).Post("/api/v1/chunk-complete").MatchBodyContains(`"gcs_uri":"gs://` + m.Bucket + `/videos/10/` + runTestSessionID + `/` + filepath.Base(chunk.File) + `"`).Reply(202).JSON(map[string]any{"session_id": runTestSessionID, "task_id": fmt.Sprintf("chunk-%d", i)})
	}
	// One observation occurs between the two fixed capture deadlines.
	transport.New(runTestAPI).Get("/api/v1/chunk-analysis/" + runTestSessionID).Reply(200).JSON(runTestRows(m)[:1])
}

func runTestSensorUpload(transport *testhelpers.MockTransport) {
	prefix := "/api/v1/sessions/" + runTestSessionID
	transport.New(runTestAPI).Post(prefix + "/sensor-upload").MatchBodyContains(`"request_id":"` + runTestRequestID + `"`).Reply(200).JSON(map[string]any{"request_id": runTestRequestID, "version": "1", "state": "UPLOADING", "object_name": "videos/10/" + runTestSessionID + "/sensor_telemetry_v1_" + runTestRequestID + ".ndjson", "upload_url": runTestStorage + "/sensor?signature=private-signature", "required_headers": map[string]string{"Content-Type": "application/x-ndjson", "X-Goog-Meta-Test": "required"}})
	runTestPUT(transport, "/sensor")
	transport.New(runTestAPI).Post(prefix + "/sensor-complete").Reply(202).JSON(map[string]any{"accepted": true, "request_id": runTestRequestID, "version": "1", "state": "PENDING"})
}

func runTestReport(opts RunOptions) (RunReport, []byte) {
	data, err := os.ReadFile(filepath.Join(opts.OutputDir, runTestSessionID, "report.json"))
	Expect(err).NotTo(HaveOccurred())
	var report RunReport
	Expect(json.Unmarshal(data, &report)).To(Succeed())
	return report, data
}

func runTestRequestIndex(requests []testhelpers.CapturedRequest, method, path string) int {
	for i, request := range requests {
		u, err := url.Parse(request.URL)
		Expect(err).NotTo(HaveOccurred())
		if request.Method == method && u.Path == path {
			return i
		}
	}
	return -1
}

var _ = Describe("Live replay execution", func() {
	It("applies the stage deadline to requests and rejects success arriving after it", func() {
		clock := &runTestClock{now: time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)}
		run := &replayRun{opts: RunOptions{Now: clock.current, Wait: clock.wait, PollInterval: time.Second}, report: &RunReport{}, dir: GinkgoT().TempDir()}
		err := run.poll(context.Background(), 5*time.Second, "sensor processing", func(ctx context.Context) (bool, error) {
			_, hasDeadline := ctx.Deadline()
			Expect(hasDeadline).To(BeTrue())
			clock.now = clock.now.Add(6 * time.Second)
			return true, nil
		})
		Expect(err).To(MatchError(ContainSubstring("sensor processing timed out")))
	})
	It("replays capture deadlines through real HTTP clients and retains a validated merged result", func() {
		dir, manifest, merged := runTestFixture(true)
		clock := &runTestClock{now: time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)}
		opts := runTestOptions(dir, clock)
		transport := testhelpers.NewMockTransport()
		runTestPreflight(transport)
		runTestChunkUpload(transport, manifest)
		runTestSensorUpload(transport)
		rows := runTestRows(manifest)
		prefix := "/api/v1/sessions/" + runTestSessionID
		summary, timeline := validationSensor(validationTelemetry())
		summary.RequestID, timeline.Source.RequestID = runTestRequestID, runTestRequestID
		summaryJSON, err := json.Marshal(summary)
		Expect(err).NotTo(HaveOccurred())
		analysis := db.AnalysisResult{SessionID: runTestSessionID, ProfileID: 10, AnalysisType: "wod", Status: "PENDING", SensorSummary: db.JSONDocument(summaryJSON), SensorState: "COMPLETED", SensorVersion: 1}
		transport.New(runTestAPI).Get("/api/v1/chunk-analysis/" + runTestSessionID).Reply(200).JSON(rows)
		transport.New(runTestAPI).Get(prefix + "/sensor-status?profile_id=10").Reply(200).JSON(map[string]string{"request_id": runTestRequestID, "version": "1", "state": "COMPLETED"})
		beforeMerge := timelineResponse{Status: "completed", Timeline: timeline}
		transport.New(runTestAPI).Get(prefix + "/sensor-timeline?profile_id=10").Reply(200).JSON(beforeMerge)
		transport.New(runTestAPI).Get("/api/v1/analysis/" + runTestSessionID).Reply(200).JSON([]db.AnalysisResult{analysis})
		transport.New(runTestAPI).Post("/api/v1/merge-chunks").Reply(202).JSON(map[string]string{"session_id": runTestSessionID, "task_id": "merge"})
		transport.New(runTestAPI).Get("/api/v1/chunk-analysis/" + runTestSessionID).Reply(200).JSON(rows)
		analysis.Status, analysis.Output = "COMPLETED", "Observed workout summary"
		transport.New(runTestAPI).Get("/api/v1/analysis/" + runTestSessionID).Reply(200).JSON([]db.AnalysisResult{analysis})
		activitySummary := activity.Summary{Version: activity.Version, ReviewVersion: activity.Version, Available: true, ReviewState: "completed", CoverageScope: "recorded_chunks", MediaGeneration: "42", SourceVersion: activity.SourceVersion(rows), Reviews: []activity.ReviewedChunk{{ChunkID: 1, State: "completed"}, {ChunkID: 2, State: "completed"}}}
		transport.New(runTestAPI).Get(prefix + "/activity-summary?profile_id=10").Reply(200).JSON(activitySummary)
		afterMerge := beforeMerge
		afterMerge.VideoMapping.Segments = []MappingSegment{{MediaStartMs: 0, MediaEndMs: 1000, CaptureStartMs: 0, CaptureEndMs: 1000}, {MediaStartMs: 1000, MediaEndMs: 2000, CaptureStartMs: 1000, CaptureEndMs: 2000}}
		transport.New(runTestAPI).Get(prefix + "/sensor-timeline?profile_id=10").Reply(200).JSON(afterMerge)
		transport.New(runTestAPI).Get("/api/v1/video-download/" + runTestSessionID + "?profile_id=10&kind=merged").Reply(200).JSON(map[string]string{"session_id": runTestSessionID, "kind": "merged", "download_url": runTestStorage + "/merged?signature=private-signature"})
		transport.New(runTestStorage).Get("/merged").Reply(200).Body(merged)
		transport.New(runTestAPI).Get("/api/v1/profiles/10").Reply(200).JSON(map[string]any{"id": 10, "name": "Live test"})
		var output bytes.Buffer
		Expect(runReplay(context.Background(), opts, &http.Client{Transport: transport}, &output)).To(Succeed())
		Expect(output.String()).To(ContainSubstring("Fixture: " + manifest.SourceSessionID + " (2.0s media / 2.0s capture, 2 chunks)"))
		Expect(transport.Verify()).To(Succeed())
		report, data := runTestReport(opts)
		Expect(report.Status).To(Equal("passed"))
		Expect(report.APICommit).To(Equal("release-under-test"))
		Expect(report.MergedDurationSecs).To(BeNumerically("~", 2, 0.001))
		Expect(report.Chunks).To(HaveLen(2))
		Expect(report.Chunks[0].SubmittedAt.Sub(report.StartedAt)).To(Equal(time.Second))
		Expect(report.Chunks[1].SubmittedAt.Sub(report.StartedAt)).To(Equal(2 * time.Second))
		Expect(report.SensorUpload.RequestID).To(Equal(runTestRequestID))
		Expect(report.SensorUpload.Version).To(Equal("1"))
		Expect(report.QualityReview).To(ContainSubstring("Manual review required"))
		Expect(string(data)).NotTo(ContainSubstring("private-test-token"))
		Expect(string(data)).NotTo(ContainSubstring("private-signature"))
		requests := transport.Requests()
		lastComplete := -1
		chunkIndex := 0
		for i, request := range requests {
			u, err := url.Parse(request.URL)
			Expect(err).NotTo(HaveOccurred())
			if u.Host == "storage.test" {
				Expect(request.Headers.Get("Authorization")).To(BeEmpty())
			}
			if request.Method == http.MethodPut {
				if u.Path == "/sensor" {
					Expect(request.Headers.Get("X-Goog-Meta-Test")).To(Equal("required"))
				}
			}
			if u.Path == "/api/v1/chunk-complete" {
				var body map[string]any
				Expect(json.Unmarshal(request.Body, &body)).To(Succeed())
				Expect(body["live_analysis_version"]).To(Equal(float64(1)))
				Expect(body["profile_id"]).To(Equal(float64(10)))
				Expect(body["start_secs"]).To(Equal(float64(chunkIndex)))
				Expect(body["end_secs"]).To(Equal(float64(chunkIndex + 1)))
				Expect(body["heart_rate_bpm"]).To(Equal(float64(manifest.Chunks[chunkIndex].HeartRateBPM)))
				putIndex := runTestRequestIndex(requests, http.MethodPut, fmt.Sprintf("/chunk-%d", chunkIndex))
				Expect(putIndex).To(BeNumerically("<", i))
				lastComplete, chunkIndex = i, chunkIndex+1
			}
		}
		sensorPrepare := runTestRequestIndex(requests, http.MethodPost, prefix+"/sensor-upload")
		sensorComplete := runTestRequestIndex(requests, http.MethodPost, prefix+"/sensor-complete")
		sensorTimeline := runTestRequestIndex(requests, http.MethodGet, prefix+"/sensor-timeline")
		mergeIndex := runTestRequestIndex(requests, http.MethodPost, "/api/v1/merge-chunks")
		Expect(lastComplete).To(BeNumerically("<", sensorPrepare))
		Expect(sensorPrepare).To(BeNumerically("<", runTestRequestIndex(requests, http.MethodPut, "/sensor")))
		Expect(runTestRequestIndex(requests, http.MethodPut, "/sensor")).To(BeNumerically("<", sensorComplete))
		Expect(sensorComplete).To(BeNumerically("<", sensorTimeline))
		Expect(sensorTimeline).To(BeNumerically("<", mergeIndex))
	})

	DescribeTable("stops before uploading when profile access is rejected",
		func(status int) {
			dir, _, _ := runTestFixture(false)
			clock := &runTestClock{now: time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)}
			opts := runTestOptions(dir, clock)
			transport := testhelpers.NewMockTransport()
			transport.New(runTestAPI).Get("/api/v1/profiles/10").Reply(status).JSON(map[string]string{"error": "access denied"})
			err := runReplay(context.Background(), opts, &http.Client{Transport: transport}, io.Discard)
			Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf("HTTP %d", status))))
			Expect(transport.Verify()).To(Succeed())
			for _, request := range transport.Requests() {
				Expect(request.Method).To(Equal(http.MethodGet))
			}
			report, _ := runTestReport(opts)
			Expect(report.Status).To(Equal("failed"))
			Expect(report.Stages[len(report.Stages)-1].Name).To(Equal("authenticate"))
		},
		Entry("expired token", http.StatusUnauthorized),
		Entry("different profile owner", http.StatusForbidden),
	)

	It("retains sensor upload identity and report when processing fails without submitting merge", func() {
		dir, manifest, _ := runTestFixture(false)
		clock := &runTestClock{now: time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)}
		opts := runTestOptions(dir, clock)
		transport := testhelpers.NewMockTransport()
		runTestPreflight(transport)
		runTestChunkUpload(transport, manifest)
		runTestSensorUpload(transport)
		transport.New(runTestAPI).Get("/api/v1/chunk-analysis/" + runTestSessionID).Reply(200).JSON(runTestRows(manifest))
		transport.New(runTestAPI).Get("/api/v1/sessions/" + runTestSessionID + "/sensor-status?profile_id=10").Reply(200).JSON(map[string]string{"request_id": runTestRequestID, "version": "1", "state": "FAILED", "last_error_code": "QUALITY_CHECK_FAILED"})
		err := runReplay(context.Background(), opts, &http.Client{Transport: transport}, io.Discard)
		Expect(err).To(MatchError(ContainSubstring("QUALITY_CHECK_FAILED")))
		Expect(transport.Verify()).To(Succeed())
		Expect(runTestRequestIndex(transport.Requests(), http.MethodPost, "/api/v1/merge-chunks")).To(Equal(-1))
		report, _ := runTestReport(opts)
		Expect(report.Status).To(Equal("failed"))
		Expect(report.SensorUpload.RequestID).To(Equal(runTestRequestID))
		Expect(report.SensorUpload.Version).To(Equal("1"))
		Expect(report.Stages[len(report.Stages)-1].Name).To(Equal("sensor"))
		raw, err := os.ReadFile(filepath.Join(opts.OutputDir, runTestSessionID, "sensor.ndjson"))
		Expect(err).NotTo(HaveOccurred())
		Expect(digest(raw)).To(Equal(report.SensorUpload.SHA256))
	})

	It("bounds pending sensor processing by its virtual deadline and preserves failed evidence", func() {
		dir, manifest, _ := runTestFixture(false)
		clock := &runTestClock{now: time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)}
		opts := runTestOptions(dir, clock)
		opts.SensorTimeout = 2 * time.Second
		transport := testhelpers.NewMockTransport()
		runTestPreflight(transport)
		runTestChunkUpload(transport, manifest)
		runTestSensorUpload(transport)
		for range 2 {
			transport.New(runTestAPI).Get("/api/v1/chunk-analysis/" + runTestSessionID).Reply(200).JSON(runTestRows(manifest))
			transport.New(runTestAPI).Get("/api/v1/sessions/" + runTestSessionID + "/sensor-status?profile_id=10").Reply(200).JSON(map[string]string{"request_id": runTestRequestID, "version": "1", "state": "RUNNING"})
		}
		err := runReplay(context.Background(), opts, &http.Client{Transport: transport}, io.Discard)
		Expect(err).To(MatchError(ContainSubstring("sensor processing timed out")))
		Expect(transport.Verify()).To(Succeed())
		report, _ := runTestReport(opts)
		Expect(report.EndedAt.Sub(report.StartedAt)).To(Equal(4 * time.Second))
		Expect(report.Status).To(Equal("failed"))
		Expect(runTestRequestIndex(transport.Requests(), http.MethodPost, "/api/v1/merge-chunks")).To(Equal(-1))
	})

	It("records a late submission while preserving the following capture deadline", func() {
		dir, manifest, _ := runTestFixture(false)
		start := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)
		clock := &runTestClock{now: start}
		opts := runTestOptions(dir, clock)
		late := true
		opts.Wait = func(ctx context.Context, delay time.Duration) error {
			if late {
				delay += 250 * time.Millisecond
				late = false
			}
			return clock.wait(ctx, delay)
		}
		transport := testhelpers.NewMockTransport()
		runTestChunkUpload(transport, manifest)
		client, err := newReplayClient(opts.APIBase, opts.Token, &http.Client{Transport: transport})
		Expect(err).NotTo(HaveOccurred())
		report := &RunReport{SessionID: runTestSessionID, Fixture: manifest, Chunks: []ChunkProgress{}}
		run := &replayRun{opts: opts, client: client, report: report, dir: GinkgoT().TempDir(), out: io.Discard}
		Expect(run.uploadChunks(context.Background(), start)).To(Succeed())
		Expect(transport.Verify()).To(Succeed())
		Expect(report.Chunks[0].SubmissionDelayMs).To(Equal(int64(250)))
		Expect(report.Chunks[0].ScheduledAt.Sub(start)).To(Equal(time.Second))
		Expect(report.Chunks[1].SubmissionDelayMs).To(Equal(int64(0)))
		Expect(report.Chunks[1].SubmittedAt.Sub(start)).To(Equal(2 * time.Second))
		Expect(strings.TrimSpace(report.SessionID)).To(Equal(runTestSessionID))
	})
})
