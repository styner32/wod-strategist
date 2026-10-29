package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/storage"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	mergeChunksUniqueTTL          = 15 * time.Minute
	leadingBlackProbeWindowSecs   = 0.35
	minLeadingBlackTrimSecs       = 0.015
	maxLeadingBlackTrimSecs       = 0.20
	minChunkDurationForTrimSecs   = 1.0
	canonicalConcatVideoTimescale = 30000
	minChunksForBulkTrimNormalize = 4
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
	return asynq.NewTask(TypeMergeChunks, data, asynq.Unique(mergeChunksUniqueTTL)), nil
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
		// Merging remains backward-compatible if ffprobe cannot verify one source
		// chunk. In that case offsets stay NULL and debug re-analysis must use its
		// exact retained-chunk fallback instead of capture-clock timestamps.
		w.logger.Warn("Could not verify merged-media chunk intervals",
			zap.String("session_id", p.SessionID),
			zap.Error(intervalErr))
	} else {
		if err := w.persistConcatMediaIntervals(ctx, p.SessionID, p.ProfileID, mediaIntervals); err != nil {
			// Do not break the established mobile merge flow for additive debug
			// metadata. The rows remain NULL, so no unverified interval is usable.
			w.logger.Warn("Could not persist merged-media chunk intervals",
				zap.String("session_id", p.SessionID),
				zap.Error(err))
		}
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

// mergeDownloadedChunks probes downloaded chunks, normalizes them into uniform
// H.264 / 30fps / 44.1kHz stereo AAC streams when codec, display dimensions,
// rotation, or audio parameters differ (e.g. mobile compression fallback from
// H.264 to raw HEVC), computes verified media intervals on the concat inputs,
// and writes the merged MP4 to mergedPath.
func mergeDownloadedChunks(ctx context.Context, log *zap.Logger, tmpDir string, objects, localChunkPaths []string, mergedPath string) ([]concatMediaInterval, error, error) {
	normalizedForConcat := false
	if len(localChunkPaths) > 0 {
		profiles, probeErr := probeAllChunkStreamProfiles(ctx, localChunkPaths)
		if probeErr != nil {
			log.Warn("Could not probe chunk stream profiles; falling back to single-pass concat",
				zap.Error(probeErr))
		} else {
			trimStarts := make([]float64, len(localChunkPaths))
			trimCount := 0
			for i, srcPath := range localChunkPaths {
				if trimSecs := probeLeadingBlackTrimSecs(ctx, srcPath); trimSecs > 0 {
					trimStarts[i] = trimSecs
					trimCount++
					log.Info("Detected leading black frame on chunk; scheduling trim before concat",
						zap.Int("chunk_index", i),
						zap.Float64("trim_start_secs", trimSecs))
				}
			}
			anyTrim := trimCount > 0

			needsNorm, targetW, targetH, includeAudio := chunksNeedNormalization(profiles)
			if !needsNorm && anyTrim && (!canTrimInPlaceForConcat(profiles) || shouldNormalizeForLeadingBlackTrim(len(localChunkPaths), trimCount)) {
				needsNorm = true
				targetW, targetH = normalizedTargetDimensions(profiles)
			}

			if needsNorm {
				log.Info("Normalizing chunks before concat",
					zap.Int("chunk_count", len(localChunkPaths)),
					zap.Int("target_width", targetW),
					zap.Int("target_height", targetH),
					zap.Bool("include_audio", includeAudio),
					zap.Bool("has_leading_black_trim", anyTrim))
				for i, srcPath := range localChunkPaths {
					normPath := filepath.Join(tmpDir, fmt.Sprintf("norm_chunk_%03d.mp4", i))
					if err := normalizeChunkForConcat(ctx, log, srcPath, normPath, profiles[i], targetW, targetH, includeAudio, trimStarts[i]); err != nil {
						return nil, nil, fmt.Errorf("normalize chunk %d: %w", i, err)
					}
					_ = os.Remove(srcPath)
					localChunkPaths[i] = normPath
				}
				normalizedForConcat = true
			} else if anyTrim {
				for i, srcPath := range localChunkPaths {
					if trimStarts[i] <= 0 {
						continue
					}
					trimPath := filepath.Join(tmpDir, fmt.Sprintf("trim_chunk_%03d.mp4", i))
					if err := trimChunkLeadingBlack(ctx, log, srcPath, trimPath, profiles[i], trimStarts[i]); err != nil {
						log.Warn("Leading black frame trim failed; keeping original chunk",
							zap.Int("chunk_index", i),
							zap.Error(err))
						continue
					}
					_ = os.Remove(srcPath)
					localChunkPaths[i] = trimPath
				}
			}
		}
	}

	mediaIntervals, intervalErr := probeConcatMediaIntervals(ctx, objects, localChunkPaths)

	concatListPath := filepath.Join(tmpDir, "concat_list.txt")
	defer os.Remove(concatListPath)
	concatEntries := make([]string, 0, len(localChunkPaths))
	for _, lp := range localChunkPaths {
		concatEntries = append(concatEntries, fmt.Sprintf("file '%s'", strings.ReplaceAll(lp, "'", "'\\''")))
	}
	if err := os.WriteFile(concatListPath, []byte(strings.Join(concatEntries, "\n")), 0o644); err != nil {
		return nil, intervalErr, fmt.Errorf("failed to write concat list: %w", err)
	}

	if normalizedForConcat {
		copyErr := runFFmpegStreamCopyConcat(ctx, log, concatListPath, mergedPath)
		if copyErr == nil && intervalErr == nil && len(mediaIntervals) > 0 {
			copyErr = verifyMergedDurationMatchesIntervals(ctx, mergedPath, mediaIntervals)
		}
		if copyErr != nil {
			log.Warn("Stream-copy concat of normalized chunks failed, retrying re-encode concat", zap.Error(copyErr))
			if fallbackErr := runFFmpegConcat(ctx, log, concatListPath, mergedPath); fallbackErr != nil {
				return nil, intervalErr, fallbackErr
			}
			if intervalErr == nil && len(mediaIntervals) > 0 {
				if verifyErr := verifyMergedDurationMatchesIntervals(ctx, mergedPath, mediaIntervals); verifyErr != nil {
					return nil, intervalErr, verifyErr
				}
			}
		}
		return mediaIntervals, intervalErr, nil
	}

	if err := runFFmpegConcat(ctx, log, concatListPath, mergedPath); err != nil {
		return nil, intervalErr, err
	}
	if intervalErr == nil && len(mediaIntervals) > 0 {
		if verifyErr := verifyMergedDurationMatchesIntervals(ctx, mergedPath, mediaIntervals); verifyErr != nil {
			return nil, intervalErr, verifyErr
		}
	}
	return mediaIntervals, intervalErr, nil
}

func shouldNormalizeForLeadingBlackTrim(totalChunks, trimCount int) bool {
	return totalChunks >= minChunksForBulkTrimNormalize && trimCount*2 >= totalChunks
}

func verifyMergedDurationMatchesIntervals(ctx context.Context, mergedPath string, intervals []concatMediaInterval) error {
	if len(intervals) == 0 {
		return nil
	}
	expected := intervals[len(intervals)-1].End
	if expected <= 0 {
		return nil
	}
	actual := probeVideoDuration(ctx, mergedPath)
	if actual <= 0 {
		return fmt.Errorf("merged video has non-positive probed duration %.3fs (expected %.3fs)", actual, expected)
	}
	tolerance := max(1.5, expected*0.05)
	if math.Abs(actual-expected) > tolerance {
		return fmt.Errorf("merged video duration %.3fs diverges from cumulative chunk duration %.3fs (tolerance %.3fs)", actual, expected, tolerance)
	}
	return nil
}

func probeAllChunkStreamProfiles(ctx context.Context, localPaths []string) ([]chunkStreamProfile, error) {
	profiles := make([]chunkStreamProfile, 0, len(localPaths))
	for i, lp := range localPaths {
		profile, err := probeChunkStreamProfile(ctx, lp)
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i, err)
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func probeChunkStreamProfile(ctx context.Context, filePath string) (chunkStreamProfile, error) {
	output, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_streams",
		"-of", "json",
		filePath,
	).Output()
	if err != nil {
		return chunkStreamProfile{}, fmt.Errorf("ffprobe streams: %w", err)
	}

	var doc struct {
		Streams []struct {
			CodecType  string            `json:"codec_type"`
			CodecName  string            `json:"codec_name"`
			PixFmt     string            `json:"pix_fmt"`
			Width      int               `json:"width"`
			Height     int               `json:"height"`
			TimeBase   string            `json:"time_base"`
			SampleRate string            `json:"sample_rate"`
			Channels   int               `json:"channels"`
			Tags       map[string]string `json:"tags"`
			SideData   []struct {
				Rotation float64 `json:"rotation"`
			} `json:"side_data_list"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &doc); err != nil {
		return chunkStreamProfile{}, fmt.Errorf("decode ffprobe streams: %w", err)
	}

	var profile chunkStreamProfile
	foundVideo := false
	for _, s := range doc.Streams {
		switch s.CodecType {
		case "video":
			if foundVideo {
				continue
			}
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
			if profile.HasAudio {
				continue
			}
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
	return profile, nil
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

// chunksNeedNormalization checks whether all chunks share identical video/audio
// stream parameters safe for FFmpeg's concat demuxer. When parameters differ, it
// computes an even target upright display resolution (capped at 1280px long edge)
// and whether the normalized output should carry a stereo 44.1kHz AAC audio track.
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

func probeLeadingBlackTrimSecs(ctx context.Context, inputPath string) float64 {
	if probeVideoDuration(ctx, inputPath) < minChunkDurationForTrimSecs {
		return 0
	}
	output, err := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin",
		"-v", "info",
		"-t", fmt.Sprintf("%.3f", leadingBlackProbeWindowSecs),
		"-i", inputPath,
		"-vf", "blackdetect=d=0.015:pix_th=0.05:pic_th=0.98",
		"-an",
		"-f", "null",
		"-",
	).CombinedOutput()
	if err != nil {
		return 0
	}
	return parseLeadingBlackTrimSecs(string(output))
}

func canTrimInPlaceForConcat(profiles []chunkStreamProfile) bool {
	if len(profiles) == 0 || profiles[0].VideoTimescale <= 0 {
		return false
	}
	baseTimescale := profiles[0].VideoTimescale
	for _, p := range profiles {
		if p.VideoCodec != "h264" || p.PixFmt != "yuv420p" || p.Rotation != 0 || p.Width%2 != 0 || p.Height%2 != 0 {
			return false
		}
		if p.VideoTimescale != baseTimescale {
			return false
		}
		if p.HasAudio && (p.AudioCodec != "aac" || p.SampleRate <= 0 || p.Channels <= 0) {
			return false
		}
	}
	return true
}

func trimChunkLeadingBlack(ctx context.Context, log *zap.Logger, inputPath, outputPath string, profile chunkStreamProfile, trimStartSecs float64) error {
	if profile.VideoTimescale <= 0 {
		return errors.New("cannot trim chunk in place without positive video timescale")
	}
	timescale := strconv.Itoa(profile.VideoTimescale)
	vf := fmt.Sprintf("trim=start=%.6f,setpts=PTS-STARTPTS,fps=30,format=yuv420p", trimStartSecs)
	args := []string{
		"-i", inputPath,
		"-map", "0:v:0",
	}
	if profile.HasAudio {
		args = append(args,
			"-map", "0:a:0",
			"-vf", vf,
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
			"-video_track_timescale", timescale,
			"-af", fmt.Sprintf("atrim=start=%.6f,asetpts=PTS-STARTPTS", trimStartSecs),
			"-c:a", "aac", "-ar", strconv.Itoa(profile.SampleRate), "-ac", strconv.Itoa(profile.Channels), "-b:a", "128k",
		)
	} else {
		args = append(args,
			"-vf", vf,
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
			"-video_track_timescale", timescale,
			"-an",
		)
	}
	args = append(args, "-movflags", "+faststart", "-y", outputPath)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("FFmpeg leading black trim failed",
			zap.String("input_path", inputPath),
			zap.Error(err),
			zap.String("output", string(output)))
		return fmt.Errorf("ffmpeg trim leading black: %s: %w", string(output), err)
	}
	return nil
}

func normalizeChunkForConcat(ctx context.Context, log *zap.Logger, inputPath, outputPath string, profile chunkStreamProfile, targetW, targetH int, includeAudio bool, trimStartSecs float64) error {
	timescale := strconv.Itoa(canonicalConcatVideoTimescale)
	vf := fmt.Sprintf(
		"fps=30,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,format=yuv420p",
		targetW, targetH, targetW, targetH,
	)
	af := "aresample=async=1:first_pts=0"
	if trimStartSecs > 0 {
		vf = fmt.Sprintf("trim=start=%.6f,setpts=PTS-STARTPTS,%s", trimStartSecs, vf)
		af = fmt.Sprintf("atrim=start=%.6f,asetpts=PTS-STARTPTS,%s", trimStartSecs, af)
	}

	var args []string
	switch {
	case !includeAudio:
		args = []string{
			"-i", inputPath,
			"-map", "0:v:0",
			"-vf", vf,
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
			"-video_track_timescale", timescale,
			"-an",
			"-movflags", "+faststart",
			"-y",
			outputPath,
		}
	case profile.HasAudio:
		args = []string{
			"-i", inputPath,
			"-map", "0:v:0",
			"-map", "0:a:0",
			"-vf", vf,
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
			"-video_track_timescale", timescale,
			"-af", af,
			"-c:a", "aac", "-ar", "44100", "-ac", "2", "-b:a", "128k",
			"-movflags", "+faststart",
			"-y",
			outputPath,
		}
	default:
		args = []string{
			"-f", "lavfi",
			"-i", "anullsrc=channel_layout=stereo:sample_rate=44100",
			"-i", inputPath,
			"-map", "1:v:0",
			"-map", "0:a:0",
			"-vf", vf,
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
			"-video_track_timescale", timescale,
			"-c:a", "aac", "-ar", "44100", "-ac", "2", "-b:a", "128k",
			"-shortest",
			"-movflags", "+faststart",
			"-y",
			outputPath,
		}
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("FFmpeg chunk normalization failed",
			zap.String("input_path", inputPath),
			zap.Error(err),
			zap.String("output", string(output)))
		return fmt.Errorf("ffmpeg normalize: %s: %w", string(output), err)
	}
	return nil
}

func runFFmpegStreamCopyConcat(ctx context.Context, log *zap.Logger, concatListPath, outputPath string) error {
	args := []string{
		"-f", "concat",
		"-safe", "0",
		"-i", concatListPath,
		"-c", "copy",
		"-movflags", "+faststart",
		"-y",
		outputPath,
	}

	log.Info("Running FFmpeg concat (stream copy on normalized chunks)", zap.Strings("args", args))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("FFmpeg stream-copy concat failed", zap.Error(err), zap.String("output", string(output)))
		return fmt.Errorf("ffmpeg copy concat: %s: %w", string(output), err)
	}

	log.Info("FFmpeg merge completed", zap.String("output_path", outputPath))
	return nil
}

func probeConcatMediaIntervals(ctx context.Context, objects, localPaths []string) ([]concatMediaInterval, error) {
	if len(objects) == 0 || len(objects) != len(localPaths) {
		return nil, errors.New("concat source list is incomplete")
	}

	intervals := make([]concatMediaInterval, 0, len(objects))
	cumulative := 0.0
	for i, localPath := range localPaths {
		duration := probeVideoDuration(ctx, localPath)
		if duration <= 0 {
			return nil, fmt.Errorf("could not probe duration for concat source %d", i)
		}
		intervals = append(intervals, concatMediaInterval{
			FilePath: objects[i],
			Start:    cumulative,
			End:      cumulative + duration,
		})
		cumulative += duration
	}
	return intervals, nil
}

func (w *Worker) persistConcatMediaIntervals(ctx context.Context, sessionID string, profileID uint, intervals []concatMediaInterval) error {
	if w.DB == nil {
		return errors.New("database is not configured")
	}
	if len(intervals) == 0 {
		return errors.New("no verified concat intervals")
	}

	return w.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		"-video_track_timescale", strconv.Itoa(canonicalConcatVideoTimescale),
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

	args := []string{
		"-i", inputPath,
		"-vf", fmt.Sprintf("subtitles=%s:force_style='%s'", srtPath, forceStyle),
		"-preset", "ultrafast",
		"-c:a", "copy",
		"-movflags", "+faststart",
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
