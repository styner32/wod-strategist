package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
)

// HighlightPayload is the payload for the highlight:generate task.
type HighlightPayload struct {
	SessionID   string
	ProfileID   uint
	MaxDuration int // max highlight duration in seconds (default 60)
}

func NewGenerateHighlightTask(sessionID string, profileID uint, maxDuration int) (*asynq.Task, error) {
	if maxDuration <= 0 {
		maxDuration = 60
	}
	payload := HighlightPayload{
		SessionID:   sessionID,
		ProfileID:   profileID,
		MaxDuration: maxDuration,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeGenerateHighlight, data), nil
}

func (w *Worker) HandleGenerateHighlightTask(ctx context.Context, t *asynq.Task) error {
	var p HighlightPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	retryCount, ok := asynq.GetRetryCount(ctx)
	if !ok {
		retryCount = 0
	}

	w.logger.Info("Processing highlight generation",
		zap.String("session_id", p.SessionID),
		zap.Int("max_duration", p.MaxDuration),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		w.logger.Error("Max retries reached. Skipping highlight generation.")
		return asynq.SkipRetry
	}

	// 1. Query the WOD analysis result for this session
	var analysisResult db.AnalysisResult
	if err := w.DB.Where("session_id = ? AND analysis_type = ? AND status = ?",
		p.SessionID, db.AnalysisTypeWOD, "COMPLETED").
		Order("created_at DESC").
		First(&analysisResult).Error; err != nil {
		w.logger.Error("No completed WOD analysis found for highlight generation",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return fmt.Errorf("no completed WOD analysis found: %w", asynq.SkipRetry)
	}

	if analysisResult.HighlightSegments == "" {
		w.logger.Warn("No highlight segments in WOD analysis",
			zap.String("session_id", p.SessionID))
		return fmt.Errorf("no highlight segments available: %w", asynq.SkipRetry)
	}

	// 2. Parse highlight segments
	var segments []HighlightSegment
	if err := json.Unmarshal([]byte(analysisResult.HighlightSegments), &segments); err != nil {
		w.logger.Error("Failed to parse highlight segments",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return fmt.Errorf("invalid highlight segments JSON: %w", asynq.SkipRetry)
	}

	if len(segments) == 0 {
		w.logger.Warn("Empty highlight segments array",
			zap.String("session_id", p.SessionID))
		return fmt.Errorf("no highlight segments: %w", asynq.SkipRetry)
	}

	w.logger.Info("Highlight segments parsed",
		zap.String("session_id", p.SessionID),
		zap.Int("segment_count", len(segments)))

	// 3. Query chunk analysis results to get time range → GCS file path mappings
	var chunks []db.ChunkAnalysisResult
	if err := w.DB.Where("session_id = ? AND status = ? AND file_path != ''",
		p.SessionID, "COMPLETED").
		Order("start_secs ASC").
		Find(&chunks).Error; err != nil {
		w.logger.Error("Failed to query chunk analysis results",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return fmt.Errorf("failed to query chunks: %w", err)
	}

	if len(chunks) == 0 {
		w.logger.Error("No chunk records with file_path found",
			zap.String("session_id", p.SessionID))
		return fmt.Errorf("no chunk records found: %w", asynq.SkipRetry)
	}

	w.logger.Info("Found chunk records",
		zap.String("session_id", p.SessionID),
		zap.Int("chunk_count", len(chunks)))

	for i, ch := range chunks {
		var start, end float64 = -1, -1
		if ch.StartSecs != nil {
			start = *ch.StartSecs
		}
		if ch.EndSecs != nil {
			end = *ch.EndSecs
		}
		w.logger.Info("Chunk Mapping",
			zap.Int("index", i),
			zap.String("file_path", ch.FilePath),
			zap.Float64("start_secs", start),
			zap.Float64("end_secs", end))
	}

	if strings.ContainsRune(p.SessionID, filepath.Separator) {
		return fmt.Errorf("invalid session ID: %w", asynq.SkipRetry)
	}
	// 4. Create temp directory
	safeSessionID := strings.ReplaceAll(filepath.Base(p.SessionID), ".", "_")
	tmpDir := filepath.Join("/tmp", fmt.Sprintf("highlight_%s_%d", safeSessionID, os.Getpid()))
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("failed to create highlight temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 5. For each highlight segment, find the matching chunk, download it, trim it
	var trimmedPaths []string
	var trimmedDurations []float64
	var validSegments []HighlightSegment
	var totalDuration float64

	for i, seg := range segments {
		segStartSecs, err := parseTimestampToSeconds(seg.Start)
		if err != nil {
			w.logger.Warn("Skipping segment with invalid start timestamp",
				zap.Int("index", i), zap.String("start", seg.Start), zap.Error(err))
			continue
		}
		segEndSecs, err := parseTimestampToSeconds(seg.End)
		if err != nil {
			w.logger.Warn("Skipping segment with invalid end timestamp",
				zap.Int("index", i), zap.String("end", seg.End), zap.Error(err))
			continue
		}

		segDuration := segEndSecs - segStartSecs
		if segDuration <= 0 {
			w.logger.Warn("Skipping segment with non-positive duration",
				zap.Int("index", i), zap.Float64("start", segStartSecs), zap.Float64("end", segEndSecs))
			continue
		}

		// Respect max duration
		if totalDuration+segDuration > float64(p.MaxDuration) {
			w.logger.Info("Max duration reached, stopping segment collection",
				zap.Float64("total_so_far", totalDuration),
				zap.Int("max_duration", p.MaxDuration))
			break
		}

		// Find which chunk covers this timestamp range (uses DB records directly)
		chunkIdx := findChunkForTimestamp(chunks, segStartSecs)
		if chunkIdx < 0 || chunkIdx >= len(chunks) {
			w.logger.Warn("No chunk found for highlight segment timestamp",
				zap.Int("index", i), zap.Float64("start_secs", segStartSecs))
			continue
		}

		chunkGCSURI := chunks[chunkIdx].FilePath
		chunkLocalPath := filepath.Join(tmpDir, fmt.Sprintf("chunk_%03d.mp4", chunkIdx))

		// Download chunk (skip if already downloaded for a previous segment)
		if _, err := os.Stat(chunkLocalPath); os.IsNotExist(err) {
			if err := w.StorageClient.DownloadFile(ctx, chunkGCSURI, chunkLocalPath); err != nil {
				w.logger.Warn("Failed to download chunk for highlight",
					zap.String("gcs_uri", chunkGCSURI), zap.Error(err))
				continue
			}
		}

		// Calculate the trim offset within this chunk
		var chunkStartSecs float64
		if chunks[chunkIdx].StartSecs != nil {
			chunkStartSecs = *chunks[chunkIdx].StartSecs
		}
		trimStart := segStartSecs - chunkStartSecs
		if trimStart < 0 {
			trimStart = 0
		}

		w.logger.Info("Highlight segment mapping",
			zap.Int("segment_index", i),
			zap.Float64("seg_start_secs", segStartSecs),
			zap.Float64("seg_end_secs", segEndSecs),
			zap.Int("resolved_chunk_idx", chunkIdx),
			zap.String("chunk_gcs_uri", chunkGCSURI),
			zap.Float64("chunk_start_secs", chunkStartSecs),
			zap.Float64("trim_start", trimStart),
			zap.Float64("seg_duration", segDuration))

		trimmedPath := filepath.Join(tmpDir, fmt.Sprintf("trimmed_%03d.mp4", i))
		if err := runFFmpegTrim(ctx, w.logger, chunkLocalPath, trimmedPath, trimStart, segDuration); err != nil {
			w.logger.Warn("FFmpeg trim failed for highlight segment",
				zap.Int("index", i), zap.Error(err))
			continue
		}

		trimmedPaths = append(trimmedPaths, trimmedPath)
		trimmedDurations = append(trimmedDurations, segDuration)
		validSegments = append(validSegments, seg)
		totalDuration += segDuration

		w.logger.Info("Highlight segment trimmed",
			zap.Int("index", i),
			zap.String("type", seg.Type),
			zap.Float64("start", segStartSecs),
			zap.Float64("end", segEndSecs),
			zap.Float64("trim_offset", trimStart),
			zap.String("reason", seg.Reason))
	}

	if len(trimmedPaths) == 0 {
		w.logger.Error("No highlight segments could be extracted",
			zap.String("session_id", p.SessionID))

		failedResult := &db.HighlightResult{
			SessionID: p.SessionID,
			Status:    "FAILED",
			Segments:  analysisResult.HighlightSegments,
			Output:    "No highlight segments could be extracted from chunk videos",
		}
		if p.ProfileID > 0 {
			failedResult.ProfileID = &p.ProfileID
		}
		w.DB.Create(failedResult)
		return fmt.Errorf("no segments extracted: %w", asynq.SkipRetry)
	}

	// 7. Group segments into different versions
	type highlightGroup struct {
		Title   string
		Prefix  string
		Indices []int
	}

	groups := []highlightGroup{
		{Title: "Highlight Reel", Prefix: "full"},
		{Title: "Best Forms", Prefix: "best"},
		{Title: "Areas for Improvement", Prefix: "improvement"},
		{Title: "Key Moments", Prefix: "key"},
	}

	for i, seg := range validSegments {
		groups[0].Indices = append(groups[0].Indices, i)
		switch seg.Type {
		case "best_form":
			groups[1].Indices = append(groups[1].Indices, i)
		case "worst_form", "fatigue_point":
			groups[2].Indices = append(groups[2].Indices, i)
		case "key_moment":
			groups[3].Indices = append(groups[3].Indices, i)
		}
	}

	// 7b. Generate workout music once for the whole session (best-effort, reused across groups)
	musicPath := tryGenerateMusic(ctx, w.logger, w, p, validSegments, tmpDir)
	musicGCSURI := ""
	if musicPath != "" {
		randSuffix := randomHex(4)
		musicObjName := fmt.Sprintf("highlights/%s_music_%s.mp3", p.SessionID, randSuffix)
		if uri, uploadErr := w.StorageClient.UploadFromFile(ctx, musicPath, musicObjName); uploadErr == nil {
			musicGCSURI = uri
			w.logger.Info("Music track uploaded", zap.String("gcs_uri", musicGCSURI))
		} else {
			w.logger.Warn("Failed to upload music track", zap.Error(uploadErr))
		}
	}

	var generatedCount int
	for _, group := range groups {
		if len(group.Indices) == 0 {
			continue
		}

		concatListPath := filepath.Join(tmpDir, fmt.Sprintf("hl_concat_%s.txt", group.Prefix))
		var concatEntries []string
		var groupSegments []HighlightSegment
		var groupDuration float64

		for _, idx := range group.Indices {
			concatEntries = append(concatEntries, fmt.Sprintf("file '%s'", trimmedPaths[idx]))
			groupSegments = append(groupSegments, validSegments[idx])
			groupDuration += trimmedDurations[idx]
		}

		if err := os.WriteFile(concatListPath, []byte(strings.Join(concatEntries, "\n")), 0o644); err != nil {
			w.logger.Error("Failed to write concat list", zap.Error(err))
			continue
		}

		rawConcatPath := filepath.Join(tmpDir, fmt.Sprintf("hl_raw_%s_%s.mp4", group.Prefix, p.SessionID))
		if err := runFFmpegConcat(ctx, w.logger, concatListPath, rawConcatPath); err != nil {
			w.logger.Error("Failed to concat highlight group", zap.String("group", group.Title), zap.Error(err))
			continue
		}

		// Apply cinematic polish (color grade + fades + watermark + music mix)
		polishedPath := filepath.Join(tmpDir, fmt.Sprintf("hl_polished_%s_%s.mp4", group.Prefix, p.SessionID))
		uploadPath := rawConcatPath // fallback to raw if polish fails
		if err := runFFmpegFinalPolish(ctx, w.logger, rawConcatPath, musicPath, polishedPath, groupDuration); err != nil {
			w.logger.Warn("FFmpeg polish failed, uploading raw concat instead",
				zap.String("group", group.Title), zap.Error(err))
		} else {
			uploadPath = polishedPath
		}

		randSuffix := randomHex(4)
		gcsObjectName := fmt.Sprintf("highlights/%s_hl_%s_%s.mp4", p.SessionID, group.Prefix, randSuffix)
		gcsURI, err := w.StorageClient.UploadFromFile(ctx, uploadPath, gcsObjectName)
		if err != nil {
			w.logger.Error("Failed to upload highlight", zap.String("group", group.Title), zap.Error(err))
			continue
		}

		segsJSON, _ := json.Marshal(groupSegments)

		result := &db.HighlightResult{
			SessionID:   p.SessionID,
			Title:       group.Title,
			Status:      "COMPLETED",
			GCSURI:      gcsURI,
			MusicGCSURI: musicGCSURI,
			Segments:    string(segsJSON),
			DurationSec: groupDuration,
			Output:      fmt.Sprintf("Generated %d highlight segments (%.1fs total) for %s", len(group.Indices), groupDuration, group.Title),
		}
		if p.ProfileID > 0 {
			result.ProfileID = &p.ProfileID
		}
		w.DB.Create(result)
		generatedCount++

		w.logger.Info("Highlight video version generated",
			zap.String("session_id", p.SessionID),
			zap.String("title", group.Title),
			zap.String("gcs_uri", gcsURI),
			zap.String("music_gcs_uri", musicGCSURI),
			zap.Int("segment_count", len(group.Indices)),
			zap.Float64("duration", groupDuration))
	}

	if generatedCount == 0 {
		return fmt.Errorf("failed to generate any highlight groups")
	}

	return nil
}

// findChunkForTimestamp finds the index of the chunk object that covers the given
// timestamp in seconds. It uses chunk analysis results to map time ranges to chunks.
// Falls back to positional mapping if chunk analysis data is incomplete.
func findChunkForTimestamp(chunks []db.ChunkAnalysisResult, timestampSecs float64) int {
	// Try to find by chunk analysis time ranges
	for i, ch := range chunks {
		if ch.StartSecs != nil && ch.EndSecs != nil {
			if timestampSecs >= *ch.StartSecs && timestampSecs < *ch.EndSecs {
				return i
			}
		}
	}

	// Fallback: estimate by dividing evenly
	if len(chunks) == 0 {
		return -1
	}

	// Find the last chunk's end time to estimate total duration
	var maxEndSecs float64
	for _, ch := range chunks {
		if ch.EndSecs != nil && *ch.EndSecs > maxEndSecs {
			maxEndSecs = *ch.EndSecs
		}
	}
	if maxEndSecs <= 0 {
		return -1
	}

	// Estimate which chunk by position
	chunkDuration := maxEndSecs / float64(len(chunks))
	idx := int(timestampSecs / chunkDuration)
	if idx >= len(chunks) {
		idx = len(chunks) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return idx
}

// buildMusicPrompt constructs a Lyria 3 text prompt from this session's highlight segments.
// It infers workout intensity from the distribution of segment types.
func buildMusicPrompt(segments []HighlightSegment) string {
	var bestCount, worstCount, fatigueCount, keyCount int
	for _, s := range segments {
		switch s.Type {
		case "best_form":
			bestCount++
		case "worst_form":
			worstCount++
		case "fatigue_point":
			fatigueCount++
		case "key_moment":
			keyCount++
		}
	}

	intensity := "high"
	bpm := "140-150"
	if fatigueCount == 0 && worstCount == 0 {
		intensity = "moderate"
		bpm = "120-130"
	}

	return fmt.Sprintf(
		"A 30-second high-energy CrossFit workout music track for a highlight reel. "+
			"Instrumental only, no vocals. BPM %s. Intensity: %s. "+
			"Driving, motivating, electronic/EDM style with strong beats. "+
			"Suitable for a social media sports highlight video. "+
			"Best form segments: %d, fatigue points: %d, key moments: %d.",
		bpm, intensity, bestCount, fatigueCount, keyCount,
	)
}

// tryGenerateMusic calls Lyria 3 Clip to generate workout music for this session.
// Returns the local path to the generated MP3, or empty string if generation fails.
// Errors are logged but never propagated — music is best-effort.
func tryGenerateMusic(ctx context.Context, log *zap.Logger, w *Worker, p HighlightPayload, segments []HighlightSegment, tmpDir string) string {
	prompt := buildMusicPrompt(segments)
	musicPath := filepath.Join(tmpDir, "music.mp3")

	log.Info("Generating workout music via Lyria 3 Clip",
		zap.String("session_id", p.SessionID),
		zap.String("prompt", prompt))

	if err := w.GeminiClient.GenerateWorkoutMusic(ctx, "lyria-3-clip-preview", prompt, musicPath); err != nil {
		log.Warn("Music generation failed, continuing without music",
			zap.String("session_id", p.SessionID),
			zap.Error(err))
		return ""
	}

	log.Info("Music generation completed",
		zap.String("session_id", p.SessionID),
		zap.String("music_path", musicPath))
	return musicPath
}

// runFFmpegFinalPolish applies cinematic post-processing to a highlight video.
func runFFmpegFinalPolish(ctx context.Context, log *zap.Logger, inputPath, musicPath, outputPath string, durationSecs float64) error {
	// Slow-motion doubles the duration; adjust fade-out accordingly.
	slowDuration := durationSecs * 2.0
	fadeOutStart := slowDuration - 0.5
	if fadeOutStart < 0 {
		fadeOutStart = 0
	}

	vf := fmt.Sprintf(
		"setpts=2.0*PTS,"+
			"minterpolate=fps=60:mi_mode=mci:mc_mode=aobmc:vsbmc=1,"+
			"fade=t=in:st=0:d=0.5,"+
			"fade=t=out:st=%.3f:d=0.5,"+
			"eq=contrast=1.08:brightness=0.02:saturation=1.15:gamma=1.05,"+
			"drawtext=text='WOD Strategist':font=sans:fontsize=18:"+
			"fontcolor=white@0.45:x=w-tw-12:y=h-th-12",
		fadeOutStart,
	)

	var args []string

	if musicPath != "" {
		afMusic := fmt.Sprintf(
			"[1:a]volume=0.20,afade=t=in:ss=0:d=0.5,afade=t=out:st=%.3f:d=0.5[amusic];"+
				"[0:a][amusic]amix=inputs=2:duration=first:dropout_transition=2[aout]",
			fadeOutStart,
		)
		args = []string{
			"-i", inputPath,
			"-i", musicPath,
			"-filter_complex", afMusic,
			"-vf", vf,
			"-map", "0:v",
			"-map", "[aout]",
			"-c:v", "libx264", "-preset", "fast", "-crf", "23",
			"-c:a", "aac", "-b:a", "128k",
			"-movflags", "+faststart",
			"-y", outputPath,
		}
	} else {
		args = []string{
			"-i", inputPath,
			"-vf", vf,
			"-map", "0:v",
			"-map", "0:a?",
			"-c:v", "libx264", "-preset", "fast", "-crf", "23",
			"-c:a", "aac", "-b:a", "128k",
			"-movflags", "+faststart",
			"-y", outputPath,
		}
	}

	log.Info("Running FFmpeg final polish", zap.Strings("args", args))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("FFmpeg polish failed", zap.Error(err), zap.String("output", string(out)))
		return fmt.Errorf("ffmpeg polish: %s: %w", string(out), err)
	}

	log.Info("FFmpeg polish completed", zap.String("output_path", outputPath))
	return nil
}

// runFFmpegTrim extracts a clip from inputPath starting at startSecs for durationSecs.
func runFFmpegTrim(ctx context.Context, log *zap.Logger, inputPath, outputPath string, startSecs, durationSecs float64) error {
	args := []string{
		"-ss", fmt.Sprintf("%.3f", startSecs),
		"-i", inputPath,
		"-t", fmt.Sprintf("%.3f", durationSecs),
		"-c", "copy",
		"-avoid_negative_ts", "make_zero",
		"-y",
		outputPath,
	}

	log.Info("Running FFmpeg trim", zap.Strings("args", args))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("FFmpeg trim failed", zap.Error(err), zap.String("output", string(output)))
		return fmt.Errorf("ffmpeg trim: %s: %w", string(output), err)
	}

	log.Info("FFmpeg trim completed", zap.String("output_path", outputPath))
	return nil
}
