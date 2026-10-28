package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
	"github.com/wod-strategist/api/internal/storage"
	"gorm.io/gorm"
)

type PrepareOptions struct {
	DatabaseURL, Bucket, OutputDir, SourceSessionID string
	Name, FixturesRoot                              string
	FromFixtureDir                                  string
	SourceProfileID                                 uint
	MediaStartSecs, MediaEndSecs, ChunkSeconds      float64
	AutoEnd                                         bool
}

type replaySource struct {
	ProfileID        uint
	WODDescription   string
	MovementHints    db.JSONDocument
	Appearance       db.JSONDocument
	SensorProcessing db.JSONDocument
	Mapping          []MappingSegment
}

// prepareFixture only reads the source database and GCS objects. Publish the
// completed directory in one rename so an interrupted extraction is never runnable.
func prepareFixture(ctx context.Context, opts PrepareOptions) error {
	root := opts.FixturesRoot
	if root == "" {
		root = defaultFixturesRoot
	}
	if strings.TrimSpace(opts.Name) != "" && strings.TrimSpace(opts.OutputDir) != "" {
		return fmt.Errorf("specify either --wod (WOD=<name>) or --fixture, not both")
	}
	if strings.TrimSpace(opts.Name) != "" {
		slug, err := normalizeFixtureName(opts.Name)
		if err != nil {
			return err
		}
		opts.Name = slug
		opts.OutputDir = filepath.Join(root, slug)
	}
	if opts.FromFixtureDir != "" {
		opts.FromFixtureDir = resolveFromFixtureDir(root, opts.FromFixtureDir)
		return prepareFromFixture(ctx, opts)
	}
	if err := validatePrepareOptions(opts); err != nil {
		return err
	}
	if opts.OutputDir != "" {
		if err := fixtureDestinationAvailable(opts.OutputDir); err != nil {
			return err
		}
	}
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			return fmt.Errorf("%s is required to prepare the replay fixture", binary)
		}
	}
	conn, err := db.Connect(opts.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to source database: %w", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	source, err := readReplaySource(ctx, conn, opts)
	if err != nil {
		return err
	}
	if opts.OutputDir == "" {
		opts.Name = deriveFixtureName(source.WODDescription, opts.SourceSessionID)
		opts.OutputDir = filepath.Join(root, opts.Name)
		if err := fixtureDestinationAvailable(opts.OutputDir); err != nil {
			return err
		}
	}
	startMs := opts.MediaStartSecs * 1000
	var captureStart, captureEnd float64
	if opts.AutoEnd {
		if _, err := contiguousMappedEndMs(source.Mapping, startMs); err != nil {
			return fmt.Errorf("source capture/media mapping: %w", err)
		}
	} else {
		endMs := opts.MediaEndSecs * 1000
		if err := validateMapping(source.Mapping, startMs, endMs); err != nil {
			return fmt.Errorf("source capture/media mapping: %w", err)
		}
		captureStart, err = captureAtMedia(source.Mapping, startMs)
		if err != nil {
			return err
		}
		captureEnd, err = prepareCaptureEnd(source.Mapping, endMs)
		if err != nil {
			return err
		}
	}
	sensorObject, sensorGeneration, err := sourceSensorReference(source.SensorProcessing, source.ProfileID, opts.SourceSessionID)
	if err != nil {
		return err
	}
	movements, err := decodeReplayMovements(source.MovementHints)
	if err != nil {
		return err
	}
	appearance, err := decodeReplayAppearance(source.Appearance)
	if err != nil {
		return err
	}
	client, err := storage.NewClient(ctx, opts.Bucket)
	if err != nil {
		return fmt.Errorf("create source GCS reader (Application Default Credentials required): %w", err)
	}
	parent := filepath.Dir(filepath.Clean(opts.OutputDir))
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".live-replay-prepare-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	videoPath := filepath.Join(stage, "source.mp4")
	videoObject := fmt.Sprintf("videos/%d/%s/merged.mp4", source.ProfileID, opts.SourceSessionID)
	videoRef, err := downloadPinnedObject(ctx, client, videoObject, 0, videoPath, 0)
	if err != nil {
		return fmt.Errorf("download source merged video: %w", err)
	}
	duration, fps, err := probeVideo(ctx, videoPath)
	if err != nil {
		return err
	}
	if !opts.AutoEnd && duration+0.001 < opts.MediaEndSecs {
		return fmt.Errorf("source video is %.3fs; requested end %.3fs is unavailable", duration, opts.MediaEndSecs)
	}
	sensorPath := filepath.Join(stage, "source-sensor.ndjson")
	sensorRef, err := downloadPinnedObject(ctx, client, sensorObject, sensorGeneration, sensorPath, 20*1024*1024)
	if err != nil {
		return fmt.Errorf("download source Polar telemetry: %w", err)
	}
	raw, err := os.ReadFile(sensorPath)
	if err != nil {
		return err
	}
	if err := validateSourceSensorIntegrity(raw, source.SensorProcessing); err != nil {
		return err
	}
	baseEpoch, err := telemetryBaseEpoch(raw, source.ProfileID, opts.SourceSessionID)
	if err != nil {
		return err
	}
	if opts.AutoEnd {
		footerMs, err := telemetryFooterDurationMs(raw)
		if err != nil {
			return err
		}
		resolvedEndSecs, err := resolveAutoMediaEndSecs(source.Mapping, opts.MediaStartSecs, duration, fps, footerMs)
		if err != nil {
			return err
		}
		opts.MediaEndSecs = resolvedEndSecs
		captureStart, err = captureAtMedia(source.Mapping, startMs)
		if err != nil {
			return err
		}
		captureEnd, err = prepareCaptureEnd(source.Mapping, opts.MediaEndSecs*1000)
		if err != nil {
			return err
		}
	}
	cropped, stats, err := cropTelemetry(raw, captureStart, captureEnd, opts.SourceSessionID, source.ProfileID, baseEpoch+int64(math.Round(captureStart)))
	if err != nil {
		return fmt.Errorf("crop source Polar telemetry: %w", err)
	}
	if stats.HRSamples == 0 || stats.ACCSamples == 0 {
		return fmt.Errorf("source interval needs real HR and ACC samples; found HR=%d ACC=%d", stats.HRSamples, stats.ACCSamples)
	}
	if err := validatePreparedTelemetry(cropped, source.ProfileID, opts.SourceSessionID); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "sensor.ndjson"), cropped, 0600); err != nil {
		return err
	}
	chunks, err := prepareVideoChunks(ctx, videoPath, stage, opts, source.Mapping, captureStart, cropped, fps)
	if err != nil {
		return err
	}
	manifest := FixtureManifest{
		Version: 1, Name: opts.Name, PreparedAt: time.Now().UTC(), SourceSessionID: opts.SourceSessionID,
		SourceProfileID: source.ProfileID, Bucket: opts.Bucket, Video: videoRef, Sensor: sensorRef,
		MediaStartSecs: opts.MediaStartSecs, MediaEndSecs: opts.MediaEndSecs, ChunkSeconds: opts.ChunkSeconds,
		CaptureStartMs: captureStart, CaptureEndMs: captureEnd, FPS: fps, Mapping: source.Mapping,
		WODDescription: source.WODDescription, Movements: movements, AppearanceHints: appearance,
		SensorFile: "sensor.ndjson", SensorSHA256: digest(cropped), SensorSizeBytes: int64(len(cropped)),
		Stats: stats, Chunks: chunks,
	}
	if _, err := validateFixture(stage, manifest); err != nil {
		return fmt.Errorf("validate complete prepared fixture: %w", err)
	}
	if err := writeJSON(filepath.Join(stage, "manifest.json"), manifest); err != nil {
		return err
	}
	for _, filename := range []string{videoPath, sensorPath} {
		if err := os.Remove(filename); err != nil {
			return err
		}
	}
	if err := fixtureDestinationAvailable(opts.OutputDir); err != nil {
		return err
	}
	if err := os.Rename(stage, opts.OutputDir); err != nil {
		return fmt.Errorf("publish prepared fixture: %w", err)
	}
	fmt.Printf("Prepared %d chunks (%s..%s, %.1fs), %d HR samples and %d ACC samples at %s\n",
		len(chunks), formatMediaTimestamp(opts.MediaStartSecs, false), formatMediaTimestamp(opts.MediaEndSecs, false),
		opts.MediaEndSecs-opts.MediaStartSecs, stats.HRSamples, stats.ACCSamples, opts.OutputDir)
	if opts.Name != "" {
		fmt.Printf("Run: make live-test WOD=%s\n", opts.Name)
	} else {
		fmt.Printf("Run: make live-test ARGS='--fixture=%s'\n", opts.OutputDir)
	}
	return nil
}

func validateSourceSensorIntegrity(raw, processing []byte) error {
	var recorded struct {
		SHA256       string `json:"expected_sha256"`
		Size         int64  `json:"expected_size_bytes"`
		LegacySHA256 string `json:"sha256"`
		LegacySize   int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal(processing, &recorded); err != nil {
		return fmt.Errorf("decode source integrity metadata: %w", err)
	}
	if recorded.SHA256 == "" {
		recorded.SHA256 = recorded.LegacySHA256
	}
	if recorded.Size == 0 {
		recorded.Size = recorded.LegacySize
	}
	if recorded.SHA256 != "" && recorded.SHA256 != digest(raw) {
		return fmt.Errorf("source sensor SHA256 differs from its accepted upload")
	}
	if recorded.Size != 0 && recorded.Size != int64(len(raw)) {
		return fmt.Errorf("source sensor size differs from its accepted upload")
	}
	return nil
}

func validatePrepareOptions(opts PrepareOptions) error {
	if strings.TrimSpace(opts.DatabaseURL) == "" || strings.TrimSpace(opts.Bucket) == "" || strings.TrimSpace(opts.SourceSessionID) == "" {
		return fmt.Errorf("prepare requires source database URL, bucket, and source session ID (pass SESSION=<session_id> or --source-session)")
	}
	if strings.ContainsAny(opts.SourceSessionID, "/\\") || strings.Contains(opts.Bucket, "/") {
		return fmt.Errorf("invalid source session ID or bucket name")
	}
	values := []float64{opts.MediaStartSecs, opts.ChunkSeconds}
	if !opts.AutoEnd {
		values = append(values, opts.MediaEndSecs)
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("prepare time values must be finite")
		}
	}
	if opts.MediaStartSecs < 0 || opts.ChunkSeconds <= 0 || (!opts.AutoEnd && opts.MediaEndSecs <= opts.MediaStartSecs) {
		return fmt.Errorf("prepare requires 0 <= media start < media end and positive chunk seconds")
	}
	return nil
}

func telemetryFooterDurationMs(raw []byte) (float64, error) {
	events, err := readTelemetry(raw)
	if err != nil {
		return 0, err
	}
	duration, err := telemetryNumber(events[len(events)-1], "t")
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("telemetry requires a positive footer duration")
	}
	return duration, nil
}

func contiguousMappedEndMs(segments []MappingSegment, startMs float64) (float64, error) {
	if math.IsNaN(startMs) || math.IsInf(startMs, 0) || startMs < 0 {
		return 0, fmt.Errorf("invalid media start timestamp")
	}
	ordered, err := sortedReplayMapping(segments)
	if err != nil {
		return 0, err
	}
	cursor := startMs
	started := false
	for _, segment := range ordered {
		if segment.MediaEndMs <= cursor {
			continue
		}
		if segment.MediaStartMs > cursor {
			if !started {
				return 0, fmt.Errorf("media interval has no verified mapping at %.6fms", cursor)
			}
			break
		}
		started = true
		cursor = segment.MediaEndMs
	}
	if !started || cursor <= startMs {
		return 0, fmt.Errorf("verified mapping ends before requested media start %.3fs", startMs/1000)
	}
	return cursor, nil
}

func resolveAutoMediaEndSecs(segments []MappingSegment, startSecs, videoDurationSecs, fps, sensorFooterMs float64) (float64, error) {
	if !finite(startSecs) || startSecs < 0 || !finite(videoDurationSecs) || videoDurationSecs <= 0 || !finite(fps) || fps <= 0 || !finite(sensorFooterMs) || sensorFooterMs <= 0 {
		return 0, fmt.Errorf("invalid inputs for automatic workout end calculation")
	}
	startMs := startSecs * 1000
	ordered, err := sortedReplayMapping(segments)
	if err != nil {
		return 0, err
	}
	cursorMediaMs := startMs
	sensorLimitMediaMs := startMs
	started := false
	sensorCapped := false
	for _, segment := range ordered {
		if segment.MediaEndMs <= cursorMediaMs {
			continue
		}
		if segment.MediaStartMs > cursorMediaMs {
			if !started {
				return 0, fmt.Errorf("source capture/media mapping: media interval has no verified mapping at %.6fms", cursorMediaMs)
			}
			break
		}
		started = true
		cursorMediaMs = segment.MediaEndMs
		if !sensorCapped {
			if sensorFooterMs <= segment.CaptureStartMs {
				sensorCapped = true
			} else if sensorFooterMs < segment.CaptureEndMs {
				frac := (sensorFooterMs - segment.CaptureStartMs) / (segment.CaptureEndMs - segment.CaptureStartMs)
				sensorLimitMediaMs = segment.MediaStartMs + frac*(segment.MediaEndMs-segment.MediaStartMs)
				sensorCapped = true
			} else {
				sensorLimitMediaMs = segment.MediaEndMs
			}
		}
	}
	if !started {
		return 0, fmt.Errorf("source capture/media mapping: verified mapping ends before %.3fs", startSecs)
	}
	limitSecs := math.Min(videoDurationSecs, math.Min(cursorMediaMs/1000, sensorLimitMediaMs/1000))
	if limitSecs <= startSecs {
		return 0, fmt.Errorf("no overlapping video/sensor data after %.3fs (video %.3fs, mapped %.3fs, sensor %.3fs)", startSecs, videoDurationSecs, cursorMediaMs/1000, sensorLimitMediaMs/1000)
	}
	frames := math.Floor((limitSecs-startSecs)*fps + 1e-9)
	if frames < 1 {
		return 0, fmt.Errorf("overlapping video/sensor interval after %.3fs is shorter than one frame", startSecs)
	}
	for frames >= 1 {
		endSecs := math.Round((startSecs+frames/fps)*1e6) / 1e6
		if endSecs > limitSecs {
			endSecs = math.Floor((startSecs+frames/fps)*1e6) / 1e6
		}
		if endSecs > startSecs && endSecs <= videoDurationSecs+0.001 && validateMapping(ordered, startMs, endSecs*1000) == nil {
			captureEnd, endErr := prepareCaptureEnd(ordered, endSecs*1000)
			if endErr == nil && captureEnd <= sensorFooterMs {
				return endSecs, nil
			}
		}
		frames--
	}
	return 0, fmt.Errorf("unable to align automatic workout end after %.3fs within video and sensor bounds", startSecs)
}

func fixtureDestinationAvailable(dir string) error {
	_, err := os.Lstat(dir)
	if err == nil {
		return fmt.Errorf("fixture directory already exists: %s; choose a new --fixture or move the existing fixture before preparing again", dir)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func readReplaySource(ctx context.Context, conn *gorm.DB, opts PrepareOptions) (replaySource, error) {
	var source replaySource
	err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var analysis struct {
			ProfileID        uint
			WODDescription   string
			SensorProcessing db.JSONDocument
		}
		if err := tx.Table("analysis_results").Select("profile_id", "wod_description", "sensor_processing").Where("session_id = ?", opts.SourceSessionID).Take(&analysis).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("source session %s was not found in the configured database; use the live source database (wod-strategist_dev), not the old local wod_dev", opts.SourceSessionID)
			}
			return fmt.Errorf("read source analysis: %w", err)
		}
		if opts.SourceProfileID != 0 && opts.SourceProfileID != analysis.ProfileID {
			return fmt.Errorf("source session belongs to profile %d, not requested profile %d", analysis.ProfileID, opts.SourceProfileID)
		}
		source.ProfileID, source.WODDescription, source.SensorProcessing = analysis.ProfileID, analysis.WODDescription, analysis.SensorProcessing
		var session struct {
			ProfileID      uint
			WODDescription string
			MovementHints  db.JSONDocument
		}
		err := tx.Table("sessions").Select("profile_id", "wod_description", "movement_hints").Where("session_id = ?", opts.SourceSessionID).Take(&session).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read source session metadata: %w", err)
		}
		if err == nil {
			if session.ProfileID != source.ProfileID {
				return fmt.Errorf("source session profile conflicts with analysis profile")
			}
			source.MovementHints = session.MovementHints
			if strings.TrimSpace(session.WODDescription) != "" {
				source.WODDescription = session.WODDescription
			}
		}
		var appearance struct{ Hints db.JSONDocument }
		err = tx.Table("session_appearance_hints").Select("hints").Where("session_id = ? AND profile_id = ?", opts.SourceSessionID, source.ProfileID).Order("updated_at DESC").Limit(1).Find(&appearance).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read source appearance: %w", err)
		}
		source.Appearance = appearance.Hints
		var chunks []struct{ StartSecs, EndSecs, MediaStartSecs, MediaEndSecs *float64 }
		if err := tx.Table("chunk_analysis_results").Select("start_secs", "end_secs", "media_start_secs", "media_end_secs").Where("session_id = ? AND profile_id = ?", opts.SourceSessionID, source.ProfileID).Order("media_start_secs ASC NULLS LAST").Find(&chunks).Error; err != nil {
			return fmt.Errorf("read source chunk timeline: %w", err)
		}
		for _, chunk := range chunks {
			if chunk.StartSecs == nil || chunk.EndSecs == nil || chunk.MediaStartSecs == nil || chunk.MediaEndSecs == nil {
				continue
			}
			values := []float64{*chunk.StartSecs, *chunk.EndSecs, *chunk.MediaStartSecs, *chunk.MediaEndSecs}
			valid := true
			for _, value := range values {
				valid = valid && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
			}
			if valid && *chunk.EndSecs > *chunk.StartSecs && *chunk.MediaEndSecs > *chunk.MediaStartSecs {
				source.Mapping = append(source.Mapping, MappingSegment{CaptureStartMs: *chunk.StartSecs * 1000, CaptureEndMs: *chunk.EndSecs * 1000, MediaStartMs: *chunk.MediaStartSecs * 1000, MediaEndMs: *chunk.MediaEndSecs * 1000})
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return source, err
}

func sourceSensorReference(document []byte, profileID uint, sessionID string) (string, int64, error) {
	var processing struct {
		ObjectName       string          `json:"object_name"`
		TargetGeneration json.RawMessage `json:"target_generation"`
	}
	if err := json.Unmarshal(document, &processing); err != nil {
		return "", 0, fmt.Errorf("source sensor_processing is not valid JSON: %w", err)
	}
	prefix := fmt.Sprintf("videos/%d/%s/", profileID, sessionID)
	if !strings.HasPrefix(processing.ObjectName, prefix) || pathUnsafe(processing.ObjectName) || !strings.HasSuffix(processing.ObjectName, ".ndjson") {
		return "", 0, fmt.Errorf("source sensor_processing.object_name must identify an NDJSON object under %s", prefix)
	}
	var generation int64
	raw := strings.TrimSpace(string(processing.TargetGeneration))
	if raw != "" && raw != "null" {
		if strings.HasPrefix(raw, "\"") {
			if err := json.Unmarshal(processing.TargetGeneration, &raw); err != nil {
				return "", 0, fmt.Errorf("invalid source sensor generation: %w", err)
			}
		}
		var err error
		generation, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || generation <= 0 {
			return "", 0, fmt.Errorf("invalid source sensor target_generation")
		}
	}
	return processing.ObjectName, generation, nil
}

func pathUnsafe(object string) bool {
	return strings.Contains(object, "\\") || strings.Contains(object, "//") || strings.Contains("/"+object+"/", "/../") || strings.Contains("/"+object+"/", "/./")
}

// The end of a half-open video interval belongs to the preceding segment.
// captureAtMedia intentionally chooses the following segment for start times,
// which would otherwise attach a pause between chunks to the preceding chunk.
func prepareCaptureEnd(mapping []MappingSegment, mediaMs float64) (float64, error) {
	for _, segment := range mapping {
		if segment.MediaEndMs == mediaMs {
			return segment.CaptureEndMs, nil
		}
	}
	return captureAtMedia(mapping, mediaMs)
}

func decodeReplayMovements(document []byte) ([]string, error) {
	if len(document) == 0 || string(document) == "null" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal(document, &values); err != nil {
		return nil, fmt.Errorf("source movement_hints: %w", err)
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			result, seen[key] = append(result, value), true
		}
	}
	return result, nil
}

func decodeReplayAppearance(document []byte) (string, error) {
	if len(document) == 0 || string(document) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(document, &value); err == nil {
		return strings.TrimSpace(value), nil
	}
	var legacy struct {
		Appearance string            `json:"appearance"`
		Session    map[string]string `json:"session"`
		Persistent map[string]string `json:"persistent"`
		Removable  []string          `json:"removable"`
	}
	if err := json.Unmarshal(document, &legacy); err != nil {
		return "", fmt.Errorf("source appearance hints: %w", err)
	}
	if strings.TrimSpace(legacy.Appearance) != "" {
		return strings.TrimSpace(legacy.Appearance), nil
	}
	var values []string
	for _, key := range []string{"top", "bottom"} {
		if value := strings.TrimSpace(legacy.Session[key]); value != "" {
			values = append(values, value)
		}
	}
	for _, key := range []string{"shoes", "hair", "build"} {
		if value := strings.TrimSpace(legacy.Persistent[key]); value != "" {
			values = append(values, value)
		}
	}
	for _, value := range legacy.Removable {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return strings.Join(values, ", "), nil
}

func downloadPinnedObject(ctx context.Context, client *storage.Client, object string, generation int64, filename string, maxBytes int64) (ObjectReference, error) {
	attrs, err := client.ObjectAttrs(ctx, object)
	if err != nil {
		return ObjectReference{}, err
	}
	if generation == 0 {
		generation = attrs.Generation
	}
	if generation <= 0 {
		return ObjectReference{}, fmt.Errorf("GCS returned no generation for %s", object)
	}
	if maxBytes > 0 && generation == attrs.Generation && attrs.Size > maxBytes {
		return ObjectReference{}, fmt.Errorf("source object exceeds %d-byte limit", maxBytes)
	}
	r, err := client.NewReaderWithGeneration(ctx, object, generation)
	if err != nil {
		return ObjectReference{}, err
	}
	defer r.Close()
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ObjectReference{}, err
	}
	hash := sha256.New()
	var reader io.Reader = r
	if maxBytes > 0 {
		reader = io.LimitReader(r, maxBytes+1)
	}
	size, copyErr := io.Copy(io.MultiWriter(f, hash), reader)
	closeErr := f.Close()
	if copyErr != nil {
		return ObjectReference{}, copyErr
	}
	if closeErr != nil {
		return ObjectReference{}, closeErr
	}
	if maxBytes > 0 && size > maxBytes {
		return ObjectReference{}, fmt.Errorf("source object exceeds %d-byte limit", maxBytes)
	}
	if generation == attrs.Generation && size != attrs.Size {
		return ObjectReference{}, fmt.Errorf("GCS object size changed while reading pinned source %s", object)
	}
	return ObjectReference{Object: object, Generation: strconv.FormatInt(generation, 10), SHA256: hex.EncodeToString(hash.Sum(nil)), SizeBytes: size}, nil
}

func telemetryBaseEpoch(raw []byte, profileID uint, sessionID string) (int64, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	if !scanner.Scan() {
		return 0, fmt.Errorf("source sensor telemetry has no meta header")
	}
	var meta sensor.MetaEvent
	if err := json.Unmarshal(scanner.Bytes(), &meta); err != nil {
		return 0, fmt.Errorf("decode source sensor header: %w", err)
	}
	if meta.Kind != "meta" || meta.ProfileID != profileID || meta.WorkoutSessionID != sessionID || meta.BaseEpochMs <= 0 || meta.ClockSource != "capture_clock" {
		return 0, fmt.Errorf("source sensor meta identity or capture clock does not match the selected session")
	}
	return meta.BaseEpochMs, nil
}

func validatePreparedTelemetry(data []byte, profileID uint, sessionID string) error {
	result, err := sensor.ParseAndProcess(bytes.NewReader(data), sensor.ParseOptions{CalculationVersion: 3, ExpectedProfileID: profileID, ExpectedSessionID: sessionID, ExpectedSHA256: digest(data), ExpectedSizeBytes: int64(len(data))})
	if err != nil {
		return fmt.Errorf("validate prepared Polar telemetry: %w", err)
	}
	if !result.Quality.IsComplete || len(result.Quality.Errors) != 0 {
		return fmt.Errorf("prepared Polar telemetry failed real sensor parser validation: %s (%s)", result.Quality.Status, strings.Join(result.Quality.Errors, "; "))
	}
	return nil
}

func probeVideo(ctx context.Context, filename string) (durationSecs, fps float64, err error) {
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=duration,avg_frame_rate,r_frame_rate:format=duration", "-of", "json", filename).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("probe video %s: %w", filepath.Base(filename), err)
	}
	var result struct {
		Streams []struct {
			Duration   string `json:"duration"`
			AverageFPS string `json:"avg_frame_rate"`
			RealFPS    string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return 0, 0, err
	}
	if len(result.Streams) == 0 {
		return 0, 0, fmt.Errorf("no video stream in %s", filepath.Base(filename))
	}
	// Match the merge worker's ffprobe format-duration convention. Prepared
	// chunks omit audio so AAC encoder padding cannot shift these intervals.
	durationSecs, _ = strconv.ParseFloat(result.Format.Duration, 64)
	if durationSecs <= 0 {
		durationSecs, _ = strconv.ParseFloat(result.Streams[0].Duration, 64)
	}
	fps = parseFrameRate(result.Streams[0].AverageFPS)
	if fps <= 0 {
		fps = parseFrameRate(result.Streams[0].RealFPS)
	}
	if math.IsNaN(durationSecs) || math.IsInf(durationSecs, 0) || durationSecs <= 0 || math.IsNaN(fps) || math.IsInf(fps, 0) || fps <= 0 {
		return 0, 0, fmt.Errorf("invalid video duration or frame rate for %s", filepath.Base(filename))
	}
	return durationSecs, fps, nil
}

func parseFrameRate(value string) float64 {
	parts := strings.Split(value, "/")
	numerator, _ := strconv.ParseFloat(parts[0], 64)
	if len(parts) == 1 {
		return numerator
	}
	if len(parts) != 2 {
		return 0
	}
	denominator, _ := strconv.ParseFloat(parts[1], 64)
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
}

func prepareVideoChunks(ctx context.Context, sourcePath, dir string, opts PrepareOptions, mapping []MappingSegment, captureStart float64, telemetry []byte, sourceFPS float64) ([]ReplayChunk, error) {
	chunkDir := filepath.Join(dir, "chunks")
	if err := os.Mkdir(chunkDir, 0700); err != nil {
		return nil, err
	}
	seconds := func(value float64) string { return strconv.FormatFloat(value, 'f', 9, 64) }
	// Normalize the source PTS before forcing keyframes. Permit half a frame
	// of muxer rounding so a keyframe just before 10s does not miss that cut.
	args := []string{"-v", "error", "-nostdin", "-ss", seconds(opts.MediaStartSecs), "-i", sourcePath, "-t", seconds(opts.MediaEndSecs - opts.MediaStartSecs), "-map", "0:v:0", "-an", "-vf", "setpts=PTS-STARTPTS,scale=w='min(iw,1280)':h='min(ih,720)':force_original_aspect_ratio=decrease:force_divisible_by=2", "-c:v", "libx264", "-preset", "veryfast", "-crf", "22", "-fps_mode", "passthrough", "-sc_threshold", "0", "-force_key_frames", "expr:gte(t,n_forced*" + seconds(opts.ChunkSeconds) + ")", "-f", "segment", "-segment_time", seconds(opts.ChunkSeconds), "-segment_time_delta", seconds(1 / (2 * sourceFPS)), "-reset_timestamps", "1", filepath.Join(chunkDir, "chunk_%04d.mp4")}
	if output, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("prepare video chunks: %w: %s", err, strings.TrimSpace(string(output)))
	}
	files, err := filepath.Glob(filepath.Join(chunkDir, "chunk_*.mp4"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("ffmpeg produced no replay chunks")
	}
	chunks := make([]ReplayChunk, 0, len(files))
	cursor := 0.0
	requestedDuration := opts.MediaEndSecs - opts.MediaStartSecs
	tolerance := 1/sourceFPS + 0.001
	for index, filename := range files {
		duration, fps, err := probeVideo(ctx, filename)
		if err != nil {
			return nil, err
		}
		if math.Abs(fps-sourceFPS) > math.Max(0.01, sourceFPS*0.001) {
			return nil, fmt.Errorf("prepared chunk %d changed source FPS: %.6f -> %.6f", index, sourceFPS, fps)
		}
		mediaEnd := cursor + duration
		if index == len(files)-1 && math.Abs(mediaEnd-requestedDuration) <= tolerance {
			mediaEnd = requestedDuration
		}
		if mediaEnd > requestedDuration+tolerance {
			return nil, fmt.Errorf("prepared video extends beyond requested interval")
		}
		captureChunkStart, err := captureAtMedia(mapping, (opts.MediaStartSecs+cursor)*1000)
		if err != nil {
			return nil, err
		}
		captureChunkEnd, err := prepareCaptureEnd(mapping, math.Min(opts.MediaEndSecs, opts.MediaStartSecs+mediaEnd)*1000)
		if err != nil {
			return nil, err
		}
		peak, err := peakHeartRate(telemetry, captureChunkStart-captureStart, captureChunkEnd-captureStart)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(dir, filename)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, ReplayChunk{File: filepath.ToSlash(relative), SHA256: digest(data), SizeBytes: int64(len(data)), MediaStartSecs: cursor, MediaEndSecs: mediaEnd, CaptureStartSecs: (captureChunkStart - captureStart) / 1000, CaptureEndSecs: (captureChunkEnd - captureStart) / 1000, HeartRateBPM: peak})
		cursor = mediaEnd
	}
	if math.Abs(cursor-requestedDuration) > tolerance {
		return nil, fmt.Errorf("prepared video duration %.3fs differs from requested %.3fs", cursor, requestedDuration)
	}
	if err := validateChunkCadence(chunks, opts.ChunkSeconds, requestedDuration, sourceFPS); err != nil {
		return nil, err
	}
	return chunks, nil
}
