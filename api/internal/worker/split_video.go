package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
)

const (
	// splitChunkDurationSecs is the target duration for each chunk when splitting
	// uploaded videos. Matches the real-time recording chunk interval.
	splitChunkDurationSecs = 10
)

// splitAndAnalyzeChunks splits a downloaded video into ~10-second segments,
// uploads each to GCS, and runs chunk analysis synchronously on each.
// This simulates the real-time recording flow for uploaded videos, providing
// accurate segment data for the two-pass deep analysis.
//
// The chunks are stored at: videos/{profileId}/{sessionId}/split_chunk_{NNN}.mp4
// Each chunk gets a ChunkAnalysisResult record with correct start_secs/end_secs.
func (w *Worker) splitAndAnalyzeChunks(ctx context.Context, videoPath string, p VideoAnalysisPayload) error {
	// 1. Probe total video duration
	totalDuration := probeVideoDuration(ctx, videoPath)
	if totalDuration <= 0 {
		return fmt.Errorf("failed to probe video duration or video is empty")
	}

	w.logger.Info("Splitting uploaded video into chunks",
		zap.String("session_id", p.SessionID),
		zap.Float64("total_duration_secs", totalDuration),
		zap.Int("chunk_duration_secs", splitChunkDurationSecs))

	// 2. Create temp directory for split chunks
	tmpDir, err := createTempDir("split")
	if err != nil {
		return fmt.Errorf("failed to create temp dir for splitting: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 3. Run FFmpeg segment split
	chunkPattern := filepath.Join(tmpDir, "chunk_%03d.mp4")
	if err := runFFmpegSplit(ctx, w.logger, videoPath, chunkPattern, splitChunkDurationSecs); err != nil {
		return fmt.Errorf("ffmpeg split failed: %w", err)
	}

	// 4. Discover produced chunks (sorted by name for chronological order)
	chunkFiles, err := discoverChunkFiles(tmpDir)
	if err != nil {
		return fmt.Errorf("failed to discover chunk files: %w", err)
	}

	if len(chunkFiles) == 0 {
		return fmt.Errorf("ffmpeg split produced no chunk files")
	}

	w.logger.Info("FFmpeg split completed",
		zap.String("session_id", p.SessionID),
		zap.Int("chunk_count", len(chunkFiles)))

	// 5. Process each chunk: upload to GCS → analyze → save DB record
	for i, chunkFile := range chunkFiles {
		chunkPath := filepath.Join(tmpDir, chunkFile)

		// Calculate time offsets
		startSecs := float64(i * splitChunkDurationSecs)
		// Probe actual chunk duration for accurate end_secs
		chunkDuration := probeVideoDuration(ctx, chunkPath)
		endSecs := startSecs + chunkDuration
		if chunkDuration <= 0 {
			// Fallback: estimate from chunk index
			endSecs = startSecs + float64(splitChunkDurationSecs)
			if endSecs > totalDuration {
				endSecs = totalDuration
			}
		}

		w.logger.Info("Processing split chunk",
			zap.String("session_id", p.SessionID),
			zap.Int("chunk_index", i),
			zap.Int("total_chunks", len(chunkFiles)),
			zap.Float64("start_secs", startSecs),
			zap.Float64("end_secs", endSecs))

		// Upload chunk to GCS
		objectName := fmt.Sprintf("videos/%d/%s/split_chunk_%03d.mp4", p.ProfileID, p.SessionID, i)
		gcsURI, uploadErr := w.StorageClient.UploadFromFile(ctx, chunkPath, objectName)
		if uploadErr != nil {
			w.logger.Error("Failed to upload split chunk to GCS, skipping",
				zap.Int("chunk_index", i),
				zap.Error(uploadErr))
			continue
		}

		// Run chunk analysis synchronously (reuse the same logic as HandleChunkAnalysisTask)
		analysisErr := w.analyzeChunkInline(ctx, chunkPath, gcsURI, p, startSecs, endSecs)
		if analysisErr != nil {
			w.logger.Warn("Chunk analysis failed for split chunk, recording as FAILED",
				zap.Int("chunk_index", i),
				zap.Error(analysisErr))
			// Still record a FAILED entry so the chunk is tracked
			w.saveChunkResult(p, gcsURI, startSecs, endSecs, "FAILED", "", "Analysis failed: "+analysisErr.Error())
		}

		// Free the local chunk file immediately to minimize tmpfs usage
		os.Remove(chunkPath)
	}

	return nil
}

// analyzeChunkInline runs chunk analysis on a local file synchronously.
// This mirrors the core logic of HandleChunkAnalysisTask but without
// the queue/unmarshal overhead — the file is already local.
func (w *Worker) analyzeChunkInline(ctx context.Context, localPath, gcsURI string, p VideoAnalysisPayload, startSecs, endSecs float64) error {
	prompt := w.buildChunkAnalysisPrompt(VideoAnalysisPayload{
		SessionID:   p.SessionID,
		FilePath:    gcsURI,
		WorkoutType: p.WorkoutType,
		Movements:   p.Movements,
		Injuries:    p.Injuries,
		ProfileID:   p.ProfileID,
		StartSecs:   startSecs,
		EndSecs:     endSecs,
	})

	analysis, geminiFile, err := w.GeminiClient.AnalyzeVideo(ctx, localPath, prompt)

	// Clean up Gemini file if uploaded
	if geminiFile != "" {
		defer func() {
			if delErr := w.GeminiClient.DeleteFile(ctx, geminiFile); delErr != nil {
				w.logger.Warn("Failed to delete Gemini file for split chunk", zap.Error(delErr))
			}
		}()
	}

	if err != nil {
		return fmt.Errorf("gemini analysis failed: %w", err)
	}

	if analysis == "" {
		return fmt.Errorf("gemini returned empty analysis")
	}

	// Extract exercise type and clean output (same as HandleChunkAnalysisTask)
	detectedExercise := parseChunkExercise(analysis)
	cleanOutput := stripExerciseTag(analysis)

	w.saveChunkResult(p, gcsURI, startSecs, endSecs, "COMPLETED", detectedExercise, cleanOutput)

	w.logger.Info("Split chunk analysis completed",
		zap.String("session_id", p.SessionID),
		zap.String("detected_exercise", detectedExercise),
		zap.Float64("start_secs", startSecs),
		zap.Float64("end_secs", endSecs))

	return nil
}

// saveChunkResult persists a ChunkAnalysisResult to the database.
func (w *Worker) saveChunkResult(p VideoAnalysisPayload, gcsURI string, startSecs, endSecs float64, status, exerciseType, output string) {
	result := &db.ChunkAnalysisResult{
		SessionID:    p.SessionID,
		FilePath:     gcsURI,
		ExerciseType: exerciseType,
		Status:       status,
		Output:       output,
		StartSecs:    &startSecs,
		EndSecs:      &endSecs,
	}
	if p.ProfileID > 0 {
		result.ProfileID = &p.ProfileID
	}
	w.DB.Create(result)
}

// runFFmpegSplit uses FFmpeg's segment muxer to split a video into chunks
// at keyframe boundaries. Uses -c copy for fast, lossless splitting.
func runFFmpegSplit(ctx context.Context, log *zap.Logger, inputPath, outputPattern string, segmentDuration int) error {
	args := []string{
		"-i", inputPath,
		"-c", "copy",
		"-f", "segment",
		"-segment_time", fmt.Sprintf("%d", segmentDuration),
		"-reset_timestamps", "1",
		"-y",
		outputPattern,
	}

	log.Info("Running FFmpeg segment split", zap.Strings("args", args))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("FFmpeg segment split failed",
			zap.Error(err),
			zap.String("output", string(output)))
		return fmt.Errorf("ffmpeg segment: %s: %w", string(output), err)
	}

	log.Info("FFmpeg segment split completed")
	return nil
}

// discoverChunkFiles returns sorted chunk filenames from the given directory.
// Only files matching the chunk_*.mp4 pattern are included.
func discoverChunkFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var chunks []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "chunk_") && strings.HasSuffix(name, ".mp4") {
			chunks = append(chunks, name)
		}
	}

	sort.Strings(chunks)
	return chunks, nil
}
