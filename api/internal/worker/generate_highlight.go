package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// HighlightPayload is the payload for the highlight:generate task.
type HighlightPayload struct {
	SessionID   string
	ProfileID   uint
	MaxDuration int // max highlight duration in seconds (default 60)
}

type highlightClip struct {
	Segment   HighlightSegment
	StartSecs float64
	EndSecs   float64
}

func (c highlightClip) duration() float64 {
	return c.EndSecs - c.StartSecs
}

type highlightReelGroup struct {
	Title   string
	Prefix  string
	Indices []int
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

func (w *Worker) handleGenerateHighlightTask(ctx context.Context, t *asynq.Task) error {
	var p HighlightPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	if err := validateSessionID(p.SessionID); err != nil {
		w.logger.Error("Invalid session ID", zap.String("session_id", p.SessionID))
		return err
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
	if p.MaxDuration <= 0 {
		p.MaxDuration = 60
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
	if analysisResult.ProfileID == 0 {
		return fmt.Errorf("completed analysis has no profile_id: %w", asynq.SkipRetry)
	}
	if p.ProfileID != 0 && p.ProfileID != analysisResult.ProfileID {
		w.logger.Warn("Highlight task profile differs from completed analysis; using analysis owner",
			zap.Uint("task_profile_id", p.ProfileID),
			zap.Uint("analysis_profile_id", analysisResult.ProfileID))
	}
	p.ProfileID = analysisResult.ProfileID

	// Validate the stored JSON before doing any storage work.
	decodedSegments, err := decodeHighlightSegmentArray(analysisResult.HighlightSegments)
	if err != nil {
		w.logger.Error("Failed to parse highlight segments",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return fmt.Errorf("invalid highlight segments JSON: %w", asynq.SkipRetry)
	}
	if len(decodedSegments) == 0 {
		return fmt.Errorf("no highlight segments: %w", asynq.SkipRetry)
	}

	// 2. Download the canonical merged-media source exactly once. Parent event
	// timestamps are media-relative and must never be mapped through legacy
	// capture-clock chunk offsets.
	tmpDir, err := createTempDir("highlight")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	mergedGCSURI := fmt.Sprintf("gs://%s/videos/%d/%s/merged.mp4", w.BucketName, p.ProfileID, p.SessionID)
	mergedLocalPath := filepath.Join(tmpDir, "merged.mp4")
	if err := w.StorageClient.DownloadFile(ctx, mergedGCSURI, mergedLocalPath); err != nil {
		w.logger.Error("Failed to download merged video for highlights",
			zap.String("gcs_uri", mergedGCSURI), zap.Error(err))
		return fmt.Errorf("failed to download merged highlight source: %w", err)
	}
	sourceTimeline, err := probeVideoTimeline(ctx, mergedLocalPath)
	if err != nil {
		return fmt.Errorf("probe merged highlight source: %w", err)
	}
	videoDuration := sourceTimeline.Duration()
	sourceProfile, err := probeChunkStreamProfile(ctx, mergedLocalPath)
	if err != nil {
		return fmt.Errorf("probe merged highlight streams: %w", err)
	}

	// 3. Normalize both legacy flat segments and v2 parent events. This is a
	// read-time conversion only; the source analysis row is intentionally not
	// rewritten or backfilled.
	segments, err := NormalizeHighlightSegmentsJSON(analysisResult.HighlightSegments, HighlightNormalizeOptions{
		VideoEndSeconds: videoDuration,
	})
	if err != nil {
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

	clips := buildHighlightClips(segments)
	groups := buildHighlightGroups(clips, float64(p.MaxDuration))
	selectedIndices := selectedHighlightIndices(clips, groups)
	normalizedSegmentsJSON := MarshalHighlightSegments(segments)

	if len(selectedIndices) == 0 {
		w.logger.Error("No highlight segments fit the reel duration budget",
			zap.String("session_id", p.SessionID),
			zap.Int("max_duration", p.MaxDuration))

		failedResult := &db.HighlightResult{
			SessionID: p.SessionID,
			ProfileID: p.ProfileID,
			Status:    "FAILED",
			Segments:  normalizedSegmentsJSON,
			Output:    "No highlight segments fit the reel duration budget",
		}
		if persistErr := w.withSessionPublication(ctx, p.ProfileID, p.SessionID, func(tx *gorm.DB) error { return tx.Create(failedResult).Error }); persistErr != nil {
			return persistErr
		}
		return fmt.Errorf("no segments selected: %w", asynq.SkipRetry)
	}

	// 4. Trim each selected parent event once, even when it appears in multiple
	// thematic reels. Exact child observations remain metadata inside the event.
	trimmedPaths := make(map[int]string, len(selectedIndices))
	trimmedDurations := make(map[int]float64, len(selectedIndices))
	trimmedFrames := make(map[int]videoFrameSignature, len(selectedIndices))
	hasSourceAudio := sourceProfile.HasAudio
	var validSegments []HighlightSegment
	for _, i := range selectedIndices {
		clip := clips[i]
		trimmedPath := filepath.Join(tmpDir, fmt.Sprintf("trimmed_%03d.mp4", i))
		if err := runFFmpegTrim(ctx, w.logger, mergedLocalPath, trimmedPath, clip.StartSecs, clip.duration()); err != nil {
			w.logger.Warn("FFmpeg trim failed for highlight segment",
				zap.Int("index", i), zap.Error(err))
			continue
		}
		timeline, err := probeVideoTimeline(ctx, trimmedPath)
		if err != nil {
			w.logger.Warn("Failed to verify highlight clip timeline", zap.Int("index", i), zap.Error(err))
			continue
		}
		frames, err := readVideoFrameSignature(ctx, trimmedPath)
		if err != nil {
			w.logger.Warn("Failed to verify highlight clip frames", zap.Int("index", i), zap.Error(err))
			continue
		}

		trimmedPaths[i] = trimmedPath
		trimmedDurations[i] = timeline.Duration()
		trimmedFrames[i] = frames
		validSegments = append(validSegments, clip.Segment)

		w.logger.Info("Highlight segment trimmed",
			zap.Int("index", i),
			zap.String("type", clip.Segment.Type),
			zap.Float64("start", clip.StartSecs),
			zap.Float64("end", clip.EndSecs),
			zap.String("reason", clip.Segment.Reason))
	}

	if len(trimmedPaths) == 0 {
		w.logger.Error("No highlight segments could be extracted",
			zap.String("session_id", p.SessionID))

		failedResult := &db.HighlightResult{
			SessionID: p.SessionID,
			Status:    "FAILED",
			Segments:  normalizedSegmentsJSON,
			Output:    "No highlight segments could be extracted from merged video",
		}
		failedResult.ProfileID = p.ProfileID
		if persistErr := w.withSessionPublication(ctx, p.ProfileID, p.SessionID, func(tx *gorm.DB) error { return tx.Create(failedResult).Error }); persistErr != nil {
			return persistErr
		}
		return fmt.Errorf("no segments extracted: %w", asynq.SkipRetry)
	}

	// 5. Generate workout music once for the selected parent events (best-effort,
	// reused across groups).
	musicPath := tryGenerateMusic(ctx, w.logger, w, p, validSegments, tmpDir)
	musicGCSURI := ""
	if musicPath != "" {
		randSuffix := randomHex(4)
		musicObjName := fmt.Sprintf("videos/%d/%s/hl_music_%s.mp3", p.ProfileID, p.SessionID, randSuffix)
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
		var groupFrames []videoFrameSignature

		for _, idx := range group.Indices {
			trimmedPath, ok := trimmedPaths[idx]
			if !ok {
				continue
			}
			concatEntries = append(concatEntries, fmt.Sprintf("file '%s'\nduration %.9f", trimmedPath, trimmedDurations[idx]))
			groupSegments = append(groupSegments, clips[idx].Segment)
			groupDuration += trimmedDurations[idx]
			groupFrames = append(groupFrames, trimmedFrames[idx])
		}

		if len(concatEntries) == 0 {
			continue
		}

		if err := os.WriteFile(concatListPath, []byte(strings.Join(concatEntries, "\n")), 0o644); err != nil {
			w.logger.Error("Failed to write concat list", zap.Error(err))
			continue
		}

		rawConcatPath := filepath.Join(tmpDir, fmt.Sprintf("hl_raw_%s_%s.mp4", group.Prefix, p.SessionID))
		if err := runFFmpegStreamCopyConcat(ctx, w.logger, concatListPath, rawConcatPath, groupDuration, hasSourceAudio); err != nil {
			w.logger.Error("Failed to concat highlight group", zap.String("group", group.Title), zap.Error(err))
			continue
		}
		if err := verifyVideoFrameSequence(ctx, rawConcatPath, groupFrames); err != nil {
			w.logger.Error("Highlight concat changed prepared video frames", zap.String("group", group.Title), zap.Error(err))
			continue
		}
		if err := verifyMediaEndpoints(ctx, rawConcatPath, groupDuration, 0.1); err != nil {
			w.logger.Error("Highlight concat has an invalid timeline", zap.String("group", group.Title), zap.Error(err))
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
		finalTimeline, err := probeVideoTimeline(ctx, uploadPath)
		if err != nil {
			w.logger.Error("Failed to verify final highlight duration", zap.String("group", group.Title), zap.Error(err))
			continue
		}
		finalDuration := finalTimeline.Duration()

		randSuffix := randomHex(4)
		gcsObjectName := fmt.Sprintf("videos/%d/%s/hl_%s_%s.mp4", p.ProfileID, p.SessionID, group.Prefix, randSuffix)
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
			DurationSec: finalDuration,
			Output:      fmt.Sprintf("Generated %d highlight segments (%.1fs total) for %s", len(groupSegments), finalDuration, group.Title),
			ProfileID:   p.ProfileID,
		}
		if persistErr := w.withSessionPublication(ctx, p.ProfileID, p.SessionID, func(tx *gorm.DB) error { return tx.Create(result).Error }); persistErr != nil {
			return persistErr
		}
		generatedCount++

		w.logger.Info("Highlight video version generated",
			zap.String("session_id", p.SessionID),
			zap.String("title", group.Title),
			zap.String("gcs_uri", gcsURI),
			zap.String("music_gcs_uri", musicGCSURI),
			zap.Int("segment_count", len(groupSegments)),
			zap.Float64("duration", finalDuration))
	}

	if generatedCount == 0 {
		return fmt.Errorf("failed to generate any highlight groups")
	}

	return nil
}

func buildHighlightClips(segments []HighlightSegment) []highlightClip {
	clips := make([]highlightClip, 0, len(segments))
	for _, segment := range segments {
		startSecs, startErr := parseTimestampToSeconds(segment.Start)
		endSecs, endErr := parseTimestampToSeconds(segment.End)
		if startErr != nil || endErr != nil || endSecs <= startSecs {
			continue
		}
		clips = append(clips, highlightClip{
			Segment:   segment,
			StartSecs: startSecs,
			EndSecs:   endSecs,
		})
	}
	return clips
}

func buildHighlightGroups(clips []highlightClip, maxDuration float64) []highlightReelGroup {
	groups := []highlightReelGroup{
		{Title: "Highlight Reel", Prefix: "full"},
		{Title: "Best Forms", Prefix: "best"},
		{Title: "Areas for Improvement", Prefix: "improvement"},
		{Title: "Key Moments", Prefix: "key"},
	}

	for groupIndex := range groups {
		var candidates []int
		for clipIndex, clip := range clips {
			if highlightBelongsToGroup(clip.Segment, groups[groupIndex].Prefix) {
				candidates = append(candidates, clipIndex)
			}
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			return highlightEventBetter(clips[candidates[i]].Segment, clips[candidates[j]].Segment)
		})
		groups[groupIndex].Indices = selectHighlightGroupIndices(clips, candidates, maxDuration)
	}

	return groups
}

func highlightBelongsToGroup(segment HighlightSegment, prefix string) bool {
	switch prefix {
	case "full":
		return true
	case "best":
		return HighlightSegmentHasObservationType(segment, HighlightObservationPositiveForm)
	case "improvement":
		return HighlightSegmentHasObservationType(segment, HighlightObservationFormIssue, HighlightObservationFatigueOnset)
	case "key":
		return segment.Type == "key_moment" ||
			HighlightSegmentHasTag(segment, HighlightTagKeyMoment)
	default:
		return false
	}
}

// selectHighlightGroupIndices applies the duration budget in candidate priority
// order. A candidate that does not fit is skipped so a later shorter event can
// still be selected. Only after selection are clips ordered by media time for
// chronological concatenation.
func selectHighlightGroupIndices(clips []highlightClip, candidates []int, maxDuration float64) []int {
	if maxDuration <= 0 {
		return nil
	}

	selected := make([]int, 0, len(candidates))
	seen := make(map[int]struct{}, len(candidates))
	var totalDuration float64
	for _, index := range candidates {
		if index < 0 || index >= len(clips) {
			continue
		}
		if _, exists := seen[index]; exists {
			continue
		}

		duration := clips[index].duration()
		if duration <= 0 || totalDuration+duration > maxDuration {
			continue
		}

		selected = append(selected, index)
		seen[index] = struct{}{}
		totalDuration += duration
	}

	sort.SliceStable(selected, func(i, j int) bool {
		left := clips[selected[i]]
		right := clips[selected[j]]
		if left.StartSecs == right.StartSecs {
			return left.EndSecs < right.EndSecs
		}
		return left.StartSecs < right.StartSecs
	})
	return selected
}

func selectedHighlightIndices(clips []highlightClip, groups []highlightReelGroup) []int {
	seen := make(map[int]struct{})
	var selected []int
	for _, group := range groups {
		for _, index := range group.Indices {
			if _, exists := seen[index]; exists {
				continue
			}
			seen[index] = struct{}{}
			selected = append(selected, index)
		}
	}

	sort.SliceStable(selected, func(i, j int) bool {
		left := clips[selected[i]]
		right := clips[selected[j]]
		if left.StartSecs == right.StartSecs {
			return left.EndSecs < right.EndSecs
		}
		return left.StartSecs < right.StartSecs
	})
	return selected
}

// buildMusicPrompt constructs a Lyria 3 text prompt from this session's highlight segments.
// It infers workout intensity from the distribution of segment types.
func buildMusicPrompt(segments []HighlightSegment) string {
	var bestCount, worstCount, fatigueCount, keyCount int
	for _, s := range segments {
		if HighlightSegmentHasObservationType(s, HighlightObservationPositiveForm) {
			bestCount++
		}
		if HighlightSegmentHasObservationType(s, HighlightObservationFormIssue) {
			worstCount++
		}
		if HighlightSegmentHasObservationType(s, HighlightObservationFatigueOnset) {
			fatigueCount++
		}
		if s.Type == "key_moment" || HighlightSegmentHasTag(s, HighlightTagKeyMoment) {
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

	usage, err := w.GeminiClient.GenerateWorkoutMusic(ctx, "lyria-3-clip-preview", prompt, musicPath)
	w.saveTokenUsage(p.SessionID, p.ProfileID, "highlight:music", usage)
	if err != nil {
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
	if err := verifyMediaEndpoints(ctx, inputPath, durationSecs, 0.1); err != nil {
		return fmt.Errorf("invalid highlight polish source: %w", err)
	}
	// Keep playback at the source rate so the selected reel stays within its
	// duration budget. Frame interpolation is visual polish only.
	fadeOutStart := durationSecs - 0.5
	if fadeOutStart < 0 {
		fadeOutStart = 0
	}

	vf := fmt.Sprintf(
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
			"-nostdin", "-xerror",
			"-i", inputPath,
			"-i", musicPath,
			"-filter_complex", afMusic,
			"-vf", vf,
			"-map", "0:v:0",
			"-map", "[aout]",
			"-c:v", "libx264", "-preset", "fast", "-crf", "23",
			"-pix_fmt", "yuv420p", "-video_track_timescale", strconv.Itoa(canonicalConcatVideoTimescale),
			"-c:a", "aac", "-b:a", "128k",
			"-ar", "44100", "-ac", "2",
			"-t", fmt.Sprintf("%.6f", durationSecs),
			"-movflags", "+faststart",
			"-y", outputPath,
		}
	} else {
		args = []string{
			"-nostdin", "-xerror",
			"-i", inputPath,
			"-vf", vf,
			"-map", "0:v:0",
			"-map", "0:a:0?",
			"-c:v", "libx264", "-preset", "fast", "-crf", "23",
			"-pix_fmt", "yuv420p", "-video_track_timescale", strconv.Itoa(canonicalConcatVideoTimescale),
			"-c:a", "aac", "-b:a", "128k",
			"-ar", "44100", "-ac", "2",
			"-t", fmt.Sprintf("%.6f", durationSecs),
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
	if err := verifyMediaEndpoints(ctx, outputPath, durationSecs, 0.1); err != nil {
		return fmt.Errorf("highlight polish changed video duration: %w", err)
	}

	log.Info("FFmpeg polish completed", zap.String("output_path", outputPath))
	return nil
}

// runFFmpegTrim extracts a clip from inputPath starting at startSecs for durationSecs.
func runFFmpegTrim(ctx context.Context, log *zap.Logger, inputPath, outputPath string, startSecs, durationSecs float64) error {
	if startSecs < 0 || durationSecs <= 0 || math.IsNaN(startSecs) || math.IsInf(startSecs, 0) || math.IsNaN(durationSecs) || math.IsInf(durationSecs, 0) {
		return fmt.Errorf("invalid highlight interval start %.6f duration %.6f", startSecs, durationSecs)
	}
	args := []string{
		"-nostdin", "-xerror",
		"-ss", fmt.Sprintf("%.6f", startSecs),
		"-i", inputPath,
		"-t", fmt.Sprintf("%.6f", durationSecs),
		"-map", "0:v:0", "-map", "0:a:0?",
		"-vf", "fps=30:start_time=0,format=yuv420p",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
		"-video_track_timescale", strconv.Itoa(canonicalConcatVideoTimescale),
		"-c:a", "aac", "-b:a", "128k", "-ar", "44100", "-ac", "2",
		"-movflags", "+faststart",
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
	if err := verifyMediaEndpoints(ctx, outputPath, durationSecs, 0.1); err != nil {
		return fmt.Errorf("highlight clip does not match requested duration: %w", err)
	}

	log.Info("FFmpeg trim completed", zap.String("output_path", outputPath))
	return nil
}
