package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"go.uber.org/zap"
)

const (
	PersonalProfilePrompt = `
## 개인 프로필
분석의 정확도를 높이기 위해 개인 정보를 참고해주세요.`

	MovementPrompt = `
## 운동 컨텍스트
분석의 정확도를 높이기 위해 아래 운동 정보를 참고해주세요.`

	KnownInjuriesPrompt = `
## 알려진 부상 사항
분석의 정확도를 높이기 위해 아래 부상 사항을 참고해주세요.`

	AnalysisPrompt = `
# 운동 영상 분석 요청

## 분석 요청 사항
당신은 전문 스포츠 생체역학 전문가이자 코치입니다. 위 컨텍스트와 첨부된 영상을 바탕으로 다음 항목을 분석해 주세요.

1. **동작 분석 및 체형 평가 (Movement & Posture Analysis)**:
   - 전반적인 자세의 정확도와 가동 범위를 평가해주세요.
   - 평소 체형의 불균형이나 의심되는 증상(예: 거북목, 라운드 숄더, 일자허리, 골반 비대칭 등)이 관찰된다면 함께 짚어주세요.
   - (입력된 운동 종목이 있다면) 해당 종목의 표준 기술(Standard)과 비교해 주세요.

2. **강점 및 약점 (Strengths & Weaknesses)**:
   - 동작 수행 중 잘 유지되고 있는 부분(Core 안정성, 리듬 등)은 무엇인가요?
   - 자세가 무너지거나 힘의 누수가 발생하는 약점은 무엇인가요?

3. **피로도 및 페이스 분석 (Fatigue Analysis)**:
   - 수행 속도가 눈에 띄게 느려지거나 자세가 흐트러지기 시작하는 **정확한 시점(분:초)**을 지목해주세요.
   - 피로가 자세에 어떤 영향을 미쳤는지(예: 등이 굽음, 무릎이 모임) 설명해 주세요.

4. **개선 솔루션 (Actionable Feedback)**:
   - 다음에 이 운동을 할 때 즉시 적용할 수 있는 구체적인 팁을 3가지 제안해주세요.
   - 입력된 **운동 목표**가 있다면, 그 목표 달성을 위한 전략적 조언을 포함해 주세요.

5. **핵심 구간 타임스탬프 (Key Timestamps)**:
   - 피드백과 관련된 비디오의 중요 구간(시작 시간 - 종료 시간)을 나열하고, 해당 구간을 주목해야 하는 이유를 한 문장으로 요약해 주세요.`

	HighlightSelectionPrompt = `

7. **하이라이트 구간 (Highlight Segments)**:
   - 소셜 미디어 공유나 퍼포먼스 비교에 적합한 핵심 구간을 선별하세요.
   - 카테고리: best_form (가장 좋은 자세), worst_form (가장 나쁜 자세), fatigue_point (피로 시작 지점), key_moment (핵심 순간)
   - 영상 길이를 고려하여 **각 카테고리당 가능한 한 2개 이상의 구간**을 찾아주세요.
   - 각 구간은 3~15초 권장, 전체 시간 합계 제한은 없습니다. 자유롭게 유의미한 구간을 모두 추출하세요.
   - 반드시 아래 형식의 **highlights** JSON 코드 블록으로 출력하세요 (json이 아닌 highlights 태그 사용):
` + "```highlights\n" + `[{"start":"0:15","end":"0:28","type":"best_form","reason":"완벽한 스내치 풀 익스텐션"},{"start":"1:10","end":"1:20","type":"best_form","reason":"코어가 매우 안정적인 두번째 움직임"},{"start":"2:30","end":"2:45","type":"worst_form","reason":"무릎 내전과 등 굽음 관찰"}]` + "\n```"

	InjuryTimestampPrompt = `

6. **부상 관련 타임스탬프 (Injury-Relevant Timestamps)**:
   - 아래 부상 부위가 활발히 사용되거나 위험에 노출되는 구간을 JSON 배열로 출력하세요.
   - 반드시 아래 형식의 **injury_timestamps** JSON 코드 블록으로 출력하세요 (json이 아닌 injury_timestamps 태그 사용):
` + "```injury_timestamps\n" + `[{"start": "0:32", "end": "0:45", "reason": "무릎 내전 관찰"}]` + "\n```"
)

// highlightBlockRegex matches fenced ```highlights ... ``` blocks in Gemini output.
var highlightBlockRegex = regexp.MustCompile("(?is)```highlights\\s*(\\[.*?\\])\\s*```")

// injuryTimestampBlockRegex matches fenced ```injury_timestamps ... ``` blocks in Gemini output.
var injuryTimestampBlockRegex = regexp.MustCompile("(?is)```injury_timestamps\\s*(\\[.*?\\])\\s*```")

func NewVideoAnalysisTask(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint) (*asynq.Task, error) {
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

	safeSessionID := filepath.Base(p.SessionID)
	if strings.ContainsRune(safeSessionID, filepath.Separator) {
		w.logger.Error("Invalid session ID: contains path separator after sanitization", zap.String("session_id", p.SessionID))
		return fmt.Errorf("invalid session ID: %w", asynq.SkipRetry)
	}

	if w.UseCache {
		w.logger.Info("Using two-pass analysis for video", zap.String("session_id", p.SessionID))
		return w.handleVideoAnalysisTwoPass(ctx, p)
	}

	return w.handleVideoAnalysisLegacy(ctx, p, safeSessionID)
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
## Expected Exercises
The workout is expected to include: %s
Use these as a hint for identifying segments, but also capture any other exercises performed.
`, strings.Join(p.Movements, ", "))
	}

	prompt += `
## Instructions
1. First, scrub through the entire video frame by frame and create a brief text timeline.
2. For EACH segment you identify, describe the specific visual evidence you see (equipment, body position, movement pattern).
3. Only report exercises you can visually confirm — do NOT guess or infer exercises from context.
4. Output a strictly formatted JSON array of the segments.
5. Use "MM:SS" format for timestamps.
6. Each segment should be at least 10 seconds long.

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
func (w *Worker) handleVideoAnalysisTwoPass(ctx context.Context, p VideoAnalysisPayload) error {
	safeSessionID := filepath.Base(p.SessionID)
	localFilePath := filepath.Join("/tmp", fmt.Sprintf("%s_%s", strings.ReplaceAll(safeSessionID, ".", "_"), filepath.Base(p.FilePath)))

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

	// Upload to Gemini Files API
	upload, err := w.GeminiClient.UploadVideo(ctx, localFilePath)
	if err != nil {
		return fmt.Errorf("failed to upload video: %w", err)
	}
	// Defer file cleanup: if injury analysis needs the file, it takes ownership
	hasInjuries := len(p.Injuries) > 0
	if !hasInjuries {
		defer func() {
			if err := w.GeminiClient.DeleteFile(ctx, upload.FileName); err != nil {
				w.logger.Error("Failed to delete file from Gemini", zap.Error(err))
			}
		}()
	}

	// ── Pass 1: Build segment index ──
	// Prefer chunk analysis data from DB (app-recorded, accurate timestamps).
	// Fall back to Flash model indexing only when no chunks exist.
	segments := w.buildSegmentsFromChunks(p.SessionID)

	if len(segments) > 0 {
		w.logger.Info("Pass 1: Segments built from chunk analysis data",
			zap.String("session_id", p.SessionID),
			zap.Int("count", len(segments)))
	} else {
		// Fallback: use Flash model to index the video
		w.logger.Info("Pass 1: No chunks found, indexing video with model",
			zap.String("session_id", p.SessionID),
			zap.String("file_uri", upload.FileURI))

		indexOutput, indexErr := w.GeminiClient.IndexVideo(ctx, upload.FileURI, upload.MIMEType, w.buildIndexPrompt(p, upload.VideoDuration))
		if indexErr != nil {
			return fmt.Errorf("failed to index video: %w", indexErr)
		}

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

		triagedSegments, triageErr := w.triageSegments(ctx, upload, p, segments, maxSegs)
		if triageErr != nil {
			w.logger.Warn("Segment triage failed, using first N segments",
				zap.Error(triageErr),
				zap.Int("fallback_count", maxSegs))
			segments = segments[:maxSegs]
		} else {
			segments = triagedSegments
		}

		w.logger.Info("Segments after triage",
			zap.Int("count", len(segments)),
			zap.Any("segments", segments))
	}

	// ── Pass 2: Analyze each segment with Pro ──
	var allAnalysis strings.Builder
	for i, seg := range segments {
		start := convertToSeconds(seg.Start)
		end := convertToSeconds(seg.End)

		segPrompt := w.buildSegmentAnalysisPrompt(p, seg)

		w.logger.Info("Pass 2: Analyzing segment",
			zap.Int("segment", i+1),
			zap.Int("total", len(segments)),
			zap.String("type", seg.Type),
			zap.Duration("start", start),
			zap.Duration("end", end))

		segAnalysis, err := w.GeminiClient.AnalyzeSegment(
			ctx, upload.FileURI, upload.MIMEType, start, end, segPrompt,
		)
		if err != nil {
			w.logger.Error("Segment analysis failed, skipping",
				zap.Int("segment", i+1),
				zap.String("type", seg.Type),
				zap.Error(err))
			continue
		}

		allAnalysis.WriteString(fmt.Sprintf("\n\n---\n## 세그먼트 %d: %s (%s ~ %s)\n\n", i+1, seg.Type, seg.Start, seg.End))
		allAnalysis.WriteString(segAnalysis)
	}

	analysis := allAnalysis.String()
	if analysis == "" {
		return fmt.Errorf("all segment analyses failed")
	}

	highlightSegments := parseHighlightSegments(analysis)

	result := &db.AnalysisResult{
		SessionID:         p.SessionID,
		Status:            "COMPLETED",
		Output:            analysis,
		AnalysisType:      db.AnalysisTypeWOD,
		HighlightSegments: highlightSegments,
	}
	if p.ProfileID > 0 {
		result.ProfileID = &p.ProfileID
	}
	w.DB.Create(result)

	w.logger.Info("Two-pass analysis completed",
		zap.String("session_id", p.SessionID),
		zap.Int("segments_analyzed", len(segments)))

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

	return nil
}

// buildSegmentAnalysisPrompt builds the analysis prompt for a specific segment.
func (w *Worker) buildSegmentAnalysisPrompt(p VideoAnalysisPayload, seg Segment) string {
	personalProfile := w.lookupProfileString(p.ProfileID)

	prompt := fmt.Sprintf(`# 운동 영상 분석 요청
전문 스포츠 생체역학 전문가로서 이 '%s' 운동 구간(%s ~ %s)을 분석해 주세요.

## 개인 프로필
%s
`, seg.Type, seg.Start, seg.End, personalProfile)

	prompt += AnalysisPrompt

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n   - 부상 부위: %s", InjuryTimestampPrompt, strings.Join(p.Injuries, ", "))
	}

	prompt += HighlightSelectionPrompt

	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf("%s\n## 운동 종목: %s", MovementPrompt, strings.Join(p.Movements, ", "))
	}

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n## 알려진 부상 사항: %s", KnownInjuriesPrompt, strings.Join(p.Injuries, ", "))
	}

	prompt += fmt.Sprintf(`

## 중요: 타임스탬프 규칙
- 이 구간은 %s ~ %s 입니다.
- 모든 타임스탬프는 이 범위 안에서만 지정하세요.
- 클립 내의 초 단위로 부상 위험이 있는 정확한 시점을 설명하세요.`, seg.Start, seg.End)

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

// buildSegmentsFromChunks queries completed chunk analysis records from the DB
// and converts them to Segment structs. Since chunks have app-recorded start/end
// timestamps, this is far more reliable than asking a model to index a long video.
// Chunks with no detected exercise (rest periods) are filtered out.
func (w *Worker) buildSegmentsFromChunks(sessionID string) []Segment {
	if w.DB == nil {
		return nil
	}

	var chunks []db.ChunkAnalysisResult
	err := w.DB.Where("session_id = ? AND status = ?", sessionID, "COMPLETED").
		Order("start_secs ASC").
		Find(&chunks).Error
	if err != nil || len(chunks) == 0 {
		return nil
	}

	var segments []Segment
	for _, chunk := range chunks {
		if chunk.StartSecs == nil || chunk.EndSecs == nil {
			continue
		}

		// Skip chunks with no detected exercise (rest, walking, setup, etc.)
		if chunk.ExerciseType == "" {
			continue
		}

		startMM := int(*chunk.StartSecs) / 60
		startSS := int(*chunk.StartSecs) % 60
		endMM := int(*chunk.EndSecs) / 60
		endSS := int(*chunk.EndSecs) % 60

		// Use the chunk output (1-2 sentence coaching feedback) as the description.
		desc := chunk.Output
		if len(desc) > 200 {
			desc = desc[:200] + "..."
		}

		segments = append(segments, Segment{
			Start:       fmt.Sprintf("%d:%02d", startMM, startSS),
			End:         fmt.Sprintf("%d:%02d", endMM, endSS),
			Type:        chunk.ExerciseType,
			Description: desc,
		})
	}

	// Merge consecutive chunks with the same exercise type into larger segments
	// to avoid excessive Pro model calls (e.g. 30 Snatch chunks → 1 Snatch segment)
	return mergeSegmentsByMovement(segments)
}

// mergeSegmentsByMovement combines consecutive segments that have the same exercise
// type (case-insensitive) into single larger segments. Unlike time-based merging,
// this correctly handles 10s chunks that are always contiguous — it splits when the
// movement changes, not by time gap.
func mergeSegmentsByMovement(segments []Segment) []Segment {
	if len(segments) <= 1 {
		return segments
	}

	var merged []Segment
	current := segments[0]

	for i := 1; i < len(segments); i++ {
		// Merge if the exercise type is the same (case-insensitive)
		if strings.EqualFold(current.Type, segments[i].Type) {
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
func (w *Worker) triageSegments(ctx context.Context, upload *gemini.UploadResult, p VideoAnalysisPayload, segments []Segment, maxSegs int) ([]Segment, error) {
	triagePrompt := buildTriagePrompt(segments, maxSegs, upload.VideoDuration)

	triageOutput, err := w.GeminiClient.IndexVideo(ctx, upload.FileURI, upload.MIMEType, triagePrompt)
	if err != nil {
		return nil, fmt.Errorf("triage model call failed: %w", err)
	}

	selected := parseTriagedSegments(triageOutput, segments, maxSegs)
	if len(selected) == 0 {
		return nil, fmt.Errorf("triage returned no valid segments")
	}

	return selected, nil
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
2. **Fatigue indicators** - segments where the athlete shows signs of fatigue (slower reps, form breakdown)
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

// convertToSeconds parses "MM:SS" format to time.Duration.
func convertToSeconds(input string) time.Duration {
	input = strings.TrimSpace(input)
	if strings.Contains(input, ":") {
		parts := strings.Split(input, ":")
		if len(parts) == 2 {
			mins, _ := strconv.Atoi(parts[0])
			secs, _ := strconv.Atoi(parts[1])
			return time.Duration((mins*60)+secs) * time.Second
		}
	}
	clean := strings.TrimSuffix(input, "s")
	if n, err := strconv.Atoi(clean); err == nil {
		return time.Duration(n) * time.Second
	}
	return 0
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
func (w *Worker) handleVideoAnalysisLegacy(ctx context.Context, p VideoAnalysisPayload, safeSessionID string) error {
	localFilePath := filepath.Join("/tmp", fmt.Sprintf("%s_%s", strings.ReplaceAll(safeSessionID, ".", "_"), filepath.Base(p.FilePath)))

	w.logger.Info("Downloading file from GCS", zap.String("uri", p.FilePath), zap.String("dest", localFilePath))
	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download file from GCS: %w", err)
	}

	videoDuration := probeVideoDuration(ctx, localFilePath)
	w.logger.Info("Probed video duration", zap.Float64("duration_secs", videoDuration))

	prompt := w.buildAnalysisPrompt(p, videoDuration)

	analysis, geminiFile, err := w.GeminiClient.AnalyzeVideo(ctx, localFilePath, prompt)

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
		if p.ProfileID > 0 {
			failedResult.ProfileID = &p.ProfileID
		}
		w.DB.Create(failedResult)

		return err
	}

	// Parse highlight segments from analysis output
	highlightSegments := parseHighlightSegments(analysis)

	result := &db.AnalysisResult{
		SessionID:         p.SessionID,
		Status:            "COMPLETED",
		Output:            analysis,
		AnalysisType:      db.AnalysisTypeWOD,
		HighlightSegments: highlightSegments,
	}
	if p.ProfileID > 0 {
		result.ProfileID = &p.ProfileID
	}
	w.DB.Create(result)

	w.logger.Info("Analysis completed", zap.String("session_id", p.SessionID), zap.String("analysis", analysis))

	// Agentic follow-up: if injuries are present, enqueue injury-focused re-analysis
	if len(p.Injuries) > 0 {
		focusTimestamps := parseInjuryTimestamps(analysis)
		injuryTask, taskErr := NewInjuryAnalysisTask(p.SessionID, p.FilePath, p.Injuries, p.ProfileID, focusTimestamps)
		if taskErr != nil {
			w.logger.Error("Failed to create injury analysis task", zap.Error(taskErr))
		} else if _, enqErr := w.QueueClient.Enqueue(injuryTask); enqErr != nil {
			w.logger.Error("Failed to enqueue injury analysis task", zap.Error(enqErr))
		} else {
			w.logger.Info("Injury analysis enqueued",
				zap.String("session_id", p.SessionID),
				zap.Strings("injuries", p.Injuries),
				zap.String("focus_timestamps", focusTimestamps))
		}
	}

	return nil
}

func (w *Worker) buildAnalysisPrompt(p VideoAnalysisPayload, videoDurationSecs float64) string {
	prompt := AnalysisPrompt

	// When injuries are present, add a section requesting structured injury timestamps
	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n   - 부상 부위: %s", InjuryTimestampPrompt, strings.Join(p.Injuries, ", "))
	}

	// Always request highlight segments for short-form video generation
	prompt += HighlightSelectionPrompt

	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf("%s\n## 운동 종목: %s", MovementPrompt, strings.Join(p.Movements, ", "))
	}

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n## 알려진 부상 사항: %s", KnownInjuriesPrompt, strings.Join(p.Injuries, ", "))
	}

	personalProfile := w.lookupProfileString(p.ProfileID)

	w.logger.Info("Personal Profile", zap.Uint("profile_id", p.ProfileID), zap.String("personal_profile", personalProfile))

	prompt += fmt.Sprintf("%s\n## 개인 프로필: %s", PersonalProfilePrompt, personalProfile)

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


// parseHighlightSegments extracts the JSON array from the ```highlights``` code block
// in the WOD analysis output. Returns the raw JSON string, or empty on failure.
func parseHighlightSegments(analysisOutput string) string {
	match := highlightBlockRegex.FindStringSubmatch(analysisOutput)
	if match == nil {
		return ""
	}
	// Sanity-check: must be a non-empty array of valid HighlightSegments.
	var parsed []HighlightSegment
	if err := json.Unmarshal([]byte(match[1]), &parsed); err != nil || len(parsed) == 0 {
		return ""
	}
	return match[1]
}

// parseInjuryTimestamps extracts the JSON array from the ```injury_timestamps``` code block
// in the WOD analysis output. Returns the raw JSON string, or empty on failure.
func parseInjuryTimestamps(analysisOutput string) string {
	match := injuryTimestampBlockRegex.FindStringSubmatch(analysisOutput)
	if match == nil {
		return ""
	}
	// Sanity-check: must be a non-empty JSON array.
	var parsed []json.RawMessage
	if err := json.Unmarshal([]byte(match[1]), &parsed); err != nil || len(parsed) == 0 {
		return ""
	}
	return match[1]
}
