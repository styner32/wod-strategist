package worker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gcs "cloud.google.com/go/storage"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/storage"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

type VideoComparisonOptions struct {
	SessionID          string
	LatestCompletedWOD bool
	OutputDir          string
	APIKey             string `json:"-"` // only for redacting errors
}

type comparisonSource struct {
	SessionID       string  `json:"session_id"`
	ProfileID       uint    `json:"profile_id"`
	GCSURI          string  `json:"gcs_uri"`
	Generation      string  `json:"generation"`
	SHA256          string  `json:"sha256"`
	Bytes           int64   `json:"bytes"`
	DurationSeconds float64 `json:"duration_seconds"`
	DownloadSeconds float64 `json:"download_seconds"`
	ProbeSeconds    float64 `json:"probe_seconds"`
}

type videoComparisonReport struct {
	Version        int                          `json:"version"`
	Model          string                       `json:"model"`
	SDK            string                       `json:"sdk"`
	Source         comparisonSource             `json:"source"`
	Preparation    gemini.ComparisonPreparation `json:"preparation"`
	GeminiFileName string                       `json:"gemini_file_name,omitempty"`
	GeminiFileURI  string                       `json:"gemini_file_uri,omitempty"`
	Cleanup        string                       `json:"cleanup"`
	Error          string                       `json:"error,omitempty"`
	Results        []gemini.VideoModeResult     `json:"results"`
}

// RunVideoComparison is an explicit local experiment. All database access is
// through read-only transactions; it never enqueues tasks or writes analysis rows.
func RunVideoComparison(ctx context.Context, conn *gorm.DB, sc *storage.Client, bucket string, gc *gemini.Client, opts VideoComparisonOptions) (retErr error) {
	if (opts.SessionID == "") == !opts.LatestCompletedWOD {
		return errors.New("choose exactly one session selector")
	}
	if opts.OutputDir == "" {
		return errors.New("output directory is required")
	}
	if err := os.MkdirAll(filepath.Dir(opts.OutputDir), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(opts.OutputDir, 0700); err != nil {
		return fmt.Errorf("create fresh output directory: %w", err)
	}
	report := videoComparisonReport{Version: 1, Model: gemini.ModelFlash38, SDK: "v1.71.0", Cleanup: "not_uploaded", Results: []gemini.VideoModeResult{}}
	redact := func(s string) string {
		if opts.APIKey != "" {
			return strings.ReplaceAll(s, opts.APIKey, "[REDACTED]")
		}
		return s
	}
	defer func() {
		if retErr != nil {
			report.Error = redact(retErr.Error())
		}
		retErr = errors.Join(retErr, saveComparisonReport(opts.OutputDir, report))
	}()
	var selected db.AnalysisResult
	var attrs *gcs.ObjectAttrs
	var sourceURI string
	prepCtx, cancelPrep := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelPrep()
	err := conn.WithContext(prepCtx).Transaction(func(tx *gorm.DB) error {
		w := NewWorker(tx, sc, bucket, nil, nil, nil)
		var err error
		selected, sourceURI, attrs, err = w.selectComparisonSource(prepCtx, opts)
		return err
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	report.Source = comparisonSource{SessionID: selected.SessionID, ProfileID: selected.ProfileID, GCSURI: sourceURI, Generation: strconv.FormatInt(attrs.Generation, 10), Bytes: attrs.Size}
	fmt.Printf("Selected %s; source generation %d; %.1f MiB\n", selected.SessionID, attrs.Generation, float64(attrs.Size)/(1024*1024))
	temp, err := os.MkdirTemp("", "wod-video-comparison-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	local := filepath.Join(temp, "source.mp4")
	started := time.Now()
	reader, err := sc.NewReaderWithGeneration(prepCtx, attrs.Name, attrs.Generation)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = reader.Close()
		return err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hash), reader)
	err = errors.Join(copyErr, f.Close(), reader.Close())
	report.Source.DownloadSeconds = time.Since(started).Seconds()
	if err != nil {
		return err
	}
	if n != attrs.Size {
		return errors.New("download size differs from pinned object")
	}
	report.Source.SHA256 = hex.EncodeToString(hash.Sum(nil))
	started = time.Now()
	report.Source.DurationSeconds = probeVideoDuration(prepCtx, local)
	report.Source.ProbeSeconds = time.Since(started).Seconds()
	if report.Source.DurationSeconds <= 0 {
		return errors.New("source has no verified positive video duration")
	}
	var prompt string
	err = conn.WithContext(prepCtx).Transaction(func(tx *gorm.DB) error {
		w := NewWorker(tx, sc, bucket, nil, nil, nil)
		// Reuse the production index prompt, including its target-identification
		// context. Neither prior output nor requested repetition totals are labels.
		prompt = w.buildIndexPrompt(VideoAnalysisPayload{SessionID: selected.SessionID, ProfileID: selected.ProfileID, WorkoutType: WorkoutTypeWOD}, time.Duration(report.Source.DurationSeconds*float64(time.Second)))
		return nil
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(opts.OutputDir, "prompt.txt"), []byte(prompt), 0600); err != nil {
		return err
	}
	upload, timings, uploadErr := gc.UploadComparisonVideo(prepCtx, local)
	report.Preparation = timings
	if upload != nil && upload.FileName != "" {
		report.GeminiFileName, report.GeminiFileURI = upload.FileName, upload.FileURI
		report.Cleanup = "pending"
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := gc.DeleteFile(cleanupCtx, upload.FileName); err != nil {
				report.Cleanup = "failed: " + redact(err.Error())
				retErr = errors.Join(retErr, errors.New("experiment upload cleanup failed; see summary.json"))
			} else {
				report.Cleanup = "deleted"
			}
		}()
	}
	if uploadErr != nil {
		return uploadErr
	}
	if err := saveComparisonReport(opts.OutputDir, report); err != nil {
		return err
	}
	for _, mode := range []genai.MediaProcessing{genai.MediaProcessingStatic, genai.MediaProcessingAgentic} {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Printf("Running %s (10 minute limit, one attempt)\n", mode)
		result := gc.CompareVideoMode(ctx, upload.FileURI, upload.MIMEType, prompt, mode)
		result.Error = redact(result.Error)
		report.Results = append(report.Results, result)
		prefix := strings.ToLower(string(mode))
		for name, data := range map[string][]byte{"request.json": result.Request, "response.json": result.Response, "answer.txt": []byte(result.Text)} {
			if err := os.WriteFile(filepath.Join(opts.OutputDir, prefix+"."+name), data, 0600); err != nil {
				return err
			}
		}
		if err := saveComparisonReport(opts.OutputDir, report); err != nil {
			return err
		}
		fmt.Printf("%s: %s, %.3fs, navigation calls=%d responses=%d\n", mode, result.Outcome, result.ElapsedSeconds, result.MediaToolCalls, result.MediaToolResponses)
	}
	for _, r := range report.Results {
		if r.Outcome != "completed" {
			return errors.New("comparison contains failed or incomplete inference; see artifacts")
		}
		if r.Mode == "AGENTIC" && !r.AgenticObserved {
			return errors.New("agentic navigation was not observed; see artifacts")
		}
	}
	return nil
}

func (w *Worker) selectComparisonSource(ctx context.Context, opts VideoComparisonOptions) (db.AnalysisResult, string, *gcs.ObjectAttrs, error) {
	if opts.SessionID != "" {
		if err := validateSessionID(opts.SessionID); err != nil {
			return db.AnalysisResult{}, "", nil, err
		}
	}
	for offset := 0; ; offset += 20 {
		var rows []db.AnalysisResult
		query := w.DB.WithContext(ctx).Model(&db.AnalysisResult{}).
			Where("analysis_results.status = ? AND analysis_type = ? AND analysis_results.archived_at IS NULL", "COMPLETED", db.AnalysisTypeWOD).
			Where("(analysis_results.session_id LIKE 'WOD-%' OR analysis_results.session_id LIKE 'P%-WOD-%')")
		if opts.SessionID != "" {
			query = query.Where("analysis_results.session_id = ?", opts.SessionID)
		}
		if err := query.Order("COALESCE(workout_at, created_at) DESC, id DESC").Limit(20).Offset(offset).Find(&rows).Error; err != nil {
			return db.AnalysisResult{}, "", nil, err
		}
		if len(rows) == 0 {
			return db.AnalysisResult{}, "", nil, errors.New("no completed WOD with retained source video found")
		}
		for _, row := range rows {
			if err := validateSessionID(row.SessionID); err != nil {
				return db.AnalysisResult{}, "", nil, err
			}
			uri, err := w.findSourceVideo(ctx, row.ProfileID, row.SessionID)
			if err != nil {
				if opts.LatestCompletedWOD && strings.HasPrefix(err.Error(), "no video files found for session ") {
					continue
				}
				return db.AnalysisResult{}, "", nil, err
			}
			_, object, err := storage.ParseGCSURI(uri)
			if err != nil {
				return db.AnalysisResult{}, "", nil, err
			}
			attrs, err := w.StorageClient.ObjectAttrs(ctx, object)
			if errors.Is(err, gcs.ErrObjectNotExist) && opts.LatestCompletedWOD {
				continue
			}
			if err != nil {
				return db.AnalysisResult{}, "", nil, err
			}
			if attrs.Generation <= 0 || attrs.Size <= 0 {
				return db.AnalysisResult{}, "", nil, errors.New("source has invalid generation or size")
			}
			return row, uri, attrs, nil
		}
	}
}

func saveComparisonReport(dir string, report videoComparisonReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), append(data, '\n'), 0600); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Static · Agentic video comparison\n\nSession: `%s`  \nModel: `%s`; SDK: `%s`  \nDuration: %.3fs; generation: `%s`  \nSHA-256: `%s`\n\n", report.Source.SessionID, report.Model, report.SDK, report.Source.DurationSeconds, report.Source.Generation, report.Source.SHA256)
	fmt.Fprintf(&b, "Preparation: download %.3fs; probe %.3fs; upload %.3fs; Files ready %.3fs.\n\n", report.Source.DownloadSeconds, report.Source.ProbeSeconds, report.Preparation.UploadSeconds, report.Preparation.ReadySeconds)
	b.WriteString("| Mode | Outcome | Seconds | Input | Output | Thinking | Tool use | Cached | Total | Navigation calls / results |\n|---|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range report.Results {
		u := r.Usage
		if u == nil {
			u = &gemini.ComparisonUsage{}
		}
		fmt.Fprintf(&b, "| %s | %s | %.3f | %s | %s | %s | %s | %s | %s | %d / %d |\n", r.Mode, r.Outcome, r.ElapsedSeconds, comparisonMetric(u.Input), comparisonMetric(u.Output), comparisonMetric(u.Thinking), comparisonMetric(u.ToolUse), comparisonMetric(u.Cached), comparisonMetric(u.Total), r.MediaToolCalls, r.MediaToolResponses)
	}
	b.WriteString("\nOne call per mode, STATIC first. Timing includes response body transfer, excludes common preparation and local parsing. Cache/order effects are not controlled. N/A means omitted, not zero. This is not an accuracy or general performance benchmark.\n")
	b.WriteString(comparisonSegmentTable(report.Results))
	for _, r := range report.Results {
		fmt.Fprintf(&b, "\n## %s segments (model observations, unverified)\n\n", r.Mode)
		segments := parseSegments(r.Text)
		if len(segments) == 0 {
			b.WriteString("No parseable segment array; see the original answer.\n")
		}
		for _, s := range segments {
			start, okStart := parseSegmentTimestamp(s.Start)
			end, okEnd := parseSegmentTimestamp(s.End)
			valid := okStart && okEnd && start >= 0 && end > start && end.Seconds() <= report.Source.DurationSeconds
			fmt.Fprintf(&b, "- %s–%s: %s; interval valid=%t\n", s.Start, s.End, s.Type, valid)
		}
		if r.Error != "" {
			fmt.Fprintf(&b, "\nError: %s\n", r.Error)
		}
	}
	fmt.Fprintf(&b, "\nUpload cleanup: %s\n", report.Cleanup)
	if report.Error != "" {
		fmt.Fprintf(&b, "\nRun error: %s\n", report.Error)
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(b.String()), 0600)
}

func comparisonMetric(v *int64) string {
	if v == nil {
		return "N/A"
	}
	return strconv.FormatInt(*v, 10)
}

// Match exact exercise labels by occurrence, not by an assumed ground truth.
// Missing/reordered detections can make these provisional pairs refer to different events.
func comparisonSegmentTable(results []gemini.VideoModeResult) string {
	var static, agentic []Segment
	for _, r := range results {
		if r.Mode == "STATIC" {
			static = parseSegments(r.Text)
		} else if r.Mode == "AGENTIC" {
			agentic = parseSegments(r.Text)
		}
	}
	var b strings.Builder
	b.WriteString("\n## Provisional timestamp comparison\n\nPair by exact exercise label and occurrence order. These are not verified identical events; missing detections can shift pairs. Delta = AGENTIC minus STATIC, seconds. Unmatched or invalid timestamps are N/A.\n\n| Exercise | STATIC | AGENTIC | Start delta | End delta |\n|---|---|---|---:|---:|\n")
	used := make([]bool, len(agentic))
	interval := func(s Segment) string { return s.Start + "–" + s.End }
	delta := func(a, s string) string {
		at, aok := parseSegmentTimestamp(a)
		st, sok := parseSegmentTimestamp(s)
		if !aok || !sok {
			return "N/A"
		}
		return fmt.Sprintf("%+.3f", (at - st).Seconds())
	}
	label := func(s string) string { return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(s) }
	for _, s := range static {
		match := -1
		for i, a := range agentic {
			if !used[i] && a.Type == s.Type {
				match = i
				break
			}
		}
		if match < 0 {
			fmt.Fprintf(&b, "| %s | %s | N/A | N/A | N/A |\n", label(s.Type), interval(s))
			continue
		}
		used[match] = true
		a := agentic[match]
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", label(s.Type), interval(s), interval(a), delta(a.Start, s.Start), delta(a.End, s.End))
	}
	for i, a := range agentic {
		if !used[i] {
			fmt.Fprintf(&b, "| %s | N/A | %s | N/A | N/A |\n", label(a.Type), interval(a))
		}
	}
	return b.String()
}
