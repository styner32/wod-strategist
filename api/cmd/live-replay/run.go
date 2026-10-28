package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
)

type RunOptions struct {
	APIBase, Token, Username, Password, WebURL   string
	FixtureDir, OutputDir                        string
	PollInterval, SensorTimeout, AnalysisTimeout time.Duration
	// Concrete clock functions keep scheduling specs fast without changing run speed.
	Now          func() time.Time
	Wait         func(context.Context, time.Duration) error
	NewSessionID func(time.Time) string
	NewRequestID func() string
}

type StageResult struct {
	Name      string    `json:"name"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
}

type ChunkProgress struct {
	File              string     `json:"file"`
	ScheduledAt       time.Time  `json:"scheduled_at"`
	SubmittedAt       time.Time  `json:"submitted_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	SubmissionDelayMs int64      `json:"submission_delay_ms"`
	HeartRateBPM      int        `json:"heart_rate_bpm"`
}

type SensorUploadIdentity struct {
	RequestID string `json:"request_id"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type RunReport struct {
	Version            int                         `json:"version"`
	Status             string                      `json:"status"`
	Error              string                      `json:"error,omitempty"`
	SessionID          string                      `json:"session_id"`
	ProfileID          uint                        `json:"profile_id"`
	APIBase            string                      `json:"api_base"`
	APICommit          string                      `json:"api_commit"`
	SessionURL         string                      `json:"session_url"`
	StartedAt          time.Time                   `json:"started_at"`
	EndedAt            time.Time                   `json:"ended_at"`
	FixtureSHA256      string                      `json:"fixture_sha256"`
	Fixture            FixtureManifest             `json:"fixture"`
	Stages             []StageResult               `json:"stages"`
	Chunks             []ChunkProgress             `json:"chunks"`
	ProfileBefore      json.RawMessage             `json:"profile_before,omitempty"`
	ProfileAfter       json.RawMessage             `json:"profile_after,omitempty"`
	SensorUpload       SensorUploadIdentity        `json:"sensor_upload"`
	SensorSummary      *sensor.SensorSummaryResult `json:"sensor_summary,omitempty"`
	SensorTimeline     *sensor.SensorTimelineData  `json:"sensor_timeline,omitempty"`
	Activity           *activity.Summary           `json:"activity,omitempty"`
	Analysis           *db.AnalysisResult          `json:"analysis,omitempty"`
	ChunkResults       []db.ChunkAnalysisResult    `json:"chunk_results,omitempty"`
	MergedDurationSecs float64                     `json:"merged_duration_secs,omitempty"`
	QualityReview      string                      `json:"quality_review"`
}

type timelineResponse struct {
	Status       string                     `json:"status"`
	Reason       string                     `json:"reason"`
	Timeline     *sensor.SensorTimelineData `json:"timeline"`
	VideoMapping struct {
		Segments []MappingSegment `json:"segments"`
	} `json:"video_mapping"`
}

type replayRun struct {
	opts   RunOptions
	client *replayClient
	report *RunReport
	dir    string
	out    io.Writer
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runReplay(ctx context.Context, opts RunOptions, client *http.Client, out io.Writer) (runErr error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Wait == nil {
		opts.Wait = waitContext
	}
	if opts.PollInterval <= 0 || opts.SensorTimeout <= 0 || opts.AnalysisTimeout <= 0 {
		return fmt.Errorf("poll and processing timeouts must be positive")
	}
	api, err := newReplayClient(opts.APIBase, opts.Token, client)
	if err != nil {
		return err
	}
	if out == nil {
		out = io.Discard
	}
	now := opts.Now()
	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		return err
	}
	sid := "WOD-" + now.In(location).Format("20060102") + "-" + ulid.Make().String()
	if opts.NewSessionID != nil {
		sid = opts.NewSessionID(now)
	}
	dir := filepath.Join(opts.OutputDir, sid)
	if err = os.MkdirAll(opts.OutputDir, 0700); err != nil {
		return err
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return err
	}
	report := &RunReport{Version: 1, Status: "running", SessionID: sid, ProfileID: targetProfileID, APIBase: api.base, StartedAt: now, Stages: []StageResult{}, Chunks: []ChunkProgress{}, QualityReview: "Manual review required: workout, repetitions and AI content are not automatically graded."}
	report.SessionURL = api.base + "/sessions/" + sid + "/analysis"
	if opts.WebURL != "" {
		u, parseErr := url.Parse(opts.WebURL)
		if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("LIVE_TEST_WEB_URL must be an HTTPS web origin")
		}
		report.SessionURL = strings.TrimRight(opts.WebURL, "/") + "/sessions/" + sid + "?profile_id=10"
	}
	r := &replayRun{opts: opts, client: api, report: report, dir: dir, out: out}
	defer func() {
		report.EndedAt = opts.Now()
		report.Status = "passed"
		if runErr != nil {
			report.Status = "failed"
			report.Error = r.safeError(runErr)
		}
		if err := writeJSON(filepath.Join(dir, "report.json"), report); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("save report: %w", err))
		}
		fmt.Fprintf(out, "%s — %s\nReport: %s\nSession: %s\n", strings.ToUpper(report.Status), sid, filepath.Join(dir, "report.json"), report.SessionURL)
	}()
	fmt.Fprintf(out, "Live replay: profile 10, new session %s\nResults are retained. Input replay follows recorded capture timing; analysis continues afterwards.\n", sid)
	var raw []byte
	if err = r.stage("fixture", func() error {
		if _, err := exec.LookPath("ffprobe"); err != nil {
			return fmt.Errorf("ffprobe is required to verify the merged video")
		}
		data, err := os.ReadFile(filepath.Join(opts.FixtureDir, "manifest.json"))
		if err != nil {
			return fmt.Errorf("load fixture; run make live-test-prepare first: %w", err)
		}
		report.FixtureSHA256 = digest(data)
		if err = json.Unmarshal(data, &report.Fixture); err != nil {
			return err
		}
		raw, err = validateFixture(opts.FixtureDir, report.Fixture)
		return err
	}); err != nil {
		return err
	}
	fmt.Fprintf(out, "Fixture: %s (%.1fs media / %.1fs capture, %d chunks)\n", report.Fixture.SourceSessionID, report.Fixture.MediaEndSecs-report.Fixture.MediaStartSecs, report.Fixture.Stats.DurationMs/1000, len(report.Fixture.Chunks))
	if err = r.stage("authenticate", func() error {
		if api.token == "" {
			if opts.Username == "" || opts.Password == "" {
				return fmt.Errorf("set LIVE_TEST_JWT or LIVE_TEST_USERNAME and LIVE_TEST_PASSWORD in .env.live.local")
			}
			var login struct {
				Token string `json:"token"`
			}
			if err := api.json(ctx, http.MethodPost, "/auth/login", map[string]string{"username": opts.Username, "password": opts.Password}, &login); err != nil {
				return err
			}
			if login.Token == "" {
				return fmt.Errorf("login returned no token")
			}
			api.token = login.Token
		}
		if err := api.json(ctx, http.MethodGet, "/profiles/10", nil, &report.ProfileBefore); err != nil {
			return err
		}
		var p struct {
			ID         uint       `json:"id"`
			ArchivedAt *time.Time `json:"archived_at"`
		}
		if err := json.Unmarshal(report.ProfileBefore, &p); err != nil {
			return err
		}
		if p.ID != targetProfileID || p.ArchivedAt != nil {
			return fmt.Errorf("profile 10 missing or archived")
		}
		var health struct {
			Commit string `json:"commit"`
		}
		// /health is outside the API prefix.
		healthClient := *api
		healthClient.base = strings.TrimSuffix(api.base, "/api/v1")
		if err := healthClient.json(ctx, http.MethodGet, "/health", nil, &health); err != nil {
			return err
		}
		report.APICommit = health.Commit
		return nil
	}); err != nil {
		return err
	}
	baseEpoch := opts.Now().UnixMilli()
	raw, err = reidentifyTelemetry(raw, sid, targetProfileID, baseEpoch)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "sensor.ndjson"), raw, 0600); err != nil {
		return err
	}
	if err = r.stage("replay_chunks", func() error { return r.uploadChunks(ctx, opts.Now()) }); err != nil {
		return err
	}
	if err = r.stage("sensor", func() error { return r.uploadAndWaitSensor(ctx, raw) }); err != nil {
		return err
	}
	if err = r.stage("merge", func() error {
		return api.json(ctx, http.MethodPost, "/merge-chunks", r.workoutBody(), nil)
	}); err != nil {
		return err
	}
	if err = r.stage("analysis", func() error { return r.waitAnalysis(ctx, raw) }); err != nil {
		return err
	}
	if err = r.stage("merged_video", func() error { return r.checkMergedVideo(ctx) }); err != nil {
		return err
	}
	if err = r.stage("profile_after", func() error { return api.json(ctx, http.MethodGet, "/profiles/10", nil, &report.ProfileAfter) }); err != nil {
		return err
	}
	return nil
}

func (r *replayRun) safeError(err error) string {
	msg := redact(err.Error())
	for _, secret := range []string{r.opts.Token, r.opts.Password, r.client.token} {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "[REDACTED]")
		}
	}
	return msg
}

func (r *replayRun) stage(name string, work func() error) error {
	entry := StageResult{Name: name, StartedAt: r.opts.Now(), Status: "running"}
	r.report.Stages = append(r.report.Stages, entry)
	i := len(r.report.Stages) - 1
	if err := writeJSON(filepath.Join(r.dir, "report.json"), r.report); err != nil {
		return err
	}
	fmt.Fprintf(r.out, "[%s] started\n", name)
	err := work()
	r.report.Stages[i].EndedAt = r.opts.Now()
	r.report.Stages[i].Status = "passed"
	if err != nil {
		r.report.Stages[i].Status = "failed"
		r.report.Stages[i].Error = r.safeError(err)
	}
	fmt.Fprintf(r.out, "[%s] %s\n", name, r.report.Stages[i].Status)
	return err
}

func (r *replayRun) workoutBody() map[string]any {
	m := r.report.Fixture
	return map[string]any{"session_id": r.report.SessionID, "profile_id": targetProfileID, "workout_type": "wod", "movements": m.Movements, "injuries": []string{}, "wod_description": m.WODDescription, "appearance_hints": m.AppearanceHints}
}

func (r *replayRun) uploadChunks(ctx context.Context, start time.Time) error {
	for _, chunk := range r.report.Fixture.Chunks {
		due := start.Add(time.Duration(chunk.CaptureEndSecs * float64(time.Second)))
		for r.opts.Now().Before(due) {
			delay := due.Sub(r.opts.Now())
			if delay > r.opts.PollInterval {
				delay = r.opts.PollInterval
			}
			if err := r.opts.Wait(ctx, delay); err != nil {
				return err
			}
			if len(r.report.Chunks) > 0 {
				if err := r.observeChunks(ctx, false); err != nil {
					return err
				}
			}
		}
		progress := ChunkProgress{File: chunk.File, ScheduledAt: due, HeartRateBPM: chunk.HeartRateBPM}
		r.report.Chunks = append(r.report.Chunks, progress)
		i := len(r.report.Chunks) - 1
		var upload struct {
			URL    string `json:"upload_url"`
			GCSURI string `json:"gcs_uri"`
		}
		if err := r.client.json(ctx, http.MethodPost, "/upload-url", map[string]any{"session_id": r.report.SessionID, "profile_id": targetProfileID, "filename": filepath.Base(chunk.File)}, &upload); err != nil {
			return err
		}
		wantURI := "gs://" + r.report.Fixture.Bucket + "/videos/10/" + r.report.SessionID + "/" + filepath.Base(chunk.File)
		if upload.GCSURI != wantURI {
			return fmt.Errorf("upload URL returned unexpected object path")
		}
		data, err := checkedFile(r.opts.FixtureDir, chunk.File, chunk.SHA256, chunk.SizeBytes)
		if err != nil {
			return err
		}
		if err = r.client.put(ctx, upload.URL, data, map[string]string{"Content-Type": "video/mp4"}); err != nil {
			return err
		}
		body := r.workoutBody()
		body["gcs_uri"] = upload.GCSURI
		body["live_analysis_version"] = 1
		body["start_secs"], body["end_secs"] = chunk.CaptureStartSecs, chunk.CaptureEndSecs
		body["heart_rate_bpm"] = chunk.HeartRateBPM
		if err = r.client.json(ctx, http.MethodPost, "/chunk-complete", body, nil); err != nil {
			return err
		}
		r.report.Chunks[i].SubmittedAt = r.opts.Now()
		r.report.Chunks[i].SubmissionDelayMs = r.opts.Now().Sub(due).Milliseconds()
		fmt.Fprintf(r.out, "  chunk %d/%d submitted, peak HR %d, delay %dms\n", i+1, len(r.report.Fixture.Chunks), chunk.HeartRateBPM, r.report.Chunks[i].SubmissionDelayMs)
		if err = writeJSON(filepath.Join(r.dir, "report.json"), r.report); err != nil {
			return err
		}
	}
	return nil
}

func (r *replayRun) observeChunks(ctx context.Context, requireMedia bool) error {
	var rows []db.ChunkAnalysisResult
	if err := r.client.json(ctx, http.MethodGet, "/chunk-analysis/"+r.report.SessionID, nil, &rows); err != nil {
		return err
	}
	r.report.ChunkResults = rows
	if _, err := validateChunkResults(r.report.Fixture, rows, r.report.SessionID, requireMedia); err != nil {
		return err
	}
	for i := range r.report.Chunks {
		if r.report.Chunks[i].CompletedAt != nil {
			continue
		}
		for _, row := range rows {
			if filepath.Base(row.FilePath) == filepath.Base(r.report.Chunks[i].File) && row.Status == "COMPLETED" {
				t := r.opts.Now()
				r.report.Chunks[i].CompletedAt = &t
			}
		}
	}
	return nil
}

func (r *replayRun) uploadAndWaitSensor(ctx context.Context, raw []byte) error {
	identity := SensorUploadIdentity{RequestID: uuid.NewString(), SHA256: digest(raw), SizeBytes: int64(len(raw))}
	if r.opts.NewRequestID != nil {
		identity.RequestID = r.opts.NewRequestID()
	}
	r.report.SensorUpload = identity
	var prepared struct {
		RequestID  string            `json:"request_id"`
		Version    string            `json:"version"`
		State      string            `json:"state"`
		ObjectName string            `json:"object_name"`
		URL        string            `json:"upload_url"`
		Headers    map[string]string `json:"required_headers"`
	}
	sessionPath := "/sessions/" + r.report.SessionID
	if err := r.client.json(ctx, http.MethodPost, sessionPath+"/sensor-upload", map[string]any{"profile_id": targetProfileID, "calculation_version": 3, "request_id": identity.RequestID, "expected_version": "0", "size_bytes": identity.SizeBytes, "sha256": identity.SHA256}, &prepared); err != nil {
		return err
	}
	if prepared.RequestID != identity.RequestID || prepared.Version != "1" || prepared.URL == "" || !strings.HasPrefix(prepared.ObjectName, "videos/10/"+r.report.SessionID+"/sensor_telemetry_v") {
		return fmt.Errorf("unexpected sensor upload identity")
	}
	r.report.SensorUpload.Version = prepared.Version
	if err := r.client.put(ctx, prepared.URL, raw, prepared.Headers); err != nil {
		return err
	}
	var completed struct {
		Accepted  bool   `json:"accepted"`
		RequestID string `json:"request_id"`
		Version   string `json:"version"`
		State     string `json:"state"`
	}
	if err := r.client.json(ctx, http.MethodPost, sessionPath+"/sensor-complete", map[string]any{"profile_id": targetProfileID, "request_id": identity.RequestID, "version": prepared.Version}, &completed); err != nil {
		return err
	}
	if !completed.Accepted || completed.RequestID != identity.RequestID || completed.Version != prepared.Version {
		return fmt.Errorf("sensor completion was not accepted for this upload")
	}
	return r.poll(ctx, r.opts.SensorTimeout, "sensor processing", func(ctx context.Context) (bool, error) {
		if err := r.observeChunks(ctx, false); err != nil {
			return false, err
		}
		var status struct {
			State         string  `json:"state"`
			RequestID     string  `json:"request_id"`
			Version       string  `json:"version"`
			LastErrorCode *string `json:"last_error_code"`
		}
		if err := r.client.json(ctx, http.MethodGet, sessionPath+"/sensor-status?profile_id=10", nil, &status); err != nil {
			return false, err
		}
		if status.RequestID != identity.RequestID || status.Version != prepared.Version {
			return false, fmt.Errorf("sensor processing identity changed")
		}
		if status.State == "FAILED" || status.State == "EXPIRED" {
			code := "unspecified"
			if status.LastErrorCode != nil {
				code = *status.LastErrorCode
			}
			return false, fmt.Errorf("sensor state %s (code %s)", status.State, code)
		}
		if status.State != "COMPLETED" {
			return false, nil
		}
		var tl timelineResponse
		if err := r.client.json(ctx, http.MethodGet, sessionPath+"/sensor-timeline?profile_id=10", nil, &tl); err != nil {
			return false, err
		}
		if tl.Status != "completed" && tl.Status != "limited" {
			return false, fmt.Errorf("sensor completed without usable timeline: %s %s", tl.Status, tl.Reason)
		}
		var results []db.AnalysisResult
		if err := r.client.json(ctx, http.MethodGet, "/analysis/"+r.report.SessionID, nil, &results); err != nil {
			return false, err
		}
		analysis, err := mainAnalysis(results, r.report.SessionID)
		if err != nil {
			return false, err
		}
		if analysis == nil {
			return false, nil
		}
		var summary sensor.SensorSummaryResult
		if err = json.Unmarshal(analysis.SensorSummary, &summary); err != nil {
			return false, err
		}
		if err = validateSensorResult(r.report.Fixture, raw, summary, tl.Timeline, identity.RequestID, prepared.Version); err != nil {
			return false, err
		}
		r.report.SensorSummary = &summary
		r.report.SensorTimeline = tl.Timeline
		return true, nil
	})
}

func mainAnalysis(results []db.AnalysisResult, sid string) (*db.AnalysisResult, error) {
	var found *db.AnalysisResult
	for i := range results {
		a := &results[i]
		if a.SessionID != sid || a.ProfileID != targetProfileID {
			return nil, fmt.Errorf("analysis returned different profile/session")
		}
		if a.AnalysisType != "wod" {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("multiple WOD analysis rows")
		}
		found = a
	}
	return found, nil
}

func (r *replayRun) waitAnalysis(ctx context.Context, raw []byte) error {
	return r.poll(ctx, r.opts.AnalysisTimeout, "final analysis", func(ctx context.Context) (bool, error) {
		if err := r.observeChunks(ctx, true); err != nil {
			return false, err
		}
		chunksReady, err := validateChunkResults(r.report.Fixture, r.report.ChunkResults, r.report.SessionID, true)
		if err != nil {
			return false, err
		}
		var results []db.AnalysisResult
		if err = r.client.json(ctx, http.MethodGet, "/analysis/"+r.report.SessionID, nil, &results); err != nil {
			return false, err
		}
		a, err := mainAnalysis(results, r.report.SessionID)
		if err != nil {
			return false, err
		}
		if a == nil {
			return false, nil
		}
		r.report.Analysis = a
		if a.Status == "FAILED" {
			return false, fmt.Errorf("final video analysis failed")
		}
		if a.Status != "COMPLETED" || !chunksReady {
			return false, nil
		}
		if strings.TrimSpace(a.Output) == "" {
			return false, fmt.Errorf("final analysis output is empty")
		}
		var summary activity.Summary
		if err = r.client.json(ctx, http.MethodGet, "/sessions/"+r.report.SessionID+"/activity-summary?profile_id=10", nil, &summary); err != nil {
			return false, err
		}
		r.report.Activity = &summary
		ready, err := validateActivityResult(r.report.ChunkResults, summary)
		if err != nil || !ready {
			return false, err
		}
		var tl timelineResponse
		if err = r.client.json(ctx, http.MethodGet, "/sessions/"+r.report.SessionID+"/sensor-timeline?profile_id=10", nil, &tl); err != nil {
			return false, err
		}
		if tl.Status != "completed" && tl.Status != "limited" {
			return false, fmt.Errorf("sensor timeline lost after merge")
		}
		var sensorSummary sensor.SensorSummaryResult
		if err = json.Unmarshal(a.SensorSummary, &sensorSummary); err != nil {
			return false, err
		}
		if err = validateSensorResult(r.report.Fixture, raw, sensorSummary, tl.Timeline, r.report.SensorUpload.RequestID, r.report.SensorUpload.Version); err != nil {
			return false, err
		}
		if err = validateTimelineMapping(r.report.ChunkResults, tl.VideoMapping.Segments); err != nil {
			return false, err
		}
		r.report.SensorSummary = &sensorSummary
		r.report.SensorTimeline = tl.Timeline
		return true, nil
	})
}

func (r *replayRun) poll(ctx context.Context, timeout time.Duration, description string, check func(context.Context) (bool, error)) error {
	stageCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline := r.opts.Now().Add(timeout)
	timedOut := func() error { return fmt.Errorf("%s timed out after %s", description, timeout) }
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !r.opts.Now().Before(deadline) || stageCtx.Err() != nil {
			return timedOut()
		}
		ready, err := check(stageCtx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !r.opts.Now().Before(deadline) || stageCtx.Err() != nil {
			return timedOut()
		}
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if err = writeJSON(filepath.Join(r.dir, "report.json"), r.report); err != nil {
			return err
		}
		delay := deadline.Sub(r.opts.Now())
		if delay > r.opts.PollInterval {
			delay = r.opts.PollInterval
		}
		if err = r.opts.Wait(stageCtx, delay); err != nil {
			if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				return timedOut()
			}
			return err
		}
	}
}

func (r *replayRun) checkMergedVideo(ctx context.Context) error {
	var download struct {
		URL       string `json:"download_url"`
		SessionID string `json:"session_id"`
		Kind      string `json:"kind"`
	}
	if err := r.client.json(ctx, http.MethodGet, "/video-download/"+r.report.SessionID+"?profile_id=10&kind=merged", nil, &download); err != nil {
		return err
	}
	if download.SessionID != r.report.SessionID || download.Kind != "merged" {
		return fmt.Errorf("merged video identity mismatch")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, download.URL, nil)
	if err != nil {
		return fmt.Errorf("invalid merged download URL")
	}
	resp, err := r.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("download merged video: %s", requestFailure(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("merged download returned HTTP %d", resp.StatusCode)
	}
	filename := filepath.Join(r.dir, "merged.mp4")
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 1<<30))
	closeErr := f.Close()
	if err = errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	duration, _, err := probeVideo(ctx, filename)
	if err != nil {
		return err
	}
	r.report.MergedDurationSecs = duration
	want := r.report.Fixture.MediaEndSecs - r.report.Fixture.MediaStartSecs
	delta := duration - want
	if delta < 0 {
		delta = -delta
	}
	if delta > 1/r.report.Fixture.FPS+0.001 {
		return fmt.Errorf("merged duration %.6fs differs from fixture %.6fs", duration, want)
	}
	return nil
}
