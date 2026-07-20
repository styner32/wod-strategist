package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	// maxAnalysisFileSizeBytes is the hard limit for video files sent to Gemini.
	// Files larger than this are rejected immediately with a FAILED analysis result.
	// The Gemini Files API struggles with very large uploads (timeouts, OOM).
	maxAnalysisFileSizeBytes = 1 << 30 // 1 GB

	PersonalProfilePrompt = `
## 개인 프로필
분석의 정확도를 높이기 위해 개인 정보를 참고해주세요.`

	KnownInjuriesPrompt = `
## 알려진 부상 사항
분석의 정확도를 높이기 위해 아래 부상 사항을 참고해주세요.`

	AnalysisPrompt = `
# 운동 영상 분석 요청

## 언어 규칙
- 모든 분석 결과는 **존댓말** (~요, ~세요, ~습니다)로 작성하세요. 반말(~해, ~다, ~함)을 절대 사용하지 마세요.

## 분석 요청 사항
당신은 전문 스포츠 생체역학 전문가이자 코치입니다. 위 컨텍스트와 첨부된 영상을 바탕으로 다음 항목을 분석해 주세요.

1. **동작 분석 및 체형 평가 (Movement & Posture Analysis)**:
   - 전반적인 자세의 정확도와 가동 범위를 평가해주세요.
   - 평소 체형의 불균형이나 의심되는 증상(예: 거북목, 라운드 숄더, 일자허리, 골반 비대칭 등)이 관찰된다면 함께 짚어주세요.
   - 영상에서 대상 인물에게 직접 확인되고 재검증된 종목의 표준 기술(Standard)과 비교해 주세요.

2. **강점 및 약점 (Strengths & Weaknesses)**:
   - 동작 수행 중 잘 유지되고 있는 부분(Core 안정성, 리듬 등)은 무엇인가요?
   - 자세가 무너지거나 힘의 누수가 발생하는 약점은 무엇인가요?

3. **피로도 및 페이스 분석 (Fatigue Analysis)**:
   - 반복 속도, 케이던스, 가동범위 또는 자세의 지속적인 저하가 직접 보이는 경우에만 **정확한 시점(분:초)**을 지목해주세요.
   - 허용되는 시각 근거가 없으면 피로 이벤트나 피로 시작 시점을 만들지 마세요.
   - 피로가 자세에 어떤 영향을 미쳤는지(예: 등이 굽음, 무릎이 모임) 영상 근거와 함께 설명해 주세요.

4. **개선 솔루션 (Actionable Feedback)**:
   - 다음에 이 운동을 할 때 즉시 적용할 수 있는 구체적인 팁을 3가지 제안해주세요.
   - 입력된 **운동 목표**가 있다면, 그 목표 달성을 위한 전략적 조언을 포함해 주세요.`

	HighlightSelectionPrompt = `

5. **하이라이트 시각 근거 (Highlight Evidence)**:
   - 이 응답이 분석하는 구간 안에서 대상 인물에게 직접 보이는 근거만 추출하세요.
   - 사용자 입력에만 있고 영상에서 보이지 않는 계획 종목은 포함하지 마세요. 힌트에 없는 실제 관찰 종목은 포함하세요.
   - 걷기, 휴식, 회복, 준비, 장비 세팅, Unknown은 하이라이트나 fatigue_point가 아닙니다.
   - 구간당 최대 3개만 출력하고, 근거가 없으면 빈 배열을 출력하세요. 카테고리별 개수 할당량은 없습니다.
   - type: positive_form(직접 보이는 좋은 기술), form_issue(직접 보이는 교정점), fatigue_onset(지속적인 속도·가동범위·자세 저하), technique_event(평가와 별개인 구체적인 기술·전환 장면)
	   - 동일한 연속 동작과 같은 type을 여러 조각으로 나누지 마세요. 각 start/end는 현상이 실제로 보이는 정확한 시각이어야 합니다.
	   - confidence는 해당 시각 근거가 영상에서 직접 확인된 확신도이며 0.0~1.0 숫자로 출력하세요.
   - 중요한 장면이면 tags에 key_moment를 추가하세요. positive_form/form_issue/fatigue_onset와 겹치는 key_moment를 별도 항목으로 중복 출력하지 마세요.
   - fatigue_onset는 심박수만으로 만들지 말고 반복 속도 저하, 케이던스 손실, 가동범위 감소 또는 자세 붕괴가 지속적으로 보여야 합니다.
   - movement 필드에는 실제로 관찰된 운동 종목명을 기입하세요.
   - 반드시 아래 형식의 **highlights** JSON 코드 블록으로 출력하세요 (json이 아닌 highlights 태그 사용):
` + "```highlights\n" + `[{"start":"0:15","end":"0:18.5","type":"positive_form","movement":"Snatch","reason":"수직에 가까운 풀 익스텐션","confidence":0.94,"tags":["key_moment"]},{"start":"0:22","end":"0:24","type":"form_issue","movement":"Snatch","reason":"캐치 순간 무릎 내전","confidence":0.86}]` + "\n```"

	InjuryTimestampPrompt = `

6. **부상 관련 타임스탬프 (Injury-Relevant Timestamps)**:
   - 아래 부상 부위가 활발히 사용되거나 위험에 노출되는 구간을 JSON 배열로 출력하세요.
   - 반드시 아래 형식의 **injury_timestamps** JSON 코드 블록으로 출력하세요 (json이 아닌 injury_timestamps 태그 사용):
` + "```injury_timestamps\n" + `[{"start": "0:32", "end": "0:45", "reason": "무릎 내전 관찰"}]` + "\n```"
)

// highlightBlockRegex matches fenced ```highlights ... ``` or <highlights> ... </highlights> blocks in Gemini output.
var highlightBlockRegex = regexp.MustCompile("(?is)(?:```highlights\\s*(\\[.*?\\])\\s*```|<highlights>\\s*(\\[.*?\\])\\s*</highlights>)")

// injuryTimestampBlockRegex matches fenced ```injury_timestamps ... ```, ```json ... ```, or <injury_timestamps> ... </injury_timestamps> blocks in Gemini output.
var injuryTimestampBlockRegex = regexp.MustCompile("(?is)(?:```(?:injury_timestamps|json)\\s*(\\[.*?\\])\\s*```|<injury_timestamps>\\s*(\\[.*?\\])\\s*</injury_timestamps>)")

func NewVideoAnalysisTask(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint, enableTTS bool, wodDescription string) (*asynq.Task, error) {
	payload := VideoAnalysisPayload{
		SessionID:      sessionID,
		FilePath:       filePath,
		WorkoutType:    NormalizeWorkoutType(workoutType),
		Movements:      movements,
		Injuries:       injuries,
		ProfileID:      profileID,
		EnableTTS:      enableTTS,
		WODDescription: wodDescription,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeVideoAnalysis, data), nil
}

func (w *Worker) HandleVideoAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	retryCount, ok := asynq.GetRetryCount(ctx)
	if !ok {
		retryCount = 0
	}

	w.logger.Info("Processing video analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.String("workout_type", NormalizeWorkoutType(p.WorkoutType)),
		zap.Strings("movements", p.Movements),
		zap.Strings("injuries", p.Injuries),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		w.logger.Error("Max retries reached. Skipping analysis.")
		return asynq.SkipRetry
	}

	if !strings.HasPrefix(p.FilePath, "gs://") {
		w.logger.Error("Invalid file path: must be a GCS URI", zap.String("file_path", p.FilePath))
		return fmt.Errorf("invalid file path: %w", asynq.SkipRetry)
	}

	if err := validateSessionID(p.SessionID); err != nil {
		w.logger.Error("Invalid session ID", zap.String("session_id", p.SessionID))
		return err
	}

	profileID, err := w.resolveVideoAnalysisProfile(ctx, p.SessionID, p.ProfileID)
	if err != nil {
		w.logger.Error("Invalid video analysis profile",
			zap.String("session_id", p.SessionID),
			zap.Uint("profile_id", p.ProfileID),
			zap.Error(err))
		return err
	}
	p.ProfileID = profileID

	pMode := p.PipelineMode
	if pMode == "" {
		pMode = string(w.PipelineMode)
	}
	if pMode == "" && w.UseCache {
		pMode = string(PipelineModeOptimized)
	}

	if pMode == string(PipelineModeOptimized) || pMode == string(PipelineModeCompare) {
		w.logger.Info("Using two-pass analysis for video (optimized)", zap.String("session_id", p.SessionID), zap.String("mode", pMode))
		return w.handleVideoAnalysisTwoPass(ctx, p)
	}

	w.logger.Info("Using legacy analysis for video", zap.String("session_id", p.SessionID), zap.String("mode", pMode))
	return w.handleVideoAnalysisLegacy(ctx, p)
}

var legacySessionProfilePattern = regexp.MustCompile(`^P([1-9][0-9]*)-WOD-\d{4}-\d{2}-\d{2}-\d{2}-\d{2}$`)

// resolveVideoAnalysisProfile prevents an analysis row from being written for
// a missing profile, while still recovering legacy queued tasks that carried
// profile_id=0. Current session IDs never encode a profile ID; only the old
// P{id}-... format is parsed as a final compatibility fallback.
func (w *Worker) resolveVideoAnalysisProfile(ctx context.Context, sessionID string, payloadProfileID uint) (uint, error) {
	if w.DB == nil {
		return 0, fmt.Errorf("database is not configured")
	}

	var session db.Session
	sessionErr := w.DB.WithContext(ctx).
		Select("session_id", "profile_id").
		Where("session_id = ?", sessionID).
		First(&session).Error
	if sessionErr != nil && !errors.Is(sessionErr, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf("failed to resolve session profile: %w", sessionErr)
	}

	profileID := payloadProfileID
	if sessionErr == nil {
		if profileID == 0 {
			profileID = session.ProfileID
		} else if session.ProfileID != profileID {
			return 0, fmt.Errorf("session profile does not match task profile: %w", asynq.SkipRetry)
		}
	}

	if profileID == 0 {
		match := legacySessionProfilePattern.FindStringSubmatch(sessionID)
		if len(match) == 2 {
			parsed, parseErr := strconv.ParseUint(match[1], 10, 32)
			if parseErr == nil {
				profileID = uint(parsed)
			}
		}
	}

	if profileID == 0 {
		return 0, fmt.Errorf("profile_id is required for video analysis: %w", asynq.SkipRetry)
	}

	var profile db.Profile
	if err := w.DB.WithContext(ctx).Select("id").First(&profile, profileID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("profile %d no longer exists: %w", profileID, asynq.SkipRetry)
		}
		return 0, fmt.Errorf("failed to validate analysis profile: %w", err)
	}

	return profileID, nil
}

// Segment represents an identified exercise set within a larger video.
type Segment struct {
	Start       string `json:"start"`       // Format: "MM:SS"
	End         string `json:"end"`         // Format: "MM:SS"
	Type        string `json:"type"`        // e.g., "Snatch", "Burpee"
	Description string `json:"description"` // Context from the model
}

// buildIndexPrompt creates the prompt for Pass 1 (Flash model indexing).
// Includes profile context so the model can identify the correct person
// in a multi-person gym environment, and video duration as a hard constraint.
func (w *Worker) buildIndexPrompt(p VideoAnalysisPayload, videoDuration time.Duration) string {
	personalProfile := w.lookupProfileString(p.ProfileID)

	durationStr := formatDuration(videoDuration)

	prompt := fmt.Sprintf(`## Task
Watch this workout video and identify all active exercise sets performed by the target person.

## CRITICAL: Video Duration
This video is exactly %s long. 
Do NOT generate any timestamps beyond %s.
Any timestamp exceeding this duration means you are hallucinating — discard it.

## Target Person
The person to track has the following profile:
- %s
Focus ONLY on this person's movements. Ignore other people in the background.
`, durationStr, durationStr, personalProfile)

	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf(`
## Non-exclusive Movement Hints
The user supplied these possible movements: %s
They are suggestions, not confirmation and not a closed list. Omit any hint not visibly performed by the target person, and preserve any different movement that is visibly supported.
`, strings.Join(p.Movements, ", "))
	}
	prompt += w.buildPersonalMovementHintsContext(p.ProfileID, p.SessionID)

	prompt += `
## Instructions
1. First, scrub through the entire video frame by frame and create a brief text timeline.
2. For EACH segment, require target-person evidence for apparatus contact (when applicable), body position, and the continuous motion pattern.
3. Nearby equipment is not evidence. A rope beside a pull-up bar does not make a target person's pull-up a rope climb.
4. Background athletes and their equipment are not target-person evidence.
5. If target exercise is visible but the exact movement is unclear, use type "Unknown"; do not force a hint. Omit walking, rest, recovery, setup, and no-exercise intervals.
6. Only report exercises you can visually confirm — do NOT guess or infer exercises from context.
7. Output a strictly formatted JSON array of the segments.
8. Use "MM:SS" format for timestamps.
9. Each segment should be at least 10 seconds long.

## Output JSON Schema
` + "```json\n[\n  {\n    \"start\": \"MM:SS\",\n    \"end\": \"MM:SS\",\n    \"type\": \"Exercise Name\",\n    \"description\": \"What you visually observe the target person doing — describe the equipment, stance, and movement.\"\n  }\n]\n```"

	return prompt
}

// formatDuration formats a duration as "MM:SS".
func formatDuration(d time.Duration) string {
	totalSecs := int(d.Seconds())
	mins := totalSecs / 60
	secs := totalSecs % 60
	return fmt.Sprintf("%d:%02d", mins, secs)
}

// handleVideoAnalysisTwoPass uses a two-pass approach:
//  1. Upload video to Gemini Files API
//  2. Index video with Flash model → find exercise segments
//  3. Analyze each segment with Pro model + VideoMetadata → deep analysis
func (w *Worker) handleVideoAnalysisTwoPass(ctx context.Context, p VideoAnalysisPayload) (retErr error) {
	started := time.Now()
	apiCalls := 0
	// Deferred error handler: write FAILED analysis result to DB on any error
	// so the user always sees something in history instead of silent nothingness.
	defer func() {
		if retErr != nil {
			w.logger.Error("Video analysis failed",
				zap.String("session_id", p.SessionID),
				zap.Error(retErr))

			userMsg := "동영상 분석 중 오류가 발생했습니다. 다시 시도해주세요."
			if strings.Contains(retErr.Error(), "file too large") {
				userMsg = "동영상 파일이 너무 큽니다 (1GB 초과). 더 짧은 영상으로 시도해주세요."
			}

			failedResult := &db.AnalysisResult{
				SessionID:    p.SessionID,
				Status:       "FAILED",
				Output:       userMsg,
				AnalysisType: db.AnalysisTypeWOD,
			}
			failedResult.ProfileID = p.ProfileID
			if dbErr := w.DB.Create(failedResult).Error; dbErr != nil {
				w.logger.Error("Failed to write FAILED analysis result",
					zap.String("session_id", p.SessionID),
					zap.Error(dbErr))
			}
		}
	}()

	localFilePath, err := createTempFile("analysis", ".mp4")
	if err != nil {
		return err
	}

	w.logger.Info("Downloading file from GCS for two-pass analysis",
		zap.String("uri", p.FilePath),
		zap.String("dest", localFilePath))
	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download file from GCS: %w", err)
	}
	defer func() {
		if err := os.Remove(localFilePath); err != nil {
			w.logger.Error("Failed to remove temp file", zap.Error(err))
		}
	}()

	// ── File size guard + conditional re-encode ──
	// If the uploaded video exceeds 500MB, create an analysis-grade re-encode
	// (CRF 28, 720p) before sending to Gemini. This mirrors the merge-chunks
	// path behavior. Only reject if the re-encoded file still exceeds 1GB.
	fi, err := os.Stat(localFilePath)
	if err != nil {
		return fmt.Errorf("failed to stat downloaded file: %w", err)
	}
	fileSizeBytes := fi.Size()
	w.logger.Info("Downloaded video file",
		zap.String("session_id", p.SessionID),
		zap.Int64("file_size_bytes", fileSizeBytes),
		zap.String("file_size_human", formatFileSizeWorker(fileSizeBytes)))

	// ── Ensure merged.mp4 exists for playback ──
	// When a video is uploaded directly (via upload-complete, not merge-chunks),
	// no merged.mp4 is created. The frontend requires merged.mp4 to show the
	// "Watch" button and enable video downloads. Upload the original file as
	// merged.mp4 if it doesn't already exist.
	if p.ProfileID > 0 {
		mergedObjectName := fmt.Sprintf("videos/%d/%s/merged.mp4", p.ProfileID, p.SessionID)
		existing, listErr := w.StorageClient.ListObjects(ctx, mergedObjectName)
		if listErr != nil {
			w.logger.Warn("Failed to check for existing merged.mp4, skipping copy",
				zap.String("session_id", p.SessionID),
				zap.Error(listErr))
		} else if len(existing) == 0 {
			w.logger.Info("No merged.mp4 found, uploading original as merged.mp4 for playback",
				zap.String("session_id", p.SessionID),
				zap.String("object_name", mergedObjectName))
			if mergedURI, uploadErr := w.StorageClient.UploadFromFile(ctx, localFilePath, mergedObjectName); uploadErr != nil {
				w.logger.Warn("Failed to upload merged.mp4 for playback, continuing with analysis",
					zap.String("session_id", p.SessionID),
					zap.Error(uploadErr))
			} else {
				w.logger.Info("Uploaded merged.mp4 for playback",
					zap.String("session_id", p.SessionID),
					zap.String("gcs_uri", mergedURI))
			}
		} else {
			w.logger.Info("merged.mp4 already exists, skipping upload",
				zap.String("session_id", p.SessionID))
		}
	}

	// Path used for Gemini upload — may be replaced by re-encoded copy below
	geminiInputPath := localFilePath

	if fileSizeBytes > maxAnalysisVideoSizeBytes {
		w.logger.Info("Video exceeds analysis threshold, creating analysis-grade re-encode",
			zap.String("session_id", p.SessionID),
			zap.Int64("file_size_bytes", fileSizeBytes),
			zap.String("file_size_human", formatFileSizeWorker(fileSizeBytes)),
			zap.Int64("threshold_bytes", maxAnalysisVideoSizeBytes))

		analysisPath, encErr := createTempFile("analysis-reencode", ".mp4")
		if encErr != nil {
			return fmt.Errorf("failed to create temp file for re-encode: %w", encErr)
		}
		defer os.Remove(analysisPath)

		if err := runFFmpegAnalysisEncode(ctx, w.logger, localFilePath, analysisPath); err != nil {
			w.logger.Warn("Analysis re-encode failed, checking if original is within hard limit",
				zap.String("session_id", p.SessionID),
				zap.Error(err))

			// Fall through — original file will be checked against the 1GB hard limit below
		} else {
			// Re-encode succeeded — check the new file size
			if reencFI, statErr := os.Stat(analysisPath); statErr == nil {
				analysisSizeBytes := reencFI.Size()
				w.logger.Info("Analysis re-encode completed",
					zap.String("session_id", p.SessionID),
					zap.Int64("original_size_bytes", fileSizeBytes),
					zap.String("original_size_human", formatFileSizeWorker(fileSizeBytes)),
					zap.Int64("analysis_size_bytes", analysisSizeBytes),
					zap.String("analysis_size_human", formatFileSizeWorker(analysisSizeBytes)),
					zap.Float64("compression_ratio", float64(fileSizeBytes)/float64(max(analysisSizeBytes, 1))))

				geminiInputPath = analysisPath
				fileSizeBytes = analysisSizeBytes
			}
		}
	}

	if fileSizeBytes > maxAnalysisFileSizeBytes {
		w.logger.Error("Video file too large for analysis (even after re-encode attempt)",
			zap.String("session_id", p.SessionID),
			zap.Int64("file_size_bytes", fileSizeBytes),
			zap.String("file_size_human", formatFileSizeWorker(fileSizeBytes)),
			zap.Int64("max_bytes", maxAnalysisFileSizeBytes))
		return fmt.Errorf("file too large for analysis (%s, max 1GB): %w",
			formatFileSizeWorker(fileSizeBytes), asynq.SkipRetry)
	}

	// Upload to Gemini Files API (uses re-encoded path if available)
	uploadTime := time.Now()
	upload, err := w.GeminiClient.UploadVideo(ctx, geminiInputPath)
	if err != nil {
		return fmt.Errorf("failed to upload video: %w", err)
	}

	success := false
	defer func() {
		if !success {
			w.logger.Info("Deleting Gemini file after two-pass analysis failure to prevent leak",
				zap.String("session_id", p.SessionID),
				zap.String("file_name", upload.FileName))
			if delErr := w.GeminiClient.DeleteFile(context.Background(), upload.FileName); delErr != nil {
				w.logger.Error("Failed to delete Gemini file after failure", zap.Error(delErr))
			}
		}
	}()
	hasInjuries := len(p.Injuries) > 0

	// ── Pass 1: Build segment index ──
	// Prefer chunk analysis data from DB (app-recorded, accurate timestamps).
	// For uploaded videos (no chunks), split into ~10s segments first to simulate
	// the real-time recording flow and provide accurate segment data.
	// Fall back to Flash model indexing only when splitting also fails.
	segments := w.buildSegmentsFromChunks(p.SessionID)

	// Determine if we need to (re-)split the video.
	// If existing chunks don't cover the full video duration (e.g. worker restarted
	// mid-split), we re-run the split so the remaining segments get analyzed.
	needsSplit := len(segments) == 0
	if len(segments) > 0 {
		videoDuration := probeVideoDuration(ctx, geminiInputPath)
		if videoDuration > 0 {
			maxEndSecs := w.queryMaxChunkEndSecs(p.SessionID)
			gap := videoDuration - maxEndSecs
			if gap > float64(splitChunkDurationSecs) {
				needsSplit = true
				w.logger.Warn("Pass 1: Existing chunks appear incomplete, will re-split",
					zap.String("session_id", p.SessionID),
					zap.Float64("max_chunk_end_secs", maxEndSecs),
					zap.Float64("video_duration_secs", videoDuration),
					zap.Float64("gap_secs", gap),
					zap.Int("existing_segments", len(segments)))
			}
		}
	}

	if needsSplit {
		// No chunks from recording or incomplete — try splitting the uploaded video into chunks
		w.logger.Info("Pass 1: Splitting uploaded video into 10s chunks",
			zap.String("session_id", p.SessionID),
			zap.Bool("had_partial_chunks", len(segments) > 0))

		if splitErr := w.splitAndAnalyzeChunks(ctx, geminiInputPath, p); splitErr != nil {
			w.logger.Warn("Chunk splitting failed, will fall back to model indexing",
				zap.String("session_id", p.SessionID),
				zap.Error(splitErr))
		} else {
			// Re-query chunks after splitting
			segments = w.buildSegmentsFromChunks(p.SessionID)
		}
	}

	if len(segments) > 0 {
		w.logger.Info("Pass 1: Segments built from chunk analysis data",
			zap.String("session_id", p.SessionID),
			zap.Int("count", len(segments)))
	} else {
		// Fallback: use Flash model to index the video
		w.logger.Info("Pass 1: No chunks found after split attempt, indexing video with model",
			zap.String("session_id", p.SessionID),
			zap.String("file_uri", upload.FileURI))

		apiCalls++
		indexOutput, indexUsage, indexErr := w.GeminiClient.IndexVideo(ctx, upload.FileURI, upload.MIMEType, w.buildIndexPrompt(p, upload.VideoDuration))
		if indexErr != nil {
			return fmt.Errorf("failed to index video: %w", indexErr)
		}

		w.saveTokenUsage(p.SessionID, p.ProfileID, "video:index", indexUsage)

		segments = parseSegments(indexOutput)

		// Post-filter: discard segments with timestamps beyond the actual video duration
		if upload.VideoDuration > 0 {
			segments = filterSegments(segments, upload.VideoDuration)
		}
	}

	if len(segments) == 0 {
		w.logger.Warn("No valid segments found, falling back to full video analysis",
			zap.String("session_id", p.SessionID))
		endStr := formatDuration(upload.VideoDuration)
		if upload.VideoDuration == 0 {
			endStr = "99:99"
		}
		segments = []Segment{{Start: "0:00", End: endStr, Type: "Full Video", Description: "Entire workout"}}
	}

	w.logger.Info("Segments identified",
		zap.String("session_id", p.SessionID),
		zap.Int("count", len(segments)),
		zap.Any("segments", segments))

	// ── Pass 1.5: Triage segments ──
	// If there are more segments than the budget allows, use Flash model to
	// rank them by coaching value and pick the top N.
	maxSegs := maxSegmentsForDuration(upload.VideoDuration)
	if len(segments) > maxSegs {
		w.logger.Info("Pass 1.5: Triaging segments",
			zap.Int("total_segments", len(segments)),
			zap.Int("max_segments", maxSegs))

		apiCalls++
		triagedSegments, triageUsage, triageErr := w.triageSegments(ctx, upload, segments, maxSegs)
		if triageErr != nil {
			w.logger.Warn("Segment triage failed, using first N segments",
				zap.Error(triageErr),
				zap.Int("fallback_count", maxSegs))
			segments = segments[:maxSegs]
		} else {
			w.saveTokenUsage(p.SessionID, p.ProfileID, "video:triage", triageUsage)
			segments = triagedSegments
		}

		w.logger.Info("Segments after triage",
			zap.Int("count", len(segments)),
			zap.Any("segments", segments))
	}

	// ── Pass 2: Analyze each segment with Pro ──
	// Build shared context once. The final segment receives history plus an
	// aggregate derived from every completed structured chunk.
	wodContext := buildWODContext(p.WODDescription)
	historyContext := w.buildHistoryContext(p.ProfileID, 5)
	fatigueContext := w.buildSessionFatigueEvidenceContext(p.SessionID)
	finalContext := historyContext + fatigueContext

	w.logger.Info("Pass 2 context prepared",
		zap.String("session_id", p.SessionID),
		zap.Bool("has_wod_context", wodContext != ""),
		zap.Bool("has_history", historyContext != ""),
		zap.Bool("has_fatigue_context", fatigueContext != ""))

	var allAnalysis strings.Builder
	var highlightCandidates []highlightCandidate
	for i, seg := range segments {
		start := convertToSeconds(seg.Start)
		end := convertToSeconds(seg.End)

		// Inject score prompt and history only into the last segment to avoid
		// asking every segment for a session-wide score.
		isLast := i == len(segments)-1
		segPrompt := w.buildSegmentAnalysisPrompt(p, seg, wodContext, finalContext, isLast)

		w.logger.Info("Pass 2: Analyzing segment",
			zap.Int("segment", i+1),
			zap.Int("total", len(segments)),
			zap.String("type", seg.Type),
			zap.Duration("start", start),
			zap.Duration("end", end),
			zap.Bool("score_prompt", isLast))

		apiCalls++
		segAnalysis, segUsage, err := w.GeminiClient.AnalyzeSegment(
			ctx, upload.FileURI, upload.MIMEType, start, end, segPrompt,
		)
		if err != nil {
			w.logger.Error("Segment analysis failed, skipping",
				zap.Int("segment", i+1),
				zap.String("type", seg.Type),
				zap.Error(err))
			continue
		}

		w.saveTokenUsage(p.SessionID, p.ProfileID, "video:segment", segUsage)
		highlightCandidates = append(highlightCandidates, parseHighlightCandidates(segAnalysis, highlightSource{
			Index:           i,
			Start:           start.Seconds(),
			End:             end.Seconds(),
			Movement:        seg.Type,
			HasBounds:       true,
			HardGapBoundary: true,
		})...)

		allAnalysis.WriteString(fmt.Sprintf("\n\n---\n## 세그먼트 %d: %s (%s ~ %s)\n\n", i+1, seg.Type, seg.Start, seg.End))
		allAnalysis.WriteString(segAnalysis)
	}

	analysis := allAnalysis.String()
	if analysis == "" {
		return fmt.Errorf("all segment analyses failed")
	}

	normalizedHighlights := consolidateHighlightCandidates(highlightCandidates, HighlightNormalizeOptions{
		VideoEndSeconds: upload.VideoDuration.Seconds(),
	})
	highlightSegments := ""
	if len(normalizedHighlights) > 0 {
		highlightSegments = MarshalHighlightSegments(normalizedHighlights)
	}
	w.logger.Info("Highlight evidence consolidated",
		zap.String("session_id", p.SessionID),
		zap.Int("candidate_count", len(highlightCandidates)),
		zap.Int("event_count", len(normalizedHighlights)))
	sessionScore := parseSessionScore(analysis)

	w.logger.Info("Session score parsed",
		zap.String("session_id", p.SessionID),
		zap.String("session_score", sessionScore))

	expiresAt := uploadTime.Add(47 * time.Hour)
	result := &db.AnalysisResult{
		SessionID:           p.SessionID,
		Status:              "COMPLETED",
		Output:              analysis,
		AnalysisType:        db.AnalysisTypeWOD,
		HighlightSegments:   highlightSegments,
		WODDescription:      p.WODDescription,
		SessionScore:        sessionScore,
		AvailableVideos:     db.CommaStringArray{"merged"},
		GeminiFileURI:       upload.FileURI,
		GeminiFileName:      upload.FileName,
		GeminiMIMEType:      upload.MIMEType,
		GeminiFileExpiresAt: &expiresAt,
	}
	result.ProfileID = p.ProfileID
	if err := w.DB.WithContext(ctx).Create(result).Error; err != nil {
		return fmt.Errorf("failed to persist completed video analysis: %w", err)
	}
	success = true

	w.logger.Info("Two-pass analysis completed",
		zap.String("session_id", p.SessionID),
		zap.Int("segments_analyzed", len(segments)))

	// Auto-enqueue hardsub generation now that the final analysis is available.
	// The hardsub worker will fetch this analysis and burn it as subtitles.
	w.enqueueHardSub(p.SessionID, p.ProfileID, p.EnableTTS)

	// Agentic follow-up: pass fileURI/fileName so injury analysis can reuse the uploaded video
	if hasInjuries {
		focusTimestamps := parseInjuryTimestamps(analysis)
		injuryTask, taskErr := NewInjuryAnalysisTask(p.SessionID, p.FilePath, p.Injuries, p.ProfileID, focusTimestamps)
		if taskErr != nil {
			w.logger.Error("Failed to create injury analysis task", zap.Error(taskErr))
		} else {
			// Attach file info to the injury task payload
			var injPayload InjuryAnalysisPayload
			_ = json.Unmarshal(injuryTask.Payload(), &injPayload)
			injPayload.GeminiFileURI = upload.FileURI
			injPayload.GeminiFileName = upload.FileName
			injPayload.GeminiMIMEType = upload.MIMEType

			pMode := p.PipelineMode
			if pMode == "" {
				pMode = string(w.PipelineMode)
			}
			injPayload.PipelineMode = pMode

			data, _ := json.Marshal(injPayload)
			injuryTask = asynq.NewTask(TypeInjuryAnalysis, data)

			if _, enqErr := w.QueueClient.Enqueue(injuryTask); enqErr != nil {
				w.logger.Error("Failed to enqueue injury analysis task", zap.Error(enqErr))
			} else {
				w.logger.Info("Injury analysis enqueued with file URI",
					zap.String("session_id", p.SessionID),
					zap.String("file_uri", upload.FileURI),
					zap.Strings("injuries", p.Injuries))
			}
		}
	}

	pMode := p.PipelineMode
	if pMode == "" {
		pMode = string(w.PipelineMode)
	}
	w.recordStageMetrics(p.SessionID, p.ProfileID, "video_analysis", pMode, apiCalls, 0, fileSizeBytes, time.Since(started))
	return nil
}

// buildSegmentAnalysisPrompt builds the analysis prompt for a specific segment.
// wodContext and historyContext are pre-built shared strings (may be empty).
// If isLastSegment is true, the score output prompt and history context are appended
// so Gemini produces a session-wide score at the end of the final segment.
func (w *Worker) buildSegmentAnalysisPrompt(p VideoAnalysisPayload, seg Segment, wodContext, historyContext string, isLastSegment bool) string {
	personalProfile := w.lookupProfileString(p.ProfileID)

	prompt := fmt.Sprintf(`# 운동 영상 분석 요청
전문 스포츠 생체역학 전문가로서 이 운동 구간(%s ~ %s)을 분석해 주세요.

## 중간 종목 라벨 (확정 아님)
이 구간의 이전 청크 라벨은 '%s'입니다. 현재 구간의 대상 인물 영상 근거로 다시 식별하고, 근거가 모순되면 라벨을 교정하거나 Unknown으로 판단하세요.

## 개인 프로필
%s
`, seg.Start, seg.End, seg.Type, personalProfile)

	// WOD context (type-specific analysis guidance)
	if wodContext != "" {
		prompt += wodContext
	}

	prompt += AnalysisPrompt
	prompt += MovementEvidenceRulesPrompt
	prompt += FatigueEvidenceRulesPrompt

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n   - 부상 부위: %s", InjuryTimestampPrompt, strings.Join(p.Injuries, ", "))
	}

	prompt += HighlightSelectionPrompt

	prompt += buildMovementHintsContext(p.Movements)
	prompt += w.buildPersonalMovementHintsContext(p.ProfileID, p.SessionID)

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n## 알려진 부상 사항: %s", KnownInjuriesPrompt, strings.Join(p.Injuries, ", "))
	}

	prompt += fmt.Sprintf(`

## 중요: 타임스탬프 규칙
- 이 구간은 %s ~ %s 입니다.
- 모든 타임스탬프는 이 범위 안에서만 지정하세요.
- 클립 내의 초 단위로 부상 위험이 있는 정확한 시점을 설명하세요.`, seg.Start, seg.End)

	// On the last segment: inject history table and request a session-wide score.
	if isLastSegment {
		if historyContext != "" {
			prompt += historyContext
		}
		prompt += ScoreOutputPrompt
	}

	return prompt
}

// parseSegments extracts the JSON array of Segment from the model's indexing output.
func parseSegments(text string) []Segment {
	re := regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")
	match := re.FindStringSubmatch(text)
	jsonStr := text
	if len(match) > 1 {
		jsonStr = match[1]
	}

	var segments []Segment
	_ = json.Unmarshal([]byte(jsonStr), &segments)
	return segments
}

// queryMaxChunkEndSecs returns the maximum end_secs value from chunk analysis
// records for the given session. Returns 0 if no chunks exist or on error.
// Used to detect incomplete splits (partial chunk coverage vs full video duration).
func (w *Worker) queryMaxChunkEndSecs(sessionID string) float64 {
	if w.DB == nil {
		return 0
	}
	var maxEnd *float64
	err := w.DB.Model(&db.ChunkAnalysisResult{}).
		Where("session_id = ?", sessionID).
		Select("MAX(end_secs)").
		Row().Scan(&maxEnd)
	if err != nil || maxEnd == nil {
		return 0
	}
	return *maxEnd
}

// buildSegmentsFromChunks queries completed chunk analysis records from the DB
// and converts them to Segment structs. Only verified offsets in the merged media
// timeline are safe here; capture-clock timestamps must never be applied to the
// session video. Chunks with no detected exercise are filtered out, while Unknown
// is retained so the deeper analyzer can revalidate it from the video.
func (w *Worker) buildSegmentsFromChunks(sessionID string) []Segment {
	if w.DB == nil {
		return nil
	}

	var chunks []db.ChunkAnalysisResult
	err := w.DB.Where("session_id = ? AND status = ?", sessionID, "COMPLETED").
		Order("media_start_secs ASC NULLS LAST, id ASC").
		Find(&chunks).Error
	if err != nil || len(chunks) == 0 {
		return nil
	}

	var segments []Segment
	for _, chunk := range chunks {
		if !debugMediaIntervalValid(chunk.MediaStartSecs, chunk.MediaEndSecs) {
			continue
		}

		if !includeChunkInDeepAnalysis(chunk) {
			continue
		}

		// Use the chunk output (1-2 sentence coaching feedback) as the description.
		desc := chunk.Output
		if len(desc) > 200 {
			desc = desc[:200] + "..."
		}

		segments = append(segments, Segment{
			Start:       formatSegmentTimestamp(*chunk.MediaStartSecs),
			End:         formatSegmentTimestamp(*chunk.MediaEndSecs),
			Type:        chunk.ExerciseType,
			Description: desc,
		})
	}

	// Merge consecutive chunks with the same exercise type into larger segments
	// to avoid excessive Pro model calls (e.g. 30 Snatch chunks → 1 Snatch segment)
	return mergeSegmentsByMovement(segments)
}

func includeChunkInDeepAnalysis(chunk db.ChunkAnalysisResult) bool {
	movement := strings.TrimSpace(chunk.ExerciseType)
	if movement == "" {
		return false
	}

	var signals struct {
		ActivityState string `json:"activity_state"`
	}
	if json.Unmarshal([]byte(chunk.ObservedSignals), &signals) == nil &&
		isExplicitNonExerciseActivity(signals.ActivityState) {
		return false
	}

	if strings.EqualFold(movement, "unknown") {
		return true
	}
	return !isNonExerciseMovement(movement)
}

func formatSegmentTimestamp(seconds float64) string {
	duration := secondsDuration(seconds)
	minutes := duration / time.Minute
	remainder := duration % time.Minute
	wholeSeconds := remainder / time.Second
	nanoseconds := remainder % time.Second
	if nanoseconds == 0 {
		return fmt.Sprintf("%d:%02d", minutes, wholeSeconds)
	}
	fraction := strings.TrimRight(fmt.Sprintf("%09d", nanoseconds), "0")
	return fmt.Sprintf("%d:%02d.%s", minutes, wholeSeconds, fraction)
}

// mergeSegmentsByMovement combines adjacent segments that have the same exercise
// type (case-insensitive). A timeline gap is kept so an omitted rest/setup interval
// cannot be pulled into a movement segment.
func mergeSegmentsByMovement(segments []Segment) []Segment {
	if len(segments) <= 1 {
		return segments
	}

	var merged []Segment
	current := segments[0]

	for i := 1; i < len(segments); i++ {
		// Preserve filtered rest/setup gaps even when the movement before and after
		// the gap has the same label.
		if strings.EqualFold(current.Type, segments[i].Type) &&
			segmentBoundariesTouch(current.End, segments[i].Start) {
			current.End = segments[i].End
			if segments[i].Description != "" {
				current.Description = current.Description + " | " + segments[i].Description
			}
		} else {
			merged = append(merged, current)
			current = segments[i]
		}
	}
	merged = append(merged, current)
	return merged
}

func segmentBoundariesTouch(end, start string) bool {
	endDuration, endOK := parseSegmentTimestamp(end)
	startDuration, startOK := parseSegmentTimestamp(start)
	if !endOK || !startOK {
		return false
	}
	difference := endDuration - startDuration
	return difference >= -time.Millisecond && difference <= time.Millisecond
}

// maxSegmentsForDuration calculates the maximum number of segments to analyze
// based on video duration: 1 segment per 2 minutes, with min 3 and max 20.
func maxSegmentsForDuration(videoDuration time.Duration) int {
	minutes := int(videoDuration.Minutes())
	max := minutes / 2
	if max < 3 {
		max = 3
	}
	if max > 20 {
		max = 20
	}
	return max
}

// TriagedSegment represents a segment scored by the triage model.
type TriagedSegment struct {
	Index  int    `json:"index"`  // 0-based index into the original segment list
	Score  int    `json:"score"`  // 1-10 coaching value score
	Reason string `json:"reason"` // Brief reason for the score
}

// triageSegments uses the Flash model to rank segments by coaching value.
// It sends the segment list + video to the model and returns the top N segments
// ordered by their original timeline position.
//
// Future optimization: use segment descriptions instead of video for cost saving,
// once chunk analysis descriptions are rich enough.
func (w *Worker) triageSegments(ctx context.Context, upload *gemini.UploadResult, segments []Segment, maxSegs int) ([]Segment, *gemini.TokenUsage, error) {
	triagePrompt := buildTriagePrompt(segments, maxSegs, upload.VideoDuration)

	triageOutput, triageUsage, err := w.GeminiClient.IndexVideo(ctx, upload.FileURI, upload.MIMEType, triagePrompt)
	if err != nil {
		return nil, nil, fmt.Errorf("triage model call failed: %w", err)
	}

	selected := parseTriagedSegments(triageOutput, segments, maxSegs)
	if len(selected) == 0 {
		return nil, triageUsage, fmt.Errorf("triage returned no valid segments")
	}

	return selected, triageUsage, nil
}

// buildTriagePrompt creates the Flash model prompt for segment prioritization.
func buildTriagePrompt(segments []Segment, maxSegs int, videoDuration time.Duration) string {
	var segList strings.Builder
	for i, seg := range segments {
		segList.WriteString(fmt.Sprintf("%d. [%s ~ %s] %s: %s\n", i, seg.Start, seg.End, seg.Type, seg.Description))
	}

	prompt := fmt.Sprintf(`## Task: Segment Triage for Coaching Analysis

You are a sports coaching expert. Below is a list of exercise segments identified in a %s workout video.
Your job is to select the **top %d segments** that would benefit most from detailed biomechanical analysis.

## Segments
%s
## Selection Criteria (in priority order)
1. **Form issues visible** - segments where posture problems, compensations, or technique errors are noted
2. **Visually established fatigue indicators** - sustained rep slowdown, cadence loss, range loss, or form breakdown during exercise; heart rate, walking, rest, recovery, and setup are not fatigue evidence
3. **High-technique movements** - complex movements (Olympic lifts, gymnastics) over simple ones (running, jumping jacks)
4. **Diversity** - try to cover different movement types rather than redundantly analyzing the same exercise
5. **Skip low-value** - rest periods, setup, transitions, or very short clips with no meaningful movement

## Output Format
Return a JSON array of the top %d segment indices (0-based), sorted by coaching value (highest first).
Include a brief reason for each selection.
`,
		formatDuration(videoDuration), maxSegs, segList.String(), maxSegs)

	prompt += "\n" + "```" + "json\n" + `[{"index": 0, "score": 9, "reason": "Snatch pull shows early arm bend"}, {"index": 3, "score": 7, "reason": "Squat depth decreasing (fatigue)"}]` + "\n" + "```"

	return prompt
}

// parseTriagedSegments extracts the triaged segment indices from the model output
// and returns the corresponding segments in chronological order.
func parseTriagedSegments(output string, allSegments []Segment, maxSegs int) []Segment {
	// Extract JSON from code fence
	re := regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")
	match := re.FindStringSubmatch(output)
	jsonStr := output
	if len(match) > 1 {
		jsonStr = match[1]
	}

	var triaged []TriagedSegment
	if err := json.Unmarshal([]byte(jsonStr), &triaged); err != nil {
		return nil
	}

	// Collect valid indices, cap at maxSegs
	seen := make(map[int]bool)
	var indices []int
	for _, t := range triaged {
		if t.Index >= 0 && t.Index < len(allSegments) && !seen[t.Index] {
			seen[t.Index] = true
			indices = append(indices, t.Index)
			if len(indices) >= maxSegs {
				break
			}
		}
	}

	// Sort indices to maintain chronological order
	sort.Ints(indices)

	var selected []Segment
	for _, idx := range indices {
		selected = append(selected, allSegments[idx])
	}
	return selected
}

// convertToSeconds parses fractional or whole "MM:SS", "H:MM:SS", and plain
// seconds formats to time.Duration.
func convertToSeconds(input string) time.Duration {
	duration, ok := parseSegmentTimestamp(input)
	if !ok {
		return 0
	}
	return duration
}

func parseSegmentTimestamp(input string) (time.Duration, bool) {
	input = strings.TrimSpace(input)
	if strings.Contains(input, ":") {
		parts := strings.Split(input, ":")
		if len(parts) == 3 {
			hours, hoursErr := strconv.Atoi(parts[0])
			mins, minsErr := strconv.Atoi(parts[1])
			secs, secsErr := strconv.ParseFloat(parts[2], 64)
			if hoursErr != nil || minsErr != nil || secsErr != nil || hours < 0 || mins < 0 || secs < 0 {
				return 0, false
			}
			return secondsDuration(float64(hours*3600+mins*60) + secs), true
		}
		if len(parts) == 2 {
			mins, minsErr := strconv.Atoi(parts[0])
			secs, secsErr := strconv.ParseFloat(parts[1], 64)
			if minsErr != nil || secsErr != nil || mins < 0 || secs < 0 {
				return 0, false
			}
			return secondsDuration(float64(mins*60) + secs), true
		}
		return 0, false
	}
	clean := strings.TrimSuffix(input, "s")
	if seconds, err := strconv.ParseFloat(clean, 64); err == nil && seconds >= 0 {
		return secondsDuration(seconds), true
	}
	return 0, false
}

// filterSegments removes segments whose start or end timestamps exceed the
// actual video duration. This catches hallucinated timestamps that the model
// generates beyond the video's real length.
func filterSegments(segments []Segment, videoDuration time.Duration) []Segment {
	var valid []Segment
	for _, seg := range segments {
		startDur := convertToSeconds(seg.Start)
		endDur := convertToSeconds(seg.End)
		if startDur <= videoDuration && endDur <= videoDuration+5*time.Second {
			valid = append(valid, seg)
		}
	}
	return valid
}

// handleVideoAnalysisLegacy is the original file-upload based path.
func (w *Worker) handleVideoAnalysisLegacy(ctx context.Context, p VideoAnalysisPayload) error {
	started := time.Now()
	localFilePath, err := createTempFile("legacy-analysis", ".mp4")
	if err != nil {
		return err
	}

	w.logger.Info("Downloading file from GCS", zap.String("uri", p.FilePath), zap.String("dest", localFilePath))
	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download file from GCS: %w", err)
	}

	fi, _ := os.Stat(localFilePath)
	var fileSizeBytes int64
	if fi != nil {
		fileSizeBytes = fi.Size()
	}

	videoDuration := probeVideoDuration(ctx, localFilePath)
	w.logger.Info("Probed video duration", zap.Float64("duration_secs", videoDuration))

	prompt := w.buildAnalysisPrompt(p, videoDuration)

	analysis, geminiFile, usage, err := w.GeminiClient.AnalyzeVideo(ctx, localFilePath, prompt)

	// Clean up local file
	defer func() {
		if err := os.Remove(localFilePath); err != nil {
			w.logger.Error("Failed to remove temp file", zap.Error(err))
		}
	}()

	// Clean up Gemini file if it was uploaded
	if geminiFile != "" {
		defer func() {
			if err := w.GeminiClient.DeleteFile(ctx, geminiFile); err != nil {
				w.logger.Error("Failed to delete file from Gemini", zap.Error(err))
			}
		}()
	}

	// Save token usage regardless of analysis outcome
	w.saveTokenUsage(p.SessionID, p.ProfileID, "video:legacy", usage)

	// retry if analysis is empty
	if analysis == "" {
		w.logger.Warn("Analysis failed. Retrying...", zap.Error(err))
		return fmt.Errorf("analysis is empty")
	}

	if err != nil {
		if os.IsNotExist(err) {
			w.logger.Error("File not found. Skipping analysis.", zap.Error(err))
			return asynq.SkipRetry
		}

		w.logger.Error("Analysis failed", zap.Error(err))

		failedResult := &db.AnalysisResult{
			SessionID: p.SessionID,
			Status:    "FAILED",
			Output:    "An internal error occurred during analysis.",
		}
		failedResult.ProfileID = p.ProfileID
		if dbErr := w.DB.WithContext(ctx).Create(failedResult).Error; dbErr != nil {
			w.logger.Error("Failed to persist legacy FAILED analysis result",
				zap.String("session_id", p.SessionID),
				zap.Error(dbErr))
		}

		return err
	}

	// Parse and consolidate full-video highlight evidence using authoritative
	// media bounds. Exact observation intervals remain inside padded events.
	legacyCandidates := parseHighlightCandidates(analysis, highlightSource{
		Index: -1, Start: 0, End: videoDuration, HasBounds: videoDuration > 0,
	})
	normalizedHighlights := consolidateHighlightCandidates(legacyCandidates, HighlightNormalizeOptions{
		VideoEndSeconds: videoDuration,
	})
	highlightSegments := ""
	if len(normalizedHighlights) > 0 {
		highlightSegments = MarshalHighlightSegments(normalizedHighlights)
	}

	result := &db.AnalysisResult{
		SessionID:         p.SessionID,
		Status:            "COMPLETED",
		Output:            analysis,
		AnalysisType:      db.AnalysisTypeWOD,
		HighlightSegments: highlightSegments,
		AvailableVideos:   db.CommaStringArray{"merged"},
	}
	result.ProfileID = p.ProfileID
	if err := w.DB.WithContext(ctx).Create(result).Error; err != nil {
		return fmt.Errorf("failed to persist completed legacy video analysis: %w", err)
	}

	w.logger.Info("Analysis completed", zap.String("session_id", p.SessionID), zap.String("analysis", analysis))

	// Auto-enqueue hardsub generation now that the final analysis is available.
	w.enqueueHardSub(p.SessionID, p.ProfileID, p.EnableTTS)

	// Agentic follow-up: if injuries are present, enqueue injury-focused re-analysis
	if len(p.Injuries) > 0 {
		focusTimestamps := parseInjuryTimestamps(analysis)
		injuryTask, taskErr := NewInjuryAnalysisTask(p.SessionID, p.FilePath, p.Injuries, p.ProfileID, focusTimestamps)
		if taskErr != nil {
			w.logger.Error("Failed to create injury analysis task", zap.Error(taskErr))
		} else {
			pMode := p.PipelineMode
			if pMode == "" {
				pMode = string(w.PipelineMode)
			}
			var injPayload InjuryAnalysisPayload
			if err := json.Unmarshal(injuryTask.Payload(), &injPayload); err == nil {
				injPayload.PipelineMode = pMode
				if data, err := json.Marshal(injPayload); err == nil {
					injuryTask = asynq.NewTask(injuryTask.Type(), data)
				}
			}

			if _, enqErr := w.QueueClient.Enqueue(injuryTask); enqErr != nil {
				w.logger.Error("Failed to enqueue injury analysis task", zap.Error(enqErr))
			} else {
				w.logger.Info("Injury analysis enqueued",
					zap.String("session_id", p.SessionID),
					zap.Strings("injuries", p.Injuries),
					zap.String("focus_timestamps", focusTimestamps))
			}
		}
	}

	pMode := p.PipelineMode
	if pMode == "" {
		pMode = string(w.PipelineMode)
	}
	w.recordStageMetrics(p.SessionID, p.ProfileID, "video_analysis", pMode, 1, 0, fileSizeBytes, time.Since(started))
	return nil
}

func (w *Worker) buildAnalysisPrompt(p VideoAnalysisPayload, videoDurationSecs float64) string {
	prompt := AnalysisPrompt + MovementEvidenceRulesPrompt + FatigueEvidenceRulesPrompt

	if wod := buildWODContext(p.WODDescription); wod != "" {
		prompt += wod
	}

	// When injuries are present, add a section requesting structured injury timestamps
	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n   - 부상 부위: %s", InjuryTimestampPrompt, strings.Join(p.Injuries, ", "))
	}

	// Always request highlight segments for short-form video generation
	prompt += HighlightSelectionPrompt

	prompt += buildMovementHintsContext(p.Movements)
	prompt += w.buildPersonalMovementHintsContext(p.ProfileID, p.SessionID)

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n## 알려진 부상 사항: %s", KnownInjuriesPrompt, strings.Join(p.Injuries, ", "))
	}

	personalProfile := w.lookupProfileString(p.ProfileID)

	w.logger.Info("Personal Profile", zap.Uint("profile_id", p.ProfileID), zap.String("personal_profile", personalProfile))

	prompt += fmt.Sprintf("%s\n## 개인 프로필: %s", PersonalProfilePrompt, personalProfile)
	prompt += w.buildSessionFatigueEvidenceContext(p.SessionID)

	// Ground timestamps with actual video duration to prevent hallucinated timestamps.
	if videoDurationSecs > 0 {
		minutes := int(videoDurationSecs) / 60
		seconds := int(videoDurationSecs) % 60
		prompt += fmt.Sprintf(`

## 중요: 타임스탬프 정확도 규칙
- 이 영상의 총 재생 시간은 %d분 %d초입니다.
- 모든 타임스탬프(핵심 구간, 하이라이트, 부상 관련)는 반드시 영상의 실제 장면과 정확히 일치해야 합니다.
- 영상에 존재하지 않는 장면의 타임스탬프를 절대 생성하지 마세요.
- 모든 타임스탬프는 0:00 ~ %d:%02d 범위 안에 있어야 합니다.
- 타임스탬프를 추측하지 말고, 영상을 직접 확인하여 정확한 시점을 기록하세요.`, minutes, seconds, minutes, seconds)
	}

	return prompt
}

// ParseHighlightSegments is the compatibility entrypoint used by admin reparse
// and legacy/debug paths that do not have per-segment provenance. New two-pass
// analysis parses each response immediately and supplies authoritative bounds.
func ParseHighlightSegments(analysisOutput string) string {
	candidates := parseHighlightCandidates(analysisOutput, highlightSource{Index: -1})
	if len(candidates) == 0 {
		return ""
	}
	return MarshalHighlightSegments(consolidateHighlightCandidates(candidates, HighlightNormalizeOptions{}))
}

func isHeartRateOnlyFatigueHighlight(highlight HighlightSegment) bool {
	typeName := strings.ToLower(strings.TrimSpace(highlight.Type))
	if typeName != "fatigue_point" && typeName != HighlightObservationFatigueOnset {
		return false
	}
	reason := strings.ToLower(highlight.Reason)
	hasHeartRate := strings.Contains(reason, "bpm") ||
		strings.Contains(reason, "heart rate") ||
		strings.Contains(reason, "heartrate") ||
		strings.Contains(reason, "심박")
	if !hasHeartRate {
		return false
	}
	for _, visualCue := range []string{
		"slow", "cadence", "tempo", "range of motion", "form", "posture", "rep", "breakdown",
		"느려", "속도", "케이던스", "템포", "가동범위", "자세", "반복", "무너", "락아웃", "감소", "손실",
	} {
		if strings.Contains(reason, visualCue) {
			return false
		}
	}
	return true
}

// parseInjuryTimestamps extracts all JSON arrays from ```injury_timestamps``` code blocks
// or <injury_timestamps> XML tags in the WOD analysis output. When multiple blocks exist
// (one per segment), all timestamps are merged into a single JSON array.
// Returns the raw JSON string, or empty on failure.
func parseInjuryTimestamps(analysisOutput string) string {
	matches := injuryTimestampBlockRegex.FindAllStringSubmatch(analysisOutput, -1)
	if len(matches) == 0 {
		return ""
	}

	var allTimestamps []json.RawMessage
	for _, match := range matches {
		// The regex has two capture groups (backtick vs XML); pick the non-empty one.
		jsonStr := firstNonEmpty(match[1:])
		if jsonStr == "" {
			continue
		}
		var parsed []json.RawMessage
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil || len(parsed) == 0 {
			continue
		}
		allTimestamps = append(allTimestamps, parsed...)
	}

	if len(allTimestamps) == 0 {
		return ""
	}

	result, err := json.Marshal(allTimestamps)
	if err != nil {
		return ""
	}
	return string(result)
}

// firstNonEmpty returns the first non-empty string from the given slice.
func firstNonEmpty(ss []string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
