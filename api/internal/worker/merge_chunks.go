package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/subtitle"
	"go.uber.org/zap"
)

func NewMergeChunksTask(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint) (*asynq.Task, error) {
	payload := VideoAnalysisPayload{
		SessionID:   sessionID,
		FilePath:    filePath,
		WorkoutType: NormalizeWorkoutType(workoutType),
		Movements:   movements,
		Injuries:    injuries,
		ProfileID:   profileID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeMergeChunks, data), nil
}

func (w *Worker) HandleMergeChunksTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	if err := validateSessionID(p.SessionID); err != nil {
		w.logger.Error("Invalid session ID", zap.String("session_id", p.SessionID))
		return err
	}

	w.logger.Info("Processing merge chunks",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath))

	// 1. Determine chronological chunk order from DB (ordered by start_secs).
	//    Each ChunkAnalysisResult stores the GCS URI in FilePath.
	var chunkRecords []db.ChunkAnalysisResult
	if err := w.DB.Where("session_id = ? AND status = ? AND file_path != ''",
		p.SessionID, "COMPLETED").
		Order("start_secs ASC").
		Find(&chunkRecords).Error; err != nil {
		return fmt.Errorf("failed to query chunk records: %w", err)
	}

	// Build the ordered list of GCS URIs from DB records.
	var objects []string
	if len(chunkRecords) > 0 {
		for _, rec := range chunkRecords {
			objects = append(objects, rec.FilePath)
		}
		w.logger.Info("Chunk order resolved from DB (start_secs)",
			zap.Int("count", len(objects)),
			zap.Strings("objects", objects))
	} else {
		// Fallback: list from GCS and sort alphabetically (legacy chunks without file_path).
		w.logger.Warn("No chunk records with file_path found, falling back to GCS listing",
			zap.String("session_id", p.SessionID))

		_, prefix, err := storage.ParseGCSURI(p.FilePath)
		if err != nil {
			return fmt.Errorf("invalid GCS URI for merge prefix: %w", asynq.SkipRetry)
		}

		listed, err := w.StorageClient.ListObjects(ctx, prefix)
		if err != nil {
			return fmt.Errorf("failed to list chunk objects: %w", err)
		}

		for _, obj := range listed {
			base := filepath.Base(obj)
			if strings.Contains(base, "_merged_") || strings.Contains(base, "_hardsubbed_") || strings.Contains(base, "_encoded_") {
				continue
			}
			objects = append(objects, fmt.Sprintf("gs://%s/%s", w.BucketName, obj))
		}
		sort.Strings(objects)
	}

	if len(objects) == 0 {
		w.logger.Warn("No chunk objects found for session", zap.String("session_id", p.SessionID))
		return fmt.Errorf("no chunks found: %w", asynq.SkipRetry)
	}

	w.logger.Info("Chunks to merge", zap.Int("count", len(objects)), zap.Strings("uris", objects))

	// 2. Download all chunks to a temp directory
	tmpDir, err := createTempDir("merge")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	var localChunkPaths []string
	for i, gcsURI := range objects {
		localPath := filepath.Join(tmpDir, fmt.Sprintf("chunk_%03d.mp4", i))
		if err := w.StorageClient.DownloadFile(ctx, gcsURI, localPath); err != nil {
			return fmt.Errorf("failed to download chunk %s: %w", gcsURI, err)
		}
		localChunkPaths = append(localChunkPaths, localPath)
		w.logger.Info("Downloaded chunk", zap.Int("index", i), zap.String("gcs_uri", gcsURI))
	}

	// 3. Create FFmpeg concat file list and merge in a single pass
	concatListPath := filepath.Join(tmpDir, "concat_list.txt")
	var concatEntries []string
	for _, lp := range localChunkPaths {
		concatEntries = append(concatEntries, fmt.Sprintf("file '%s'", lp))
	}
	if err := os.WriteFile(concatListPath, []byte(strings.Join(concatEntries, "\n")), 0o644); err != nil {
		return fmt.Errorf("failed to write concat list: %w", err)
	}

	mergedPath := filepath.Join(tmpDir, fmt.Sprintf("merged_%s.mp4", p.SessionID))
	if err := runFFmpegConcat(ctx, w.logger, concatListPath, mergedPath); err != nil {
		return fmt.Errorf("ffmpeg merge failed: %w", err)
	}

	// Free tmpfs: delete chunk files now that FFmpeg concat is done.
	// In Cloud Run, /tmp is backed by memory (tmpfs), so keeping chunks
	// alongside the merged output doubles peak memory usage.
	for _, lp := range localChunkPaths {
		_ = os.Remove(lp)
	}
	_ = os.Remove(concatListPath)
	w.logger.Info("Freed chunk files from tmpfs after merge",
		zap.Int("count", len(localChunkPaths)))

	// Log merged file size — critical for diagnosing oversized videos
	var mergedSizeBytes int64
	if fi, err := os.Stat(mergedPath); err == nil {
		mergedSizeBytes = fi.Size()
	}
	w.logger.Info("Merged video file ready",
		zap.String("session_id", p.SessionID),
		zap.Int64("merged_size_bytes", mergedSizeBytes),
		zap.String("merged_size_human", formatFileSizeWorker(mergedSizeBytes)))

	// 4. Upload merged file to GCS (user-facing, full quality)
	mergedObjectName := fmt.Sprintf("videos/%d/%s/merged.mp4", p.ProfileID, p.SessionID)
	mergedGCSURI, err := w.StorageClient.UploadFromFile(ctx, mergedPath, mergedObjectName)
	if err != nil {
		return fmt.Errorf("failed to upload merged video: %w", err)
	}

	w.logger.Info("Merged video uploaded", zap.String("gcs_uri", mergedGCSURI))

	// 4.5. Conditional analysis-grade re-encode
	// If the merged video is too large for Gemini (>500MB), create a smaller
	// analysis-grade copy with tighter compression (CRF 28).
	// The user still gets the full-quality merged.mp4 for download/viewing.
	analysisGCSURI := mergedGCSURI
	if mergedSizeBytes > maxAnalysisVideoSizeBytes {
		w.logger.Info("Merged video exceeds analysis threshold, creating analysis-grade copy",
			zap.String("session_id", p.SessionID),
			zap.Int64("merged_size_bytes", mergedSizeBytes),
			zap.Int64("threshold_bytes", maxAnalysisVideoSizeBytes))

		analysisPath := filepath.Join(tmpDir, fmt.Sprintf("analysis_%s.mp4", p.SessionID))
		if err := runFFmpegAnalysisEncode(ctx, w.logger, mergedPath, analysisPath); err != nil {
			w.logger.Warn("Analysis re-encode failed, using original merged file",
				zap.String("session_id", p.SessionID),
				zap.Error(err))
		} else {
			// Log analysis copy size
			var analysisSizeBytes int64
			if fi, err := os.Stat(analysisPath); err == nil {
				analysisSizeBytes = fi.Size()
			}
			w.logger.Info("Analysis-grade copy created",
				zap.String("session_id", p.SessionID),
				zap.Int64("analysis_size_bytes", analysisSizeBytes),
				zap.String("analysis_size_human", formatFileSizeWorker(analysisSizeBytes)),
				zap.Float64("compression_ratio", float64(mergedSizeBytes)/float64(max(analysisSizeBytes, 1))))

			// Upload analysis copy to GCS
			analysisObjectName := fmt.Sprintf("videos/%d/%s/analysis.mp4", p.ProfileID, p.SessionID)
			if analysisURI, err := w.StorageClient.UploadFromFile(ctx, analysisPath, analysisObjectName); err != nil {
				w.logger.Warn("Failed to upload analysis copy, using original merged file",
					zap.String("session_id", p.SessionID),
					zap.Error(err))
			} else {
				analysisGCSURI = analysisURI
				w.logger.Info("Analysis copy uploaded",
					zap.String("session_id", p.SessionID),
					zap.String("analysis_gcs_uri", analysisGCSURI))
			}

			// Free the analysis copy from tmpfs
			_ = os.Remove(analysisPath)
		}
	}

	// 5. Hard-sub: burn chunk analysis subtitles into the merged video.
	//
	// WARNING: Hard-subbing requires full decode → re-encode of every frame.
	// For a 5-min 720p video, expect ~200–400 MB RAM and ~2–5 min CPU time.
	// Uses -preset ultrafast to minimise CPU time at the cost of ~40% larger files.
	// Requires FFmpeg compiled with --enable-libass (subtitles filter).
	//
	// This step is best-effort: if it fails (e.g. missing libass, DB error,
	// insufficient resources), we log the error and proceed without hard-subs.
	// TODO: Consider moving to a separate, resource-heavy worker queue.
	hardSubGCSURI := w.tryHardSub(ctx, p, tmpDir, mergedPath)

	// 6. Enqueue full video analysis on the analysis-grade file (or merged if no re-encode needed)
	analysisTask, err := NewVideoAnalysisTask(p.SessionID, analysisGCSURI, p.WorkoutType, p.Movements, p.Injuries, p.ProfileID)
	if err != nil {
		return fmt.Errorf("failed to create analysis task for merged video: %w", err)
	}

	if _, err := w.QueueClient.Enqueue(analysisTask); err != nil {
		return fmt.Errorf("failed to enqueue analysis task for merged video: %w", err)
	}

	w.logger.Info("Analysis enqueued for merged video",
		zap.String("session_id", p.SessionID),
		zap.String("analysis_uri", analysisGCSURI),
		zap.String("hardsub_uri", hardSubGCSURI))
	return nil
}

// tryHardSub attempts to burn chunk analysis subtitles into the merged video.
// Returns the GCS URI of the hard-subbed video on success, or empty string on
// failure. Errors are logged but never propagated — hard-sub is best-effort.
func (w *Worker) tryHardSub(ctx context.Context, p VideoAnalysisPayload, tmpDir, mergedPath string) string {
	w.logger.Info("Hard-sub: starting",
		zap.String("session_id", p.SessionID),
		zap.String("tmp_dir", tmpDir),
		zap.String("merged_path", mergedPath))

	// Verify merged video exists and is non-empty
	if fi, err := os.Stat(mergedPath); err != nil {
		w.logger.Warn("Hard-sub: merged video file not found, skipping",
			zap.String("merged_path", mergedPath), zap.Error(err))
		return ""
	} else {
		w.logger.Info("Hard-sub: merged video file OK",
			zap.String("merged_path", mergedPath),
			zap.Int64("size_bytes", fi.Size()))
	}

	var chunks []db.ChunkAnalysisResult
	if err := w.DB.Where("session_id = ? AND status = ?", p.SessionID, "COMPLETED").
		Order("start_secs ASC").
		Find(&chunks).Error; err != nil {
		w.logger.Warn("Hard-sub: failed to query chunk analysis, skipping",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return ""
	}

	w.logger.Info("Hard-sub: queried chunk analysis",
		zap.String("session_id", p.SessionID),
		zap.Int("total_chunks", len(chunks)))

	// Log each chunk's details for debugging
	for i, ch := range chunks {
		var startSecs, endSecs float64
		if ch.StartSecs != nil {
			startSecs = *ch.StartSecs
		}
		if ch.EndSecs != nil {
			endSecs = *ch.EndSecs
		}
		outputPreview := ch.Output
		if len(outputPreview) > 80 {
			outputPreview = outputPreview[:80] + "..."
		}
		w.logger.Info("Hard-sub: chunk detail",
			zap.Int("index", i),
			zap.String("status", ch.Status),
			zap.Float64("start_secs", startSecs),
			zap.Float64("end_secs", endSecs),
			zap.Bool("has_start", ch.StartSecs != nil),
			zap.Bool("has_end", ch.EndSecs != nil),
			zap.Int("output_len", len(ch.Output)),
			zap.String("output_preview", outputPreview))
	}

	srt := subtitle.FormatSRT(chunks)
	if srt == "" {
		w.logger.Info("Hard-sub: no subtitle content, skipping",
			zap.String("session_id", p.SessionID),
			zap.Int("total_chunks", len(chunks)))
		return ""
	}

	// Log SRT preview for debugging (first 200 chars)
	srtPreview := srt
	if len(srtPreview) > 200 {
		srtPreview = srtPreview[:200] + "..."
	}
	w.logger.Info("Hard-sub: generated SRT",
		zap.String("session_id", p.SessionID),
		zap.Int("srt_length", len(srt)),
		zap.String("srt_preview", srtPreview))

	srtPath := filepath.Join(tmpDir, "feedback.srt")
	if err := os.WriteFile(srtPath, []byte(srt), 0o644); err != nil {
		w.logger.Warn("Hard-sub: failed to write SRT file, skipping", zap.Error(err))
		return ""
	}

	// Verify SRT file was written correctly
	if fi, err := os.Stat(srtPath); err != nil {
		w.logger.Warn("Hard-sub: SRT file stat failed after write",
			zap.String("srt_path", srtPath), zap.Error(err))
		return ""
	} else {
		w.logger.Info("Hard-sub: SRT file written OK",
			zap.String("srt_path", srtPath),
			zap.Int64("size_bytes", fi.Size()))
	}

	hardSubPath := filepath.Join(tmpDir, fmt.Sprintf("hardsubbed_%s.mp4", p.SessionID))

	w.logger.Info("Hard-sub: starting FFmpeg",
		zap.String("input", mergedPath),
		zap.String("srt", srtPath),
		zap.String("output", hardSubPath))

	// Log resource usage before and after for observability
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	start := time.Now()

	if err := runFFmpegHardSub(ctx, w.logger, mergedPath, srtPath, hardSubPath); err != nil {
		w.logger.Warn("Hard-sub: FFmpeg failed, skipping",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return ""
	}

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	elapsed := time.Since(start)

	// Verify output file exists and is non-empty
	var hardSubSizeBytes int64
	if fi, err := os.Stat(hardSubPath); err != nil {
		w.logger.Warn("Hard-sub: output file not found after FFmpeg",
			zap.String("output_path", hardSubPath), zap.Error(err))
		return ""
	} else {
		hardSubSizeBytes = fi.Size()
		if hardSubSizeBytes == 0 {
			w.logger.Warn("Hard-sub: output file is empty",
				zap.String("output_path", hardSubPath))
			return ""
		}
	}

	w.logger.Info("Hard-sub: FFmpeg completed",
		zap.String("session_id", p.SessionID),
		zap.Duration("duration", elapsed),
		zap.Int64("output_size_bytes", hardSubSizeBytes),
		zap.Uint64("mem_alloc_before_mb", memBefore.Alloc/1024/1024),
		zap.Uint64("mem_alloc_after_mb", memAfter.Alloc/1024/1024),
		zap.Uint64("mem_sys_mb", memAfter.Sys/1024/1024))

	hardSubObjectName := fmt.Sprintf("videos/%d/%s/hardsubbed.mp4", p.ProfileID, p.SessionID)
	w.logger.Info("Hard-sub: uploading to GCS",
		zap.String("object_name", hardSubObjectName),
		zap.Int64("file_size_bytes", hardSubSizeBytes))

	hardSubGCSURI, err := w.StorageClient.UploadFromFile(ctx, hardSubPath, hardSubObjectName)
	if err != nil {
		w.logger.Warn("Hard-sub: failed to upload hard-subbed video, skipping",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return ""
	}

	w.logger.Info("Hard-sub: uploaded",
		zap.String("session_id", p.SessionID),
		zap.String("gcs_uri", hardSubGCSURI))
	return hardSubGCSURI
}

// runFFmpegConcat concatenates video files listed in concatListPath into outputPath.
// Re-encodes to 30 fps / AAC to normalise frame-rate and PTS timestamps across
// chunks, preventing playback-speed artifacts that occur when source chunks are
// recorded at variable or high frame rates (e.g. 60/120 fps slo-mo on iOS).
func runFFmpegConcat(ctx context.Context, log *zap.Logger, concatListPath, outputPath string) error {
	args := []string{
		"-f", "concat",
		"-safe", "0",
		"-i", concatListPath,
		// Normalise to 30 fps so variable-frame-rate chunks play at real speed.
		"-vf", "fps=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-y",
		outputPath,
	}

	log.Info("Running FFmpeg concat (re-encode to 30fps)", zap.Strings("args", args))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("FFmpeg failed", zap.Error(err), zap.String("output", string(output)))
		return fmt.Errorf("ffmpeg: %s: %w", string(output), err)
	}

	log.Info("FFmpeg merge completed", zap.String("output_path", outputPath))
	return nil
}

// runFFmpegHardSub burns subtitles from an SRT file into a video using FFmpeg.
//
// WARNING: This runs a full decode→filter→re-encode pipeline.
// CPU: ~100% single core for realtime-to-3x duration.
// Memory: ~200–400 MB for 720p.
// Uses -preset ultrafast to reduce CPU time at cost of ~40% larger output.
func runFFmpegHardSub(ctx context.Context, log *zap.Logger, inputPath, srtPath, outputPath string) error {
	args := []string{
		"-i", inputPath,
		"-vf", fmt.Sprintf("subtitles=%s", srtPath),
		"-preset", "ultrafast",
		"-c:a", "copy",
		"-y",
		outputPath,
	}

	log.Info("Running FFmpeg hard-sub", zap.Strings("args", args))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("FFmpeg hard-sub failed", zap.Error(err), zap.String("output", string(output)))
		return fmt.Errorf("ffmpeg hardsub: %s: %w", string(output), err)
	}

	log.Info("FFmpeg hard-sub completed", zap.String("output_path", outputPath))
	return nil
}
