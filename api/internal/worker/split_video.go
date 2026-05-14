package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
)

const (
	// splitChunkDurationSecs is the target duration for each chunk when splitting
	// uploaded videos. Matches the real-time recording chunk interval.
	splitChunkDurationSecs = 10

	// splitAnalysisConcurrency is the number of chunks analyzed in parallel.
	// Bounded to avoid overwhelming the Gemini API rate limits and memory.
	// With ~18s per chunk and 10 workers, a 23-min video (143 chunks) completes
	// in ~4.3 minutes instead of ~43 minutes serially.
	splitAnalysisConcurrency = 10
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

	// 5. Process each chunk in parallel: upload to GCS → analyze → save DB record
	// Uses a semaphore to bound concurrency and avoid overwhelming Gemini API.
	// NOTE: With the default asynq task timeout of 30 minutes and 10 concurrent
	// workers (~18s per chunk), this supports videos up to ~160 chunks (~27 min).
	// Videos significantly longer than 27 min may still time out and will be
	// resumed on the next retry via the coverage check + idempotent skip logic.
	var wg sync.WaitGroup
	sem := make(chan struct{}, splitAnalysisConcurrency)
	var errCount atomic.Int32
	var skipCount atomic.Int32

	for i, chunkFile := range chunkFiles {
		// Calculate time offsets before spawning goroutine
		startSecs := float64(i * splitChunkDurationSecs)
		chunkPath := filepath.Join(tmpDir, chunkFile)

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

		// Skip chunks that were already analyzed in a previous (partial) run
		if w.chunkAlreadyAnalyzed(p.SessionID, startSecs) {
			skipCount.Add(1)
			os.Remove(chunkPath)
			continue
		}

		wg.Add(1)
		sem <- struct{}{} // acquire semaphore slot

		go func(idx int, path string, start, end float64) {
			defer wg.Done()
			defer func() { <-sem }() // release semaphore slot

			w.logger.Info("Processing split chunk",
				zap.String("session_id", p.SessionID),
				zap.Int("chunk_index", idx),
				zap.Int("total_chunks", len(chunkFiles)),
				zap.Float64("start_secs", start),
				zap.Float64("end_secs", end))

			// Upload chunk to GCS
			objectName := fmt.Sprintf("videos/%d/%s/split_chunk_%03d.mp4", p.ProfileID, p.SessionID, idx)
			gcsURI, uploadErr := w.StorageClient.UploadFromFile(ctx, path, objectName)
			if uploadErr != nil {
				w.logger.Error("Failed to upload split chunk to GCS, skipping",
					zap.Int("chunk_index", idx),
					zap.Error(uploadErr))
				errCount.Add(1)
				os.Remove(path)
				return
			}

			// Run chunk analysis (reuse the same logic as HandleChunkAnalysisTask)
			analysisErr := w.analyzeChunkInline(ctx, path, gcsURI, p, start, end)
			if analysisErr != nil {
				w.logger.Warn("Chunk analysis failed for split chunk, recording as FAILED",
					zap.Int("chunk_index", idx),
					zap.Error(analysisErr))
				// Still record a FAILED entry so the chunk is tracked
				w.saveChunkResult(p, gcsURI, start, end, "FAILED", "", "Analysis failed: "+analysisErr.Error())
				errCount.Add(1)
			}

			// Free the local chunk file immediately to minimize tmpfs usage
			os.Remove(path)
		}(i, chunkPath, startSecs, endSecs)
	}

	wg.Wait()

	if skipped := skipCount.Load(); skipped > 0 {
		w.logger.Info("Skipped already-analyzed chunks",
			zap.String("session_id", p.SessionID),
			zap.Int32("skipped", skipped),
			zap.Int("total_chunks", len(chunkFiles)))
	}

	if failed := errCount.Load(); failed > 0 {
		w.logger.Warn("Some chunks failed during parallel analysis",
			zap.String("session_id", p.SessionID),
			zap.Int32("failed", failed),
			zap.Int("total_chunks", len(chunkFiles)))
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

	analysis, geminiFile, usage, err := w.GeminiClient.AnalyzeVideo(ctx, localPath, prompt)

	// Clean up Gemini file if uploaded
	if geminiFile != "" {
		defer func() {
			if delErr := w.GeminiClient.DeleteFile(ctx, geminiFile); delErr != nil {
				w.logger.Warn("Failed to delete Gemini file for split chunk", zap.Error(delErr))
			}
		}()
	}

	// Save token usage regardless of analysis outcome
	w.saveTokenUsage(p.SessionID, p.ProfileID, "chunk:analysis", usage)

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
	result.ProfileID = p.ProfileID
	w.DB.Create(result)
}

// chunkAlreadyAnalyzed checks if a COMPLETED chunk record already exists
// for the given session and start time. Used to skip re-analysis of chunks
// that were successfully processed in a previous (partial) run.
func (w *Worker) chunkAlreadyAnalyzed(sessionID string, startSecs float64) bool {
	if w.DB == nil {
		return false
	}
	var count int64
	w.DB.Model(&db.ChunkAnalysisResult{}).
		Where("session_id = ? AND start_secs = ? AND status = ?", sessionID, startSecs, "COMPLETED").
		Count(&count)
	return count > 0
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
