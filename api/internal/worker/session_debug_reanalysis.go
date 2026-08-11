package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/storage"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	TypeSessionDebugReanalysis = "session:debug-reanalysis"

	sessionDebugPromptVersion = "session-debug-reanalysis-v1"
	sessionDebugSchemaVersion = "session-reanalysis-candidate-v1"
	sessionDebugMaxRetries    = 2
)

type SessionDebugReanalysisPayload struct {
	RunID uint `json:"run_id"`
}

type sessionDebugContext struct {
	WODDescription string
	WorkoutType    string
	MovementHints  db.JSONDocument
	Injuries       []string
	Segments       []Segment
	Corrections    []sessionDebugCorrection
}

type sessionDebugCorrection struct {
	FeedbackID     uint     `json:"feedback_id"`
	Revision       int      `json:"revision"`
	ChunkID        uint     `json:"chunk_id"`
	Category       string   `json:"category"`
	MovementName   string   `json:"movement_name,omitempty"`
	ActivityState  string   `json:"activity_state,omitempty"`
	FatigueState   string   `json:"fatigue_state,omitempty"`
	MediaStartSecs *float64 `json:"media_start_secs,omitempty"`
	MediaEndSecs   *float64 `json:"media_end_secs,omitempty"`
}

func NewSessionDebugReanalysisTask(runID uint) (*asynq.Task, error) {
	if runID == 0 {
		return nil, errors.New("run_id is required")
	}
	payload, err := json.Marshal(SessionDebugReanalysisPayload{RunID: runID})
	if err != nil {
		return nil, fmt.Errorf("marshal session debug re-analysis payload: %w", err)
	}
	return asynq.NewTask(TypeSessionDebugReanalysis, payload), nil
}

// HandleSessionDebugReanalysisTask creates a whole-workout debug candidate.
// It never writes analysis_results or enqueues highlights, hardsubs, injury,
// subtitle, or TTS work.
func (w *Worker) HandleSessionDebugReanalysisTask(ctx context.Context, task *asynq.Task) error {
	started := time.Now()
	var payload SessionDebugReanalysisPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil || payload.RunID == 0 {
		return fmt.Errorf("invalid session debug re-analysis payload: %w", asynq.SkipRetry)
	}
	if w.DB == nil {
		return fmt.Errorf("session debug re-analysis database is not configured: %w", asynq.SkipRetry)
	}

	retryCount, _ := asynq.GetRetryCount(ctx)
	var run db.SessionReanalysisRun
	if err := w.DB.WithContext(ctx).First(&run, payload.RunID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("session debug re-analysis run not found: %w", asynq.SkipRetry)
		}
		return fmt.Errorf("load session debug re-analysis run: %w", err)
	}
	if isSessionReanalysisTerminal(run.Status) {
		return nil
	}
	if w.StorageClient == nil || w.GeminiClient == nil {
		_ = w.completeSessionDebugRun(ctx, run.ID, db.SessionReanalysisStatusFailed, started, map[string]any{
			"safe_error": "The session re-analysis worker is not configured.",
		})
		return fmt.Errorf("session debug dependencies are not configured: %w", asynq.SkipRetry)
	}

	now := time.Now().UTC()
	claim := w.DB.WithContext(ctx).Model(&db.SessionReanalysisRun{}).
		Where("id = ? AND status = ?", run.ID, db.SessionReanalysisStatusQueued).
		Updates(map[string]any{"status": db.SessionReanalysisStatusRunning, "started_at": now, "safe_error": ""})
	if claim.Error != nil {
		return fmt.Errorf("claim session debug re-analysis: %w", claim.Error)
	}
	if claim.RowsAffected == 0 {
		return nil
	}

	if err := validateSessionDebugSource(run.SourceGCSURI, w.BucketName); err != nil {
		return w.finishSessionDebugUnavailable(ctx, run.ID, db.SessionReanalysisStatusVideoUnavailable, started,
			"The session video is unavailable.")
	}
	target, err := w.loadSessionDebugContext(ctx, run.SessionID, run.ProfileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return w.finishSessionDebugUnavailable(ctx, run.ID, db.SessionReanalysisStatusContextUnavailable, started,
				"The workout context is unavailable.")
		}
		return w.failSessionDebugRun(ctx, run.ID, retryCount, started, "The workout context could not be loaded.", err)
	}

	file, uploadBytes, err := w.prepareSessionDebugGeminiFile(ctx, &run)
	if err != nil {
		return w.failSessionDebugRun(ctx, run.ID, retryCount, started, "The session video could not be prepared.", err)
	}
	p := VideoAnalysisPayload{
		SessionID: run.SessionID, ProfileID: run.ProfileID, WorkoutType: target.WorkoutType,
		Movements: movementHintsFromDocument(target.MovementHints), Injuries: target.Injuries,
		WODDescription: target.WODDescription,
	}
	promptCorrections := mappedSessionDebugCorrections(target.Corrections)
	correctionContext := buildSessionDebugCorrectionContext(promptCorrections)
	segments := target.Segments
	var aggregate gemini.TokenUsage
	var promptRecord strings.Builder
	apiCalls := 0
	model := ""
	if len(segments) == 0 {
		indexPrompt := w.buildIndexPrompt(p, file.Duration) + correctionContext
		promptRecord.WriteString(indexPrompt)
		indexOutput, usage, indexErr := w.GeminiClient.IndexVideo(ctx, file.URI, chunkDebugMIMEType(file.MIMEType), indexPrompt)
		apiCalls++
		if indexErr == nil {
			w.saveTokenUsage(run.SessionID, run.ProfileID, "session:reanalysis", usage)
			addSessionDebugUsage(&aggregate, usage)
			segments = parseSegments(indexOutput)
			if file.Duration > 0 {
				segments = filterSessionDebugSegments(segments, file.Duration)
			}
		}
		if len(segments) == 0 && file.Duration > 0 {
			segments = []Segment{{Start: "0:00", End: formatDuration(file.Duration), Type: "Full Video", Description: "Entire workout"}}
		}
		if len(segments) == 0 {
			return w.finishSessionDebugUnavailable(ctx, run.ID, db.SessionReanalysisStatusContextUnavailable, started,
				"No exact workout segments are available.")
		}
	}
	if file.Duration > 0 {
		segments = filterSessionDebugSegments(segments, file.Duration)
		if len(segments) == 0 {
			return w.finishSessionDebugUnavailable(ctx, run.ID, db.SessionReanalysisStatusContextUnavailable, started,
				"No exact workout segments are available within the session video.")
		}
	}
	maxSegments := maxSegmentsForDuration(file.Duration)
	if len(segments) > maxSegments {
		triagePrompt := buildTriagePrompt(segments, maxSegments, file.Duration, target.WorkoutType)
		promptRecord.WriteString(triagePrompt)
		triaged, usage, triageErr := w.triageSegments(ctx, &gemini.UploadResult{
			FileName: file.Name, FileURI: file.URI, MIMEType: chunkDebugMIMEType(file.MIMEType), VideoDuration: file.Duration,
		}, segments, maxSegments, target.WorkoutType)
		apiCalls++
		w.saveTokenUsage(run.SessionID, run.ProfileID, "session:reanalysis", usage)
		addSessionDebugUsage(&aggregate, usage)
		if triageErr == nil && len(triaged) > 0 {
			segments = triaged
		} else {
			segments = segments[:maxSegments]
		}
	}

	contextSnapshot, _ := json.Marshal(map[string]any{
		"session_id": run.SessionID, "profile_id": run.ProfileID,
		"profile_context": w.lookupProfileString(run.ProfileID),
		"appearance":      w.buildTargetPersonContext(run.ProfileID, run.SessionID),
		"wod_description": target.WODDescription, "workout_type": target.WorkoutType,
		"movement_hints": movementHintsFromDocument(target.MovementHints),
		"injuries":       target.Injuries, "confirmed_corrections": target.Corrections,
		"prompt_corrections":        promptCorrections,
		"unmapped_correction_count": len(target.Corrections) - len(promptCorrections),
		"segments":                  segments, "source_gcs_uri": run.SourceGCSURI,
		"prompt_version": sessionDebugPromptVersion, "schema_version": sessionDebugSchemaVersion,
	})
	if err := w.DB.WithContext(ctx).Model(&db.SessionReanalysisRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"source_context_snapshot": string(contextSnapshot), "prompt_version": sessionDebugPromptVersion,
		"schema_version": sessionDebugSchemaVersion, "workout_type": target.WorkoutType,
	}).Error; err != nil {
		return w.failSessionDebugRun(ctx, run.ID, retryCount, started, "The re-analysis state could not be saved.", err)
	}

	wodContext := buildWODContext(target.WODDescription)
	finalContext := w.buildHistoryContext(run.ProfileID, 5) + w.buildSessionDebugFatigueContext(ctx, run.SessionID, run.ProfileID, target.Corrections)
	var output strings.Builder
	var highlightCandidates []highlightCandidate
	for i, segment := range segments {
		start, end := convertToSeconds(segment.Start), convertToSeconds(segment.End)
		if end <= start {
			continue
		}
		prompt := w.buildSegmentAnalysisPrompt(p, segment, wodContext, finalContext, i == len(segments)-1)
		prompt += correctionContext
		promptRecord.WriteString(prompt)
		selectedModel := resolveReanalysisModel(run.Model)
		analysis, usage, callErr := w.GeminiClient.AnalyzeSegmentWithModel(ctx, file.URI, chunkDebugMIMEType(file.MIMEType), start, end, prompt, selectedModel)
		apiCalls++
		if callErr != nil || strings.TrimSpace(analysis) == "" {
			if callErr == nil {
				callErr = errors.New("empty session debug segment result")
			}
			w.logger.Warn("session debug segment failed", zap.Uint("run_id", run.ID), zap.Int("segment", i+1), zap.Error(callErr))
			return w.failSessionDebugRun(ctx, run.ID, retryCount, started,
				"The analyzer could not complete every workout segment.", callErr)
		}
		w.saveTokenUsage(run.SessionID, run.ProfileID, "session:reanalysis", usage)
		addSessionDebugUsage(&aggregate, usage)
		if usage != nil && usage.Model != "" {
			model = usage.Model
		}
		highlightCandidates = append(highlightCandidates, parseHighlightCandidates(analysis, highlightSource{
			Index: i, Start: start.Seconds(), End: end.Seconds(), Movement: segment.Type, HasBounds: true, HardGapBoundary: true,
		})...)
		output.WriteString(fmt.Sprintf("\n\n---\n## 세그먼트 %d: %s (%s ~ %s)\n\n%s", i+1, segment.Type, segment.Start, segment.End, analysis))
	}
	if strings.TrimSpace(output.String()) == "" {
		return w.failSessionDebugRun(ctx, run.ID, retryCount, started, "The analyzer could not produce a candidate.", errors.New("all session debug segments failed"))
	}
	if model == "" {
		if provider, ok := w.GeminiClient.(interface{ Model() string }); ok {
			model = provider.Model()
		}
	}
	analysis := output.String()
	normalizedHighlights := consolidateHighlightCandidates(highlightCandidates, HighlightNormalizeOptions{
		VideoEndSeconds: file.Duration.Seconds(),
	})
	highlightSegments := ""
	if len(normalizedHighlights) > 0 {
		highlightSegments = MarshalHighlightSegments(normalizedHighlights)
	}
	hash := sha256.Sum256([]byte(promptRecord.String()))
	w.recordStageMetrics(run.SessionID, run.ProfileID, "session_reanalysis", "current", apiCalls, 0, uploadBytes, time.Since(started))
	return w.completeSessionDebugRun(ctx, run.ID, db.SessionReanalysisStatusCompleted, started, map[string]any{
		"output": analysis, "highlight_segments": highlightSegments,
		"session_score": parseSessionScore(analysis), "workout_type": target.WorkoutType,
		"model": model, "prompt_hash": hex.EncodeToString(hash[:]),
		"prompt_tokens": aggregate.PromptTokens, "candidate_tokens": aggregate.CandidateTokens,
		"total_tokens": aggregate.TotalTokens, "safe_error": "",
	})
}

func buildSessionDebugCorrectionContext(corrections []sessionDebugCorrection) string {
	if len(corrections) == 0 {
		return ""
	}
	raw, err := json.Marshal(corrections)
	if err != nil {
		return ""
	}
	return fmt.Sprintf(`

## 사용자가 저장한 청크 교정 (구조화된 참고 라벨, 확정된 영상 근거 아님)
%s
- 이 목록에는 저장된 활성 교정만 포함되며 자유 텍스트 메모와 미확정 재분석 후보는 포함되지 않습니다.
- 교정은 구간을 찾는 비배타적 참고 라벨입니다. 현재 영상에서 대상 인물의 기구 접촉, 몸 위치와 동작 패턴을 다시 확인하세요.
- 영상 근거가 교정과 모순되면 영상 근거를 우선하고 Unknown 또는 실제 관찰 종목으로 판단하세요.
- fatigue_state가 fatigued여도 시각적 피로 근거가 먼저 확인되지 않으면 피로 이벤트나 시점을 만들지 마세요.`, string(raw))
}

func mappedSessionDebugCorrections(corrections []sessionDebugCorrection) []sessionDebugCorrection {
	mapped := make([]sessionDebugCorrection, 0, len(corrections))
	for _, correction := range corrections {
		if !debugMediaIntervalValid(correction.MediaStartSecs, correction.MediaEndSecs) {
			continue
		}
		mapped = append(mapped, correction)
	}
	return mapped
}

func (w *Worker) loadSessionDebugContext(ctx context.Context, sessionID string, profileID uint) (*sessionDebugContext, error) {
	var profile db.Profile
	if err := w.DB.WithContext(ctx).Where("id = ?", profileID).First(&profile).Error; err != nil {
		return nil, err
	}
	target := &sessionDebugContext{WorkoutType: WorkoutTypeWOD, MovementHints: db.JSONDocument(`[]`)}
	var session db.Session
	if err := w.DB.WithContext(ctx).Where("session_id = ? AND profile_id = ?", sessionID, profileID).First(&session).Error; err == nil {
		target.WODDescription = session.WODDescription
		target.WorkoutType = NormalizeWorkoutType(session.WorkoutType)
		target.MovementHints = session.MovementHints
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	} else {
		var analysis db.AnalysisResult
		if analysisErr := w.DB.WithContext(ctx).Where("session_id = ? AND profile_id = ?", sessionID, profileID).
			Order("created_at DESC, id DESC").First(&analysis).Error; analysisErr == nil {
			target.WODDescription = analysis.WODDescription
		} else if !errors.Is(analysisErr, gorm.ErrRecordNotFound) {
			return nil, analysisErr
		}
	}
	if profile.Injuries != nil {
		_ = json.Unmarshal([]byte(*profile.Injuries), &target.Injuries)
	}
	corrections, err := w.loadActiveSessionDebugCorrections(ctx, sessionID, profileID)
	if err != nil {
		return nil, err
	}
	target.Corrections = corrections
	target.Segments, err = w.buildSessionDebugSegments(ctx, sessionID, profileID, corrections, target.WorkoutType)
	if err != nil {
		return nil, err
	}
	return target, nil
}

func (w *Worker) loadActiveSessionDebugCorrections(ctx context.Context, sessionID string, profileID uint) ([]sessionDebugCorrection, error) {
	var history []db.AnalysisFeedback
	if err := w.DB.WithContext(ctx).Where("session_id = ? AND profile_id = ? AND target_type = ? AND chunk_analysis_result_id IS NOT NULL",
		sessionID, profileID, db.FeedbackTargetChunk).Order("created_at DESC, id DESC").Find(&history).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	result := make([]sessionDebugCorrection, 0)
	for _, item := range history {
		if _, ok := seen[item.FeedbackKey]; ok {
			continue
		}
		seen[item.FeedbackKey] = struct{}{}
		if item.Retracted || item.ChunkAnalysisResultID == nil {
			continue
		}
		var values struct {
			MovementName  string `json:"movement_name"`
			ActivityState string `json:"activity_state"`
			FatigueState  string `json:"fatigue_state"`
		}
		if json.Unmarshal(item.Correction, &values) != nil {
			continue
		}
		result = append(result, sessionDebugCorrection{
			FeedbackID: item.ID, Revision: item.Revision, ChunkID: *item.ChunkAnalysisResultID,
			Category: item.Category, MovementName: values.MovementName,
			ActivityState: values.ActivityState, FatigueState: values.FatigueState,
		})
	}
	return result, nil
}

func (w *Worker) buildSessionDebugSegments(ctx context.Context, sessionID string, profileID uint, corrections []sessionDebugCorrection, workoutType string) ([]Segment, error) {
	var chunks []db.ChunkAnalysisResult
	if err := w.DB.WithContext(ctx).Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Order("media_start_secs ASC NULLS LAST, id ASC").Find(&chunks).Error; err != nil {
		return nil, err
	}
	byChunk := make(map[uint][]int)
	for index := range corrections {
		byChunk[corrections[index].ChunkID] = append(byChunk[corrections[index].ChunkID], index)
	}
	segments := make([]Segment, 0)
	for _, chunk := range chunks {
		if !debugMediaIntervalValid(chunk.MediaStartSecs, chunk.MediaEndSecs) {
			continue
		}
		movement := strings.TrimSpace(chunk.ExerciseType)
		include := strings.EqualFold(chunk.Status, "COMPLETED") && includeChunkInDeepAnalysis(chunk, workoutType)
		for _, correctionIndex := range byChunk[chunk.ID] {
			corrections[correctionIndex].MediaStartSecs = cloneSessionDebugFloat(chunk.MediaStartSecs)
			corrections[correctionIndex].MediaEndSecs = cloneSessionDebugFloat(chunk.MediaEndSecs)
			correction := corrections[correctionIndex]
			if correction.MovementName != "" {
				movement = strings.TrimSpace(correction.MovementName)
				include = movement != "" && (!isNonExerciseMovement(movement) || strings.EqualFold(movement, "Unknown"))
			}
			switch correction.ActivityState {
			case "walking", "rest_setup", "not_exercise":
				include = false
			case "unknown":
				movement, include = "Unknown", true
			case "exercise":
				include = true
				if movement == "" {
					movement = "Unknown"
				}
			}
		}
		if !include || movement == "" || (isNonExerciseMovement(movement) && !strings.EqualFold(movement, "Unknown")) {
			continue
		}
		description := chunk.Output
		if len(byChunk[chunk.ID]) > 0 {
			description = "Saved user correction (non-authoritative label); revalidate from visible evidence."
		}
		segments = append(segments, Segment{Start: formatSegmentTimestamp(*chunk.MediaStartSecs), End: formatSegmentTimestamp(*chunk.MediaEndSecs), Type: movement, Description: description})
	}
	return mergeSegmentsByMovement(segments), nil
}

// buildSessionDebugFatigueContext applies only active structured corrections
// before aggregating the original visual evidence. A saved "fatigued" value is
// never allowed to create evidence that the production chunk did not contain.
func (w *Worker) buildSessionDebugFatigueContext(ctx context.Context, sessionID string, profileID uint, corrections []sessionDebugCorrection) string {
	var chunks []db.ChunkAnalysisResult
	if err := w.DB.WithContext(ctx).Where("session_id = ? AND profile_id = ? AND status = ?", sessionID, profileID, "COMPLETED").
		Order("media_start_secs ASC NULLS LAST, id ASC").Find(&chunks).Error; err != nil {
		return ""
	}
	byChunk := make(map[uint][]sessionDebugCorrection)
	for _, correction := range corrections {
		byChunk[correction.ChunkID] = append(byChunk[correction.ChunkID], correction)
	}
	for index := range chunks {
		var signals map[string]any
		if json.Unmarshal([]byte(sanitizeObservedSignals(chunks[index].ObservedSignals)), &signals) != nil {
			signals = map[string]any{}
		}
		for _, correction := range byChunk[chunks[index].ID] {
			if correction.MovementName != "" {
				chunks[index].ExerciseType = correction.MovementName
				signals["movement"] = correction.MovementName
				if isNonExerciseMovement(correction.MovementName) {
					signals["fatigue_visually_established"] = false
				}
			}
			switch correction.ActivityState {
			case "walking", "rest_setup", "not_exercise":
				signals["activity_state"] = correction.ActivityState
				signals["fatigue_visually_established"] = false
			case "unknown":
				signals["activity_state"] = "unknown"
				signals["fatigue_visually_established"] = false
			case "exercise":
				signals["activity_state"] = "exercise"
			}
			switch correction.FatigueState {
			case "not_fatigued", "walking_rest", "unknown":
				signals["fatigue_visually_established"] = false
			}
		}
		if raw, err := json.Marshal(signals); err == nil {
			chunks[index].ObservedSignals = string(raw)
		}
	}
	return buildFatigueEvidenceContext(chunks)
}

func (w *Worker) prepareSessionDebugGeminiFile(ctx context.Context, run *db.SessionReanalysisRun) (reusableGeminiFile, int64, error) {
	if cached := w.findReusableSessionDebugGeminiFile(ctx, run); cached.URI != "" {
		duration, err := w.probeSessionDebugSourceDuration(ctx, run.SourceGCSURI)
		if err == nil {
			cached.Duration = duration
			return cached, 0, nil
		}
		w.logger.Warn("could not probe cached session debug source; uploading a fresh Files object", zap.Uint("run_id", run.ID), zap.Error(err))
	}
	localPath, err := createTempFile("session-debug-reanalysis", ".mp4")
	if err != nil {
		return reusableGeminiFile{}, 0, err
	}
	defer os.Remove(localPath)
	if err := w.StorageClient.DownloadFile(ctx, run.SourceGCSURI, localPath); err != nil {
		return reusableGeminiFile{}, 0, fmt.Errorf("download session debug source: %w", err)
	}
	var uploadBytes int64
	if info, err := os.Stat(localPath); err == nil {
		uploadBytes = info.Size()
	}
	upload, err := w.GeminiClient.UploadVideo(ctx, localPath)
	if err != nil {
		if upload != nil && upload.FileName != "" {
			if deleteErr := w.GeminiClient.DeleteFile(context.Background(), upload.FileName); deleteErr != nil {
				w.logger.Warn("failed to clean up incomplete session debug upload", zap.Error(deleteErr))
			}
		}
		return reusableGeminiFile{}, uploadBytes, fmt.Errorf("upload session debug source: %w", err)
	}
	if upload.VideoDuration <= 0 {
		if seconds := probeVideoDuration(ctx, localPath); seconds > 0 {
			upload.VideoDuration = secondsDuration(seconds)
		}
	}
	expires := time.Now().UTC().Add(geminiFileCacheTTL)
	_ = w.DB.WithContext(ctx).Model(&db.SessionReanalysisRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"gemini_file_uri": upload.FileURI, "gemini_file_name": upload.FileName,
		"gemini_mime_type": upload.MIMEType, "gemini_file_expires_at": expires,
	}).Error
	return reusableGeminiFile{URI: upload.FileURI, Name: upload.FileName, MIMEType: upload.MIMEType, Expires: &expires, Duration: upload.VideoDuration}, uploadBytes, nil
}

func (w *Worker) findReusableSessionDebugGeminiFile(ctx context.Context, run *db.SessionReanalysisRun) reusableGeminiFile {
	cutoff := time.Now().UTC().Add(time.Minute)
	if run.GeminiFileURI != "" && run.GeminiFileName != "" && run.GeminiFileExpiresAt != nil && run.GeminiFileExpiresAt.After(cutoff) && w.geminiFileStillExists(ctx, run.GeminiFileName) {
		return reusableGeminiFile{URI: run.GeminiFileURI, Name: run.GeminiFileName, MIMEType: run.GeminiMIMEType, Expires: run.GeminiFileExpiresAt}
	}
	var previous db.SessionReanalysisRun
	if err := w.DB.WithContext(ctx).Where("profile_id = ? AND session_id = ? AND source_gcs_uri = ? AND id <> ? AND gemini_file_uri <> '' AND gemini_file_name <> '' AND gemini_file_expires_at > ?",
		run.ProfileID, run.SessionID, run.SourceGCSURI, run.ID, cutoff).Order("created_at DESC").First(&previous).Error; err == nil && w.geminiFileStillExists(ctx, previous.GeminiFileName) {
		return reusableGeminiFile{URI: previous.GeminiFileURI, Name: previous.GeminiFileName, MIMEType: previous.GeminiMIMEType, Expires: previous.GeminiFileExpiresAt}
	}
	var chunk db.ChunkReanalysisRun
	if err := w.DB.WithContext(ctx).Where("profile_id = ? AND session_id = ? AND source_gcs_uri = ? AND gemini_file_uri <> '' AND gemini_file_name <> '' AND gemini_file_expires_at > ?",
		run.ProfileID, run.SessionID, run.SourceGCSURI, cutoff).Order("created_at DESC").First(&chunk).Error; err == nil && w.geminiFileStillExists(ctx, chunk.GeminiFileName) {
		return reusableGeminiFile{URI: chunk.GeminiFileURI, Name: chunk.GeminiFileName, MIMEType: chunk.GeminiMIMEType, Expires: chunk.GeminiFileExpiresAt}
	}
	return reusableGeminiFile{}
}

func (w *Worker) probeSessionDebugSourceDuration(ctx context.Context, sourceURI string) (time.Duration, error) {
	localPath, err := createTempFile("session-debug-duration", ".mp4")
	if err != nil {
		return 0, err
	}
	defer os.Remove(localPath)
	if err := w.StorageClient.DownloadFile(ctx, sourceURI, localPath); err != nil {
		return 0, fmt.Errorf("download session debug source for duration: %w", err)
	}
	seconds := probeVideoDuration(ctx, localPath)
	if seconds <= 0 {
		return 0, errors.New("session debug source duration is unavailable")
	}
	return secondsDuration(seconds), nil
}

func validateSessionDebugSource(uri, bucketName string) error {
	bucket, object, err := storage.ParseGCSURI(uri)
	if err != nil || object == "" || (bucketName != "" && bucket != bucketName) {
		return errors.New("invalid session debug source")
	}
	return nil
}

func cloneSessionDebugFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func filterSessionDebugSegments(segments []Segment, duration time.Duration) []Segment {
	filtered := make([]Segment, 0, len(segments))
	for _, segment := range segments {
		start, startOK := parseSegmentTimestamp(segment.Start)
		end, endOK := parseSegmentTimestamp(segment.End)
		if !startOK || !endOK || start < 0 || end <= start || end > duration+250*time.Millisecond {
			continue
		}
		filtered = append(filtered, segment)
	}
	return filtered
}

func addSessionDebugUsage(total *gemini.TokenUsage, usage *gemini.TokenUsage) {
	if usage == nil {
		return
	}
	total.PromptTokens += usage.PromptTokens
	total.CandidateTokens += usage.CandidateTokens
	total.TotalTokens += usage.TotalTokens
	if usage.Model != "" {
		total.Model = usage.Model
	}
}

func (w *Worker) failSessionDebugRun(ctx context.Context, runID uint, retryCount int, started time.Time, safeError string, cause error) error {
	if retryCount < sessionDebugMaxRetries {
		_ = w.DB.WithContext(ctx).Model(&db.SessionReanalysisRun{}).Where("id = ?", runID).Updates(map[string]any{
			"status": db.SessionReanalysisStatusQueued, "safe_error": "", "duration_ms": time.Since(started).Milliseconds(),
		}).Error
		return cause
	}
	if err := w.completeSessionDebugRun(ctx, runID, db.SessionReanalysisStatusFailed, started, map[string]any{"safe_error": safeError}); err != nil {
		return err
	}
	return fmt.Errorf("session debug re-analysis failed: %v: %w", cause, asynq.SkipRetry)
}

func (w *Worker) finishSessionDebugUnavailable(ctx context.Context, runID uint, status string, started time.Time, safeError string) error {
	return w.completeSessionDebugRun(ctx, runID, status, started, map[string]any{"safe_error": safeError})
}

func (w *Worker) completeSessionDebugRun(ctx context.Context, runID uint, status string, started time.Time, values map[string]any) error {
	now := time.Now().UTC()
	values["status"] = status
	values["duration_ms"] = time.Since(started).Milliseconds()
	values["completed_at"] = now
	if err := w.DB.WithContext(ctx).Model(&db.SessionReanalysisRun{}).Where("id = ?", runID).Updates(values).Error; err != nil {
		return fmt.Errorf("persist terminal session re-analysis state: %w", err)
	}
	return nil
}

func isSessionReanalysisTerminal(status string) bool {
	switch status {
	case db.SessionReanalysisStatusCompleted, db.SessionReanalysisStatusFailed,
		db.SessionReanalysisStatusVideoUnavailable, db.SessionReanalysisStatusContextUnavailable:
		return true
	default:
		return false
	}
}
