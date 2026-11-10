package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/storage"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	mergeChunksTaskTimeout        = time.Hour
	mergeChunksUniqueTTL          = 15 * time.Minute
	maxMergeChunkPrepConcurrency  = 4
	leadingBlackProbeWindowSecs   = 0.35
	minLeadingBlackTrimSecs       = 0.015
	maxLeadingBlackTrimSecs       = 0.20
	minChunkDurationForTrimSecs   = 1.0
	canonicalConcatVideoTimescale = 30000
)

var blackDetectLineRegex = regexp.MustCompile(`black_start:([0-9.]+)\s+black_end:([0-9.]+)`)

type concatMediaInterval struct {
	FilePath string
	Start    float64
	End      float64
}

type chunkStreamProfile struct {
	VideoCodec     string
	PixFmt         string
	Width          int
	Height         int
	Rotation       int
	DisplayWidth   int
	DisplayHeight  int
	VideoTimeBase  string
	VideoTimescale int
	HasAudio       bool
	AudioCodec     string
	SampleRate     int
	Channels       int
	VideoIndex     int
	AudioIndex     int
	VideoCount     int
	AudioCount     int
	AudioTimeBase  string
	ChannelLayout  string
	StreamOrder    string
}

func NewMergeChunksTask(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint, enableTTS bool, wodDescription string) (*asynq.Task, error) {
	payload := VideoAnalysisPayload{
		SessionID:        sessionID,
		MergeRequestedAt: time.Now().UTC().Truncate(time.Minute),
		FilePath:         filePath,
		WorkoutType:      NormalizeWorkoutType(workoutType),
		Movements:        movements,
		Injuries:         injuries,
		ProfileID:        profileID,
		EnableTTS:        enableTTS,
		WODDescription:   wodDescription,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeMergeChunks, data, asynq.Timeout(mergeChunksTaskTimeout), asynq.Unique(mergeChunksUniqueTTL)), nil
}

func (w *Worker) handleMergeChunksTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	if err := validateSessionID(p.SessionID); err != nil {
		w.logger.Error("Invalid session ID", zap.String("session_id", p.SessionID))
		return err
	}
	profileID, err := w.resolveVideoAnalysisProfile(ctx, p.SessionID, p.ProfileID)
	if err != nil {
		return err
	}
	p.ProfileID = profileID
	p.WODDescription = w.resolveSessionWODDescription(ctx, p.SessionID, p.ProfileID, p.WODDescription)

	w.logger.Info("Processing merge chunks",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath))

	// 1. List all original chunks from GCS to determine what files actually exist.
	gcsChunks, err := w.listOriginalChunks(ctx, p.FilePath)
	if err != nil {
		return fmt.Errorf("failed to list original chunks: %w", err)
	}

	if len(gcsChunks) == 0 {
		w.logger.Warn("No chunk objects found for session", zap.String("session_id", p.SessionID))
		return fmt.Errorf("no chunks found: %w", asynq.SkipRetry)
	}

	// 2. Query completed or failed chunk records from DB to ensure all are processed.
	var objects []string
	if w.DB != nil {
		var dbRecords []db.ChunkAnalysisResult
		if err := w.DB.Where("session_id = ? AND profile_id = ? AND status IN ? AND file_path NOT LIKE ?",
			p.SessionID, p.ProfileID, []string{"COMPLETED", "FAILED"}, "%/split_chunk_%").
			Order("start_secs ASC NULLS LAST, file_path ASC, id ASC").
			Find(&dbRecords).Error; err != nil {
			return fmt.Errorf("failed to query chunk records: %w", err)
		}

		// Check if all listed GCS chunks have completed/failed analysis records in DB.
		dbMap := make(map[string]db.ChunkAnalysisResult)
		for _, rec := range dbRecords {
			dbMap[rec.FilePath] = rec
		}

		var pendingChunks []string
		for _, chunkURI := range gcsChunks {
			if _, exists := dbMap[chunkURI]; !exists {
				pendingChunks = append(pendingChunks, chunkURI)
			}
		}

		if len(pendingChunks) > 0 {
			if err := w.resolvePendingChunks(ctx, p, pendingChunks); err != nil {
				return err
			}
			if err := w.DB.Where("session_id = ? AND profile_id = ? AND status IN ? AND file_path NOT LIKE ?",
				p.SessionID, p.ProfileID, []string{"COMPLETED", "FAILED"}, "%/split_chunk_%").
				Order("start_secs ASC NULLS LAST, file_path ASC, id ASC").Find(&dbRecords).Error; err != nil {
				return fmt.Errorf("failed to re-query chunk records: %w", err)
			}
		}
		objects, err = orderedMergeChunks(gcsChunks, dbRecords)
		if err != nil {
			failed := &db.AnalysisResult{SessionID: p.SessionID, ProfileID: p.ProfileID,
				Status: "FAILED", AnalysisType: db.AnalysisTypeWOD,
				Output: "촬영 순서를 확인할 수 없어 동영상을 병합하지 못했습니다. 원본 영상을 다시 업로드해주세요."}
			if saveErr := w.persistVideoAnalysisFailed(ctx, p, failed); saveErr != nil {
				return fmt.Errorf("persist merge failure: %w", saveErr)
			}
			return fmt.Errorf("%v: %w", err, asynq.SkipRetry)
		}

		w.logger.Info("Chunk order resolved from DB (start_secs)",
			zap.Int("count", len(objects)),
			zap.Strings("objects", objects))
	} else {
		// Fallback: list from GCS and sort alphabetically if DB is not available.
		w.logger.Warn("DB is not configured, falling back to GCS listing and alphabetical merge",
			zap.String("session_id", p.SessionID))
		objects = gcsChunks
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

	mergedPath := filepath.Join(tmpDir, fmt.Sprintf("merged_%s.mp4", p.SessionID))
	mediaIntervals, intervalErr, err := mergeDownloadedChunks(ctx, w.logger, tmpDir, objects, localChunkPaths, mergedPath)
	if err != nil {
		return fmt.Errorf("ffmpeg merge failed: %w", err)
	}
	if intervalErr != nil {
		return fmt.Errorf("could not verify merged-media chunk intervals: %w", intervalErr)
	}
	if err := w.persistConcatMediaIntervals(ctx, p.SessionID, p.ProfileID, mediaIntervals); err != nil {
		// Persistence remains additive: only verified media reaches this point.
		// Missing DB offsets stay unavailable rather than using capture time.
		w.logger.Warn("Could not persist merged-media chunk intervals",
			zap.String("session_id", p.SessionID), zap.Error(err))
	}

	// Free tmpfs: delete chunk files now that FFmpeg concat is done.
	// In Cloud Run, /tmp is backed by memory (tmpfs), so keeping chunks
	// alongside the merged output doubles peak memory usage.
	for _, lp := range localChunkPaths {
		_ = os.Remove(lp)
	}
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

	// 4.25. Hard duration guardrail: keep merged.mp4 playable in GCS, but skip
	// expensive analysis re-encode, post-merge activity review, and full-session
	// Gemini analysis when the merged video exceeds maxAnalysisVideoDuration.
	mergedDurationSecs := probeVideoDuration(ctx, mergedPath)
	if len(mediaIntervals) > 0 && mediaIntervals[len(mediaIntervals)-1].End > mergedDurationSecs {
		mergedDurationSecs = mediaIntervals[len(mediaIntervals)-1].End
	}
	if mergedDurationSecs > maxAnalysisVideoDuration.Seconds() {
		w.logger.Error("Merged video duration exceeds maximum analysis limit; skipping AI analysis and re-encode",
			zap.String("session_id", p.SessionID),
			zap.Float64("duration_secs", mergedDurationSecs),
			zap.Float64("max_duration_secs", maxAnalysisVideoDuration.Seconds()),
			zap.String("merged_gcs_uri", mergedGCSURI))

		activityReviewErr := w.skipOverlongActivityReview(ctx, p, mergedGCSURI)

		if w.DB != nil {
			resolvedProfileID, profErr := w.resolveVideoAnalysisProfile(ctx, p.SessionID, p.ProfileID)
			if profErr != nil {
				return profErr
			}
			persistPayload := p
			persistPayload.ProfileID = resolvedProfileID
			failedResult := &db.AnalysisResult{
				SessionID:       p.SessionID,
				ProfileID:       resolvedProfileID,
				Status:          "FAILED",
				Output:          videoTooLongUserMessage,
				AnalysisType:    db.AnalysisTypeWOD,
				AvailableVideos: db.CommaStringArray{"merged"},
			}
			persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if dbErr := w.persistVideoAnalysisFailed(persistCtx, persistPayload, failedResult); dbErr != nil {
				return fmt.Errorf("failed to persist FAILED analysis result for overlong merged video: %w", dbErr)
			}
		}

		return activityReviewErr
	}

	// Counting is dispatched independently of the full analysis outcome.
	activityReviewErr := w.enqueueActivityReview(ctx, p, mergedGCSURI)

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

	// 5. Enqueue full video analysis on the analysis-grade file (or merged if no re-encode needed).
	// Hardsub generation is deferred: it will be auto-enqueued after the full
	// analysis completes, so it can burn the more accurate final analysis
	// feedback (strengths + weaknesses) instead of real-time chunk cues.
	analysisTask, err := NewVideoAnalysisTask(p.SessionID, analysisGCSURI, p.WorkoutType, p.Movements, p.Injuries, p.ProfileID, p.EnableTTS, p.WODDescription)
	if err != nil {
		return fmt.Errorf("failed to create analysis task for merged video: %w", err)
	}

	if p.PipelineMode != "" {
		var payload VideoAnalysisPayload
		if err := json.Unmarshal(analysisTask.Payload(), &payload); err == nil {
			payload.PipelineMode = p.PipelineMode
			if data, err := json.Marshal(payload); err == nil {
				analysisTask = asynq.NewTask(analysisTask.Type(), data, asynq.Timeout(videoAnalysisTaskTimeout), asynq.Unique(videoAnalysisTaskTimeout))
			}
		}
	}

	if _, err := w.QueueClient.Enqueue(analysisTask); err != nil {
		if errors.Is(err, asynq.ErrDuplicateTask) {
			w.logger.Info("Analysis task already queued for merged video",
				zap.String("session_id", p.SessionID),
				zap.String("analysis_uri", analysisGCSURI))
			return activityReviewErr
		}
		return fmt.Errorf("failed to enqueue analysis task for merged video: %w", err)
	}

	w.logger.Info("Analysis enqueued for merged video",
		zap.String("session_id", p.SessionID),
		zap.String("analysis_uri", analysisGCSURI))
	return activityReviewErr
}

type preparedConcatChunk struct {
	normPath  string
	duration  float64
	signature videoFrameSignature
}

func mergeChunkPrepConcurrency(totalChunks int) int {
	if totalChunks <= 1 {
		return 1
	}
	workers := max(2, runtime.GOMAXPROCS(0))
	workers = min(workers, maxMergeChunkPrepConcurrency)
	return min(workers, totalChunks)
}

// mergeDownloadedChunks prepares every input once on its presented-video timeline.
// Only verified canonical video is copied into the merge; audio is encoded once
// more at the final boundary to reconcile per-file AAC priming safely.
func mergeDownloadedChunks(ctx context.Context, log *zap.Logger, tmpDir string, objects, localChunkPaths []string, mergedPath string) ([]concatMediaInterval, error, error) {
	if len(objects) == 0 || len(objects) != len(localChunkPaths) {
		return nil, nil, errors.New("concat source list is incomplete")
	}
	profiles, err := probeAllChunkStreamProfiles(ctx, localChunkPaths)
	if err != nil {
		return nil, nil, fmt.Errorf("probe concat source profiles: %w", err)
	}
	targetW, targetH, includeAudio := concatPreparationSettings(profiles)
	prepared, err := prepareAllChunksForConcat(ctx, log, tmpDir, localChunkPaths, profiles, targetW, targetH, includeAudio)
	if err != nil {
		return nil, nil, err
	}
	intervals := make([]concatMediaInterval, 0, len(objects))
	signatures := make([]videoFrameSignature, 0, len(objects))
	concatEntries := make([]string, 0, len(objects)*2)
	cumulative := 0.0
	for i, prep := range prepared {
		signatures = append(signatures, prep.signature)
		intervals = append(intervals, concatMediaInterval{FilePath: objects[i], Start: cumulative, End: cumulative + prep.duration})
		cumulative += prep.duration
		concatEntries = append(concatEntries,
			fmt.Sprintf("file '%s'", strings.ReplaceAll(prep.normPath, "'", "'\\''")),
			fmt.Sprintf("duration %.9f", prep.duration))
	}
	concatListPath := filepath.Join(tmpDir, "concat_list.txt")
	defer os.Remove(concatListPath)
	if err := os.WriteFile(concatListPath, []byte(strings.Join(concatEntries, "\n")), 0o644); err != nil {
		return nil, nil, fmt.Errorf("write concat list: %w", err)
	}
	if err := runFFmpegStreamCopyConcat(ctx, log, concatListPath, mergedPath, cumulative, includeAudio); err != nil {
		return nil, nil, err
	}
	if err := verifyVideoFrameSequence(ctx, mergedPath, signatures); err != nil {
		return nil, nil, fmt.Errorf("verify merged frame sequence: %w", err)
	}
	if err := verifyMergedDurationMatchesIntervals(ctx, mergedPath, intervals); err != nil {
		return nil, nil, err
	}
	return intervals, nil, nil
}

func prepareAllChunksForConcat(ctx context.Context, log *zap.Logger, tmpDir string, localChunkPaths []string, profiles []chunkStreamProfile, targetW, targetH int, includeAudio bool) ([]preparedConcatChunk, error) {
	prepared := make([]preparedConcatChunk, len(localChunkPaths))
	workers := mergeChunkPrepConcurrency(len(localChunkPaths))
	if workers <= 1 {
		for i, srcPath := range localChunkPaths {
			prep, err := prepareChunkForConcat(ctx, log, tmpDir, i, srcPath, profiles[i], targetW, targetH, includeAudio)
			if err != nil {
				return nil, err
			}
			prepared[i] = prep
			localChunkPaths[i] = prep.normPath
		}
		return prepared, nil
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int, len(localChunkPaths))
	for i := range localChunkPaths {
		jobs <- i
	}
	close(jobs)

	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if workCtx.Err() != nil {
					return
				}
				prep, err := prepareChunkForConcat(workCtx, log, tmpDir, i, localChunkPaths[i], profiles[i], targetW, targetH, includeAudio)
				if err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				prepared[i] = prep
				localChunkPaths[i] = prep.normPath
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return prepared, nil
}

func prepareChunkForConcat(ctx context.Context, log *zap.Logger, tmpDir string, index int, srcPath string, profile chunkStreamProfile, targetW, targetH int, includeAudio bool) (preparedConcatChunk, error) {
	timeline, err := probeVideoTimeline(ctx, srcPath)
	if err != nil {
		return preparedConcatChunk{}, fmt.Errorf("probe original chunk %d timeline: %w", index, err)
	}
	trimSecs, err := probeLeadingBlackTrimSecs(ctx, srcPath, profile, timeline)
	if err != nil {
		return preparedConcatChunk{}, fmt.Errorf("probe leading black on chunk %d: %w", index, err)
	}
	return prepareChunkForConcatWithEvidence(ctx, log, tmpDir, index, srcPath, profile, targetW, targetH, includeAudio, timeline, trimSecs)
}

func prepareChunkForConcatWithEvidence(ctx context.Context, log *zap.Logger, tmpDir string, index int, srcPath string, profile chunkStreamProfile, targetW, targetH int, includeAudio bool, timeline videoTimeline, trimSecs float64) (preparedConcatChunk, error) {
	if trimSecs > 0 {
		log.Info("Removing leading black from chunk", zap.Int("chunk_index", index), zap.Float64("trim_start_secs", trimSecs))
	}
	normPath := filepath.Join(tmpDir, fmt.Sprintf("norm_chunk_%03d.mp4", index))
	if err := normalizeChunkForConcat(ctx, log, srcPath, normPath, profile, targetW, targetH, includeAudio, timeline, trimSecs); err != nil {
		_ = os.Remove(normPath)
		return preparedConcatChunk{}, fmt.Errorf("normalize chunk %d: %w", index, err)
	}
	preparedProfile, err := probeChunkStreamProfile(ctx, normPath)
	if err != nil {
		_ = os.Remove(normPath)
		return preparedConcatChunk{}, fmt.Errorf("probe prepared chunk %d: %w", index, err)
	}
	if preparedProfile.VideoCodec != "h264" || preparedProfile.PixFmt != "yuv420p" ||
		preparedProfile.VideoTimeBase != "1/30000" || preparedProfile.Width != targetW || preparedProfile.Height != targetH ||
		preparedProfile.Rotation != 0 || preparedProfile.VideoIndex != 0 || preparedProfile.HasAudio != includeAudio ||
		includeAudio && (preparedProfile.AudioIndex != 1 || preparedProfile.AudioCodec != "aac" || preparedProfile.AudioTimeBase != "1/44100" || preparedProfile.SampleRate != 44100 || preparedProfile.Channels != 2) {
		_ = os.Remove(normPath)
		return preparedConcatChunk{}, fmt.Errorf("prepared chunk %d does not match the canonical concat profile", index)
	}
	expectedDuration := timeline.Duration() - trimSecs
	expectedTolerance := 1.0/30 + timeline.Tick
	if err := validateDurationExpectation(expectedDuration, expectedTolerance); err != nil {
		_ = os.Remove(normPath)
		return preparedConcatChunk{}, fmt.Errorf("verify original-to-prepared chunk %d: %w", index, err)
	}
	preparedTimeline, err := probeVideoTimeline(ctx, normPath)
	if err != nil {
		_ = os.Remove(normPath)
		return preparedConcatChunk{}, fmt.Errorf("verify original-to-prepared chunk %d: %w", index, err)
	}
	if err := verifyTimelineDuration(preparedTimeline, expectedDuration, expectedTolerance); err != nil {
		_ = os.Remove(normPath)
		return preparedConcatChunk{}, fmt.Errorf("verify original-to-prepared chunk %d: %w", index, err)
	}
	signature, err := readVideoFrameSignature(ctx, normPath)
	if err != nil {
		_ = os.Remove(normPath)
		return preparedConcatChunk{}, fmt.Errorf("verify prepared chunk %d frames: %w", index, err)
	}
	duration := signature.End - signature.Start
	if signature.FrameCount <= 0 || signature.Start < -1.0/canonicalConcatVideoTimescale || math.Abs(signature.Start) > 1.0/canonicalConcatVideoTimescale || duration <= 0 {
		_ = os.Remove(normPath)
		return preparedConcatChunk{}, fmt.Errorf("prepared chunk %d has invalid zero-based video timeline", index)
	}
	if err := verifyMediaEndpointsWithTimeline(ctx, normPath, preparedTimeline, duration, 2.0/30+1.0/canonicalConcatVideoTimescale); err != nil {
		_ = os.Remove(normPath)
		return preparedConcatChunk{}, fmt.Errorf("verify prepared chunk %d endpoints: %w", index, err)
	}
	_ = os.Remove(srcPath)
	return preparedConcatChunk{
		normPath:  normPath,
		duration:  duration,
		signature: signature,
	}, nil
}

func verifyMergedDurationMatchesIntervals(ctx context.Context, mergedPath string, intervals []concatMediaInterval) error {
	if len(intervals) == 0 {
		return errors.New("no verified concat intervals")
	}
	if err := verifyMediaEndpoints(ctx, mergedPath, intervals[len(intervals)-1].End, 2.0/30+1.0/canonicalConcatVideoTimescale); err != nil {
		return fmt.Errorf("merged video diverges from cumulative chunk duration: %w", err)
	}
	return nil
}

func probeAllChunkStreamProfiles(ctx context.Context, localPaths []string) ([]chunkStreamProfile, error) {
	profiles := make([]chunkStreamProfile, len(localPaths))
	workers := mergeChunkPrepConcurrency(len(localPaths))
	if workers <= 1 {
		for i, lp := range localPaths {
			profile, err := probeChunkStreamProfile(ctx, lp)
			if err != nil {
				return nil, fmt.Errorf("chunk %d: %w", i, err)
			}
			profiles[i] = profile
		}
		return profiles, nil
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int, len(localPaths))
	for i := range localPaths {
		jobs <- i
	}
	close(jobs)

	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if workCtx.Err() != nil {
					return
				}
				profile, err := probeChunkStreamProfile(workCtx, localPaths[i])
				if err != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("chunk %d: %w", i, err)
						cancel()
					})
					return
				}
				profiles[i] = profile
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return profiles, nil
}

func probeChunkStreamProfile(ctx context.Context, filePath string) (chunkStreamProfile, error) {
	var doc struct {
		Streams []struct {
			Index         int    `json:"index"`
			CodecType     string `json:"codec_type"`
			CodecName     string `json:"codec_name"`
			PixFmt        string `json:"pix_fmt"`
			Width         int    `json:"width"`
			Height        int    `json:"height"`
			TimeBase      string `json:"time_base"`
			SampleRate    string `json:"sample_rate"`
			Channels      int    `json:"channels"`
			ChannelLayout string `json:"channel_layout"`
			Disposition   struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
			Tags     map[string]string `json:"tags"`
			SideData []struct {
				Rotation float64 `json:"rotation"`
			} `json:"side_data_list"`
		} `json:"streams"`
	}
	if _, err := runMediaExec(ctx, "probe_chunk_stream_profile", func(execCtx context.Context) error {
		cmd := exec.CommandContext(execCtx, "ffprobe",
			"-threads", mediaDecoderThreads(execCtx), "-v", "error", "-show_streams", "-of", "json", filePath)
		return consumeMediaOutput(cmd, func(r io.Reader) error { return json.NewDecoder(r).Decode(&doc) })
	}); err != nil {
		return chunkStreamProfile{}, fmt.Errorf("probe chunk streams: %w", err)
	}

	profile := chunkStreamProfile{VideoIndex: -1, AudioIndex: -1}
	var streamOrder []string
	foundVideo := false
	for _, s := range doc.Streams {
		switch s.CodecType {
		case "video":
			if s.Disposition.AttachedPic != 0 {
				continue
			}
			profile.VideoCount++
			if foundVideo {
				return chunkStreamProfile{}, errors.New("multiple video streams are ambiguous")
			}
			streamOrder = append(streamOrder, "video")
			profile.VideoIndex = s.Index
			foundVideo = true
			profile.VideoCodec = strings.ToLower(strings.TrimSpace(s.CodecName))
			profile.PixFmt = strings.ToLower(strings.TrimSpace(s.PixFmt))
			profile.Width = s.Width
			profile.Height = s.Height
			profile.VideoTimeBase = strings.ToLower(strings.TrimSpace(s.TimeBase))
			profile.VideoTimescale = parseStreamTimescale(s.TimeBase)

			rotDeg := 0.0
			for _, sd := range s.SideData {
				if sd.Rotation != 0 {
					rotDeg = sd.Rotation
					break
				}
			}
			if rotDeg == 0 && s.Tags != nil {
				if rawRot, ok := s.Tags["rotate"]; ok {
					if parsed, parseErr := strconv.ParseFloat(strings.TrimSpace(rawRot), 64); parseErr == nil {
						rotDeg = parsed
					}
				}
			}
			rot := ((int(math.Round(rotDeg)) % 360) + 360) % 360
			profile.Rotation = rot
			if rot == 90 || rot == 270 {
				profile.DisplayWidth = s.Height
				profile.DisplayHeight = s.Width
			} else {
				profile.DisplayWidth = s.Width
				profile.DisplayHeight = s.Height
			}
		case "audio":
			profile.AudioCount++
			if profile.HasAudio {
				return chunkStreamProfile{}, errors.New("multiple audio streams are ambiguous")
			}
			streamOrder = append(streamOrder, "audio")
			profile.AudioIndex = s.Index
			profile.AudioTimeBase = strings.TrimSpace(s.TimeBase)
			profile.ChannelLayout = strings.ToLower(strings.TrimSpace(s.ChannelLayout))
			profile.HasAudio = true
			profile.AudioCodec = strings.ToLower(strings.TrimSpace(s.CodecName))
			if sr, parseErr := strconv.Atoi(strings.TrimSpace(s.SampleRate)); parseErr == nil {
				profile.SampleRate = sr
			}
			profile.Channels = s.Channels
		}
	}

	if !foundVideo || profile.VideoCodec == "" || profile.Width <= 0 || profile.Height <= 0 {
		return chunkStreamProfile{}, errors.New("no valid video stream found")
	}
	profile.StreamOrder = strings.Join(streamOrder, ",")
	if !validStreamTimeBase(profile.VideoTimeBase) {
		return chunkStreamProfile{}, errors.New("video stream has invalid time_base")
	}
	if profile.HasAudio && (profile.AudioCodec == "" || profile.SampleRate <= 0 || profile.Channels <= 0 || !validStreamTimeBase(profile.AudioTimeBase)) {
		return chunkStreamProfile{}, errors.New("audio stream has invalid codec, sample rate, channels, or time_base")
	}
	return profile, nil
}

func validStreamTimeBase(timeBase string) bool {
	parts := strings.Split(strings.TrimSpace(timeBase), "/")
	if len(parts) != 2 {
		return false
	}
	num, nerr := strconv.ParseInt(parts[0], 10, 64)
	den, derr := strconv.ParseInt(parts[1], 10, 64)
	tick, terr := mediaTimeBase(strings.TrimSpace(timeBase))
	return nerr == nil && derr == nil && num > 0 && den > 0 && terr == nil && tick > 0 && !math.IsNaN(tick) && !math.IsInf(tick, 0)
}

func parseStreamTimescale(timeBase string) int {
	parts := strings.Split(strings.TrimSpace(timeBase), "/")
	if len(parts) != 2 {
		return 0
	}
	num, errNum := strconv.Atoi(strings.TrimSpace(parts[0]))
	den, errDen := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errNum != nil || errDen != nil || num != 1 || den <= 0 {
		return 0
	}
	return den
}

// chunksNeedNormalization identifies heterogeneous inputs for the existing
// resolution policy. Every input is prepared independently even when homogeneous.
// Mixed inputs use an upright resolution capped at 1280px and all inputs share
// a stereo 44.1kHz AAC track when any source contains audio.
func chunksNeedNormalization(profiles []chunkStreamProfile) (bool, int, int, bool) {
	if len(profiles) <= 1 {
		includeAudio := len(profiles) == 1 && profiles[0].HasAudio
		return false, 0, 0, includeAudio
	}

	base := profiles[0]
	homogeneous := base.VideoCodec != "" && base.DisplayWidth > 0 && base.DisplayHeight > 0
	includeAudio := base.HasAudio

	for _, p := range profiles[1:] {
		if p.HasAudio {
			includeAudio = true
		}
		if p.VideoCodec == "" || p.DisplayWidth <= 0 || p.DisplayHeight <= 0 {
			homogeneous = false
			continue
		}
		if p.VideoCodec != base.VideoCodec ||
			p.PixFmt != base.PixFmt ||
			p.Width != base.Width ||
			p.Height != base.Height ||
			p.Rotation != base.Rotation ||
			p.VideoTimeBase != base.VideoTimeBase ||
			p.VideoTimescale != base.VideoTimescale ||
			p.HasAudio != base.HasAudio {
			homogeneous = false
			continue
		}
		if p.HasAudio && (p.AudioCodec != base.AudioCodec || p.SampleRate != base.SampleRate || p.Channels != base.Channels) {
			homogeneous = false
		}
	}

	if homogeneous {
		return false, 0, 0, includeAudio
	}

	targetW, targetH := normalizedTargetDimensions(profiles)
	return true, targetW, targetH, includeAudio
}

// concatPreparationSettings preserves the pre-repair resolution policy. New
// clock/order/layout checks affect mapping and verification, not video quality:
// every input is already prepared independently before concat.
func concatPreparationSettings(profiles []chunkStreamProfile) (int, int, bool) {
	heterogeneous, width, height, includeAudio := chunksNeedNormalization(profiles)
	if !heterogeneous && len(profiles) > 0 {
		width = evenDimension(profiles[0].DisplayWidth)
		height = evenDimension(profiles[0].DisplayHeight)
	}
	return width, height, includeAudio
}

func normalizedTargetDimensions(profiles []chunkStreamProfile) (int, int) {
	if len(profiles) == 0 {
		return 720, 1280
	}
	base := profiles[0]
	primaryPortrait := base.DisplayHeight >= base.DisplayWidth
	maxW, maxH := 0, 0
	for _, p := range profiles {
		if p.DisplayWidth <= 0 || p.DisplayHeight <= 0 {
			continue
		}
		if (p.DisplayHeight >= p.DisplayWidth) != primaryPortrait {
			continue
		}
		if p.DisplayWidth > maxW {
			maxW = p.DisplayWidth
		}
		if p.DisplayHeight > maxH {
			maxH = p.DisplayHeight
		}
	}
	if maxW <= 0 || maxH <= 0 {
		maxW, maxH = base.DisplayWidth, base.DisplayHeight
	}
	if maxW <= 0 || maxH <= 0 {
		maxW, maxH = 720, 1280
	}

	const maxNormalizedLongEdge = 1280
	longEdge := max(maxW, maxH)
	if longEdge > maxNormalizedLongEdge {
		scale := float64(maxNormalizedLongEdge) / float64(longEdge)
		maxW = int(math.Round(float64(maxW) * scale))
		maxH = int(math.Round(float64(maxH) * scale))
	}

	return evenDimension(maxW), evenDimension(maxH)
}

func evenDimension(v int) int {
	if v < 2 {
		return 2
	}
	return (v + 1) &^ 1
}

func parseLeadingBlackTrimSecs(ffmpegOutput string) float64 {
	m := blackDetectLineRegex.FindStringSubmatch(ffmpegOutput)
	if len(m) != 3 {
		return 0
	}
	blackStart, errStart := strconv.ParseFloat(m[1], 64)
	blackEnd, errEnd := strconv.ParseFloat(m[2], 64)
	if errStart != nil || errEnd != nil {
		return 0
	}
	if blackStart > 0.005 || blackEnd < minLeadingBlackTrimSecs || blackEnd > maxLeadingBlackTrimSecs {
		return 0
	}
	return math.Max(0.01, blackEnd-0.001)
}

func probeLeadingBlackTrimSecs(ctx context.Context, inputPath string, profile chunkStreamProfile, timeline videoTimeline) (float64, error) {
	if timeline.Duration() < minChunkDurationForTrimSecs {
		return 0, nil
	}
	var output []byte
	_, err := runMediaExec(ctx, "probe_leading_black", func(execCtx context.Context) error {
		var runErr error
		output, runErr = exec.CommandContext(execCtx, "ffmpeg",
			"-nostdin", "-xerror", "-v", "info", "-threads", mediaDecoderThreads(execCtx),
			"-i", inputPath,
			"-map", fmt.Sprintf("0:%d", profile.VideoIndex),
			"-t", fmt.Sprintf("%.3f", leadingBlackProbeWindowSecs),
			"-vf", "setpts=PTS-STARTPTS,blackdetect=d=0.015:pix_th=0.05:pic_th=0.98",
			"-an", "-sn", "-dn", "-f", "null", "-",
		).CombinedOutput()
		return runErr
	})
	if err != nil {
		return 0, fmt.Errorf("ffmpeg leading black probe: %s: %w", string(output), err)
	}
	return parseLeadingBlackTrimSecs(string(output)), nil
}

func normalizeChunkForConcat(ctx context.Context, log *zap.Logger, inputPath, outputPath string, profile chunkStreamProfile, targetW, targetH int, includeAudio bool, timeline videoTimeline, trimStartSecs float64) error {
	anchor := timeline.Start + trimStartSecs
	duration := timeline.End - anchor
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return errors.New("normalization has no retained video interval")
	}
	vf := fmt.Sprintf("trim=start=%.9f:end=%.9f,setpts=PTS-%.9f/TB,fps=30:start_time=0,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,format=yuv420p",
		anchor, timeline.End, anchor, targetW, targetH, targetW, targetH)
	_, err := runMediaExec(ctx, "normalize_chunk_concat", func(execCtx context.Context) error {
		args := []string{"-v", "error", "-nostdin", "-xerror", "-copyts", "-i", inputPath}
		if includeAudio && !profile.HasAudio {
			args = append(args, "-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=44100")
		}
		args = append(args, "-map", fmt.Sprintf("0:%d", profile.VideoIndex),
			"-vf", vf, "-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
			"-video_track_timescale", strconv.Itoa(canonicalConcatVideoTimescale),
			"-metadata:s:v:0", "rotate=0")
		if includeAudio {
			af := fmt.Sprintf("atrim=start=%.9f:end=%.9f,asetpts=PTS-%.9f/TB,aresample=44100:async=1:first_pts=0,apad,atrim=duration=%.9f", anchor, timeline.End, anchor, duration)
			if profile.HasAudio {
				args = append(args, "-map", fmt.Sprintf("0:%d", profile.AudioIndex))
			} else {
				args = append(args, "-map", "1:a:0")
				af = fmt.Sprintf("atrim=duration=%.9f,asetpts=PTS-STARTPTS", duration)
			}
			args = append(args, "-af", af, "-c:a", "aac", "-ar", "44100", "-ac", "2", "-b:a", "128k")
		} else {
			args = append(args, "-an")
		}
		if mediaThreadsLimited(execCtx) {
			args = append(args, "-threads", "1")
		}
		args = append(args, "-sn", "-dn", "-map_metadata", "-1", "-movflags", "+faststart", "-y", outputPath)
		args = limitMediaCommandThreads(execCtx, args)
		return consumeMediaOutput(exec.CommandContext(execCtx, "ffmpeg", args...), func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
	})
	if err != nil {
		log.Error("FFmpeg chunk normalization failed", zap.String("input_path", inputPath), zap.Error(err))
		return fmt.Errorf("ffmpeg normalize: %w", err)
	}
	return nil
}

func runFFmpegStreamCopyConcat(ctx context.Context, log *zap.Logger, concatListPath, outputPath string, expectedDuration float64, includeAudio bool) error {
	_, err := runMediaExec(ctx, "stream_copy_concat", func(execCtx context.Context) error {
		args := []string{"-v", "error", "-nostdin", "-xerror", "-copyts", "-f", "concat", "-safe", "0", "-i", concatListPath,
			"-map", "0:v:0", "-c:v", "copy", "-video_track_timescale", strconv.Itoa(canonicalConcatVideoTimescale)}
		if includeAudio {
			args = append(args, "-map", "0:a:0", "-af", fmt.Sprintf("aresample=44100:async=1:first_pts=0,apad,atrim=duration=%.9f", expectedDuration),
				"-c:a", "aac", "-ar", "44100", "-ac", "2", "-b:a", "128k")
		} else {
			args = append(args, "-an")
		}
		if mediaThreadsLimited(execCtx) {
			args = append(args, "-threads", "1")
		}
		args = append(args, "-sn", "-dn", "-movflags", "+faststart", "-y", outputPath)
		args = limitMediaCommandThreads(execCtx, args)
		log.Info("Running FFmpeg concat (copy video, reconcile AAC boundaries)", zap.Strings("args", args))
		return consumeMediaOutput(exec.CommandContext(execCtx, "ffmpeg", args...), func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
	})
	if err != nil {
		log.Error("FFmpeg concat failed", zap.Error(err))
		return fmt.Errorf("ffmpeg concat: %w", err)
	}
	return nil
}

func (w *Worker) persistConcatMediaIntervals(ctx context.Context, sessionID string, profileID uint, intervals []concatMediaInterval) error {
	if w.DB == nil {
		return errors.New("database is not configured")
	}
	if len(intervals) == 0 {
		return errors.New("no verified concat intervals")
	}

	return w.withSessionPublication(ctx, profileID, sessionID, func(tx *gorm.DB) error {
		for _, interval := range intervals {
			result := tx.Model(&db.ChunkAnalysisResult{}).
				Where("session_id = ? AND profile_id = ? AND file_path = ? AND file_path NOT LIKE ?",
					sessionID, profileID, interval.FilePath, "%/split_chunk_%").
				Updates(map[string]any{
					"media_start_secs": interval.Start,
					"media_end_secs":   interval.End,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return fmt.Errorf("concat source row not found for %s", interval.FilePath)
			}
		}
		return nil
	})
}

// runFFmpegHardSub burns subtitles from an SRT file into a video using FFmpeg.
//
// WARNING: This runs a full decode→filter→re-encode pipeline.
// CPU: ~100% single core for realtime-to-3x duration.
// Memory: ~200–400 MB for 720p.
// Uses -preset ultrafast to reduce CPU time at cost of ~40% larger output.
func runFFmpegHardSub(ctx context.Context, log *zap.Logger, inputPath, srtPath, outputPath string) error {
	source, err := probeVideoTimeline(ctx, inputPath)
	if err != nil {
		return fmt.Errorf("probe hard-sub source: %w", err)
	}
	// Subtitle style: modern semi-transparent look
	// - FontName=Arial: clean sans-serif
	// - FontSize=12: compact, non-intrusive
	// - PrimaryColour=&H00FFFFFF: white text
	// - OutlineColour=&H80000000: semi-transparent black outline
	// - BackColour=&HB0000000: dark semi-transparent shadow/background
	// - BorderStyle=4: opaque box behind text
	// - Outline=1: thin outline for readability
	// - Shadow=0: no offset shadow (box handles it)
	// - MarginV=30: lift from absolute bottom edge
	// - Alignment=2: bottom-center
	forceStyle := "FontName=Arial,FontSize=12,PrimaryColour=&H00FFFFFF," +
		"OutlineColour=&H80000000,BackColour=&HB0000000," +
		"BorderStyle=4,Outline=1,Shadow=0,MarginV=30,Alignment=2"

	_, err = runMediaExec(ctx, "hardsub_encode", func(execCtx context.Context) error {
		args := []string{
			"-i", inputPath,
			"-map", "0:V:0", "-map", "0:a:0?",
			"-vf", fmt.Sprintf("subtitles=%s:force_style='%s'", srtPath, forceStyle),
			"-preset", "ultrafast",
			"-c:a", "copy",
			"-movflags", "+faststart",
		}
		if mediaThreadsLimited(execCtx) {
			args = append(args, "-threads", "1")
		}
		args = append(args, "-y", outputPath)
		args = append([]string{"-v", "error", "-nostdin", "-xerror"}, limitMediaCommandThreads(execCtx, args)...)
		log.Info("Running FFmpeg hard-sub", zap.Strings("args", args))
		cmd := exec.CommandContext(execCtx, "ffmpeg", args...)
		return consumeMediaOutput(cmd, func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
	})
	if err != nil {
		log.Error("FFmpeg hard-sub failed", zap.Error(err))
		return fmt.Errorf("ffmpeg hardsub: %w", err)
	}

	if err := verifyVideoDuration(ctx, outputPath, source.Duration(), 0.1); err != nil {
		return fmt.Errorf("verify hard-sub video: %w", err)
	}
	log.Info("FFmpeg hard-sub completed", zap.String("output_path", outputPath))
	return nil
}

// listOriginalChunks lists the original chunk video files from GCS,
// filtering out any output files or server-side split files.
func (w *Worker) listOriginalChunks(ctx context.Context, filePath string) ([]string, error) {
	_, prefix, err := storage.ParseGCSURI(filePath)
	if err != nil {
		return nil, fmt.Errorf("invalid GCS URI for merge prefix: %w", asynq.SkipRetry)
	}

	// Ensure prefix ends with a slash to avoid matching similar session prefixes (e.g. session-1 matching session-10)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	listed, err := w.StorageClient.ListObjects(ctx, prefix)
	if err != nil {
		return nil, err
	}

	var chunks []string
	for _, obj := range listed {
		if strings.HasPrefix(strings.TrimPrefix(obj, prefix), "derived/") {
			continue
		}
		base := filepath.Base(obj)
		// Exclude non-video session artifacts (e.g. sensor telemetry).
		switch strings.ToLower(filepath.Ext(base)) {
		case ".mp4", ".mov":
		default:
			continue
		}
		// Exclude output files from previous merge/analysis runs.
		if strings.Contains(base, "_merged_") || strings.Contains(base, "_hardsubbed_") || strings.Contains(base, "_encoded_") {
			continue
		}
		if base == "merged.mp4" || base == "hardsubbed.mp4" || base == "analysis.mp4" {
			continue
		}
		if strings.HasPrefix(base, "split_chunk_") {
			continue
		}
		if strings.HasPrefix(base, "hl_") {
			continue
		}
		chunks = append(chunks, fmt.Sprintf("gs://%s/%s", w.BucketName, obj))
	}
	sort.Strings(chunks)
	return chunks, nil
}
