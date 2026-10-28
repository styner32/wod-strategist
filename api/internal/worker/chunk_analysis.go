package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const ChunkAnalysisPrompt = `
# 운동 청크 영상 분석 요청 (실시간 피드백)

당신은 현재 영상에서 직접 보이는 운동 동작을 기록하고 짧은 자세 큐를 제공하는 관찰자입니다.
방금 수행된 약 10초 분량의 짧은 영상 클립이 주어집니다.

## 중요 원칙
- 이 사용자에 대한 과거 데이터가 없습니다. 평균, 벤치마크, 또래 비교를 하지 마세요.
- 현재 영상에서 관찰되는 것만 기반으로 판단하세요.
- 안전이 최우선: 부상 위험이 보이면 레벨과 관계없이 즉시 경고하세요.
- 한 번에 하나의 큐만 제시하세요.
- 모든 피드백은 **존댓말** (~요, ~세요, ~습니다)로 작성하세요. 반말(~해, ~다, ~함)을 절대 사용하지 마세요.

## 대상 인물
이 영상에는 대상 인물 외의 사람이 함께 찍힐 수 있습니다.
아래 컨텍스트에 지정된 대상 인물의 동작만 분석하세요.
대상 인물을 특정할 수 없으면 배경 인물의 동작을 대신 보고하지 말고 [NO_EXERCISE]를 출력하세요.

## 필수: 운동 종목 식별
먼저, 영상에서 수행 중인 운동 종목을 식별하세요.
- 운동이 보이면 → 첫 줄에 반드시 [EXERCISE: 영어 운동 이름] 태그를 출력하세요.
  (예: [EXERCISE: Hang Power Clean], [EXERCISE: Push Jerk], [EXERCISE: Deadlift], [EXERCISE: Power Snatch], [EXERCISE: Back Squat], [EXERCISE: Pull-up], [EXERCISE: Burpee])
  * 클린과 스내치는 포괄적인 [EXERCISE: Hang Clean] 또는 [EXERCISE: Snatch]를 쓰지 말고, [EXERCISE: Hang Power Clean], [EXERCISE: Hang Squat Clean], [EXERCISE: Power Clean], [EXERCISE: Squat Clean], [EXERCISE: Hang Power Snatch], [EXERCISE: Hang Squat Snatch] 등으로 구체적인 표준 종목명을 쓰세요.
  * 바벨/덤벨을 손으로 쥐고 세트를 이어가는 구간은 기구의 바닥 접촉 여부와 관계없이 운동 진행 중([EXERCISE: ...])입니다. 행(Hang) 위치에서의 유지뿐 아니라 바닥에 둔 기구의 그립을 유지한 채 반복 사이에 잠시 멈추는 구간도 포함하세요. 단순한 장비 세팅·정리나 휴식은 구분하세요. 손이 가려졌다는 이유로 기구를 놓았다고 추정하지 마세요.
  * 로잉(Row), 바이크 에르그(Bike Erg), 에코 바이크(Echo Bike), 스키에르그(Skierg)에 앉거나 선 채로 핸들·핸들바·페달과 접촉해 팔·다리·상체 움직임이 이어지면 앉아 있어도 운동 진행 중([EXERCISE: ...])입니다. 속도가 느려진 것은 휴식이 아닙니다. 기구에서 손발을 떼고 내려왔을 때만 [NO_EXERCISE]를 출력하세요.
  * 후방 촬영으로 대상 인물의 몸통이 바벨 봉을 가리더라도 양쪽 원판이 바닥에서 들리거나 고관절·어깨의 반복적인 굽힘·폄(Deadlift, Clean 등)이 이어지면 운동 진행 중([EXERCISE: ...])입니다. 벽 물구나무(Handstand Push-up)나 바닥 동작(Burpee, Push-up)도 머리·발끝의 상하 이동이 보이면 [NO_EXERCISE]로 단정하지 마세요.
- 대상 인물이 운동 중인 것은 분명하지만 정확한 종목 근거가 부족하면 → 첫 줄에 [EXERCISE: Unknown] 태그를 출력하세요.
- 운동이 보이지 않으면 (기구를 놓고 쉬는 구간, 운동과 무관한 걷기, 장비 세팅·정리, 촬영 범위 밖 등) → 첫 줄에 [NO_EXERCISE] 태그를 출력하세요.

## 코칭 피드백 규칙
- 운동이 감지된 경우: [EXERCISE: ...] 태그 다음 줄에 **반드시 1~2문장**으로만 코칭 피드백을 답하세요.
- 운동이 감지되지 않은 경우: [NO_EXERCISE] 태그 뒤에 코칭 피드백 문장은 작성하지 마세요 (아래 요청된 JSON 블록만 각 규칙에 따라 출력하세요).
- 아래 컨텍스트를 참고하되, 컨텍스트 자체를 반복하지 마세요.
- 부상 사항이 있으면 → 해당 부위에 위험한 움직임이 보일 때만 안전 경고를 우선하세요.`

// Level policy prompts appended based on user's fitness level.
const (
	LevelPolicyBeginner = `

## 레벨 정책: 초급 (Beginner)
- 자세와 좌우 균형에만 집중하세요. 페이스는 무시하세요.
- 자세 문제가 보이면 → 가장 중요한 한 가지만 교정 큐를 제시하세요.
- 자세가 좋으면 → 현재 보이는 좋은 정렬이나 동작을 짧게 격려하세요. 영상에서 특정 근육이 약하다고 판단하지 마세요.
- 절대로 초급자에게 속도를 올리거나 무게를 추가하라고 하지 마세요.
- 어조: 따뜻하고, 인내심 있고, 격려하는. "~요" 존댓말 사용.`

	LevelPolicyIntermediate = `

## 레벨 정책: 중급 (Intermediate)
- 자세는 의미 있는 문제가 있을 때만 언급하세요.
- 현재 구간의 반복 시각, 정지 시간과 보이는 정렬만 관찰하세요.
- 보이는 정렬 변화가 있으면 한 가지 동작 큐를 제시하세요. 동작이 일정하면 유지되는 궤적이나 캐치 위치를 짧게 격려하세요.
- 표정이나 멈춤에서 수행 여유·피로·강도를 추론하거나 더 빠른 속도/무게 증가를 요구하지 마세요.
- 어조: 직접적이고 간결한 존댓말 명령형. "~하세요" 스타일. 반말 금지.`

	LevelPolicyAdvanced = `

## 레벨 정책: 고급 (Advanced)
- 자세가 깨끗하고 페이스가 일정하면 → "좋아요, 유지" 정도의 최소 피드백만. 불필요한 코칭 자제.
- 정렬·캐치·기구 위치의 변화가 직접 보일 때만 한 가지 동작 큐를 제시하세요.
- 반복 속도나 정지 시간의 변화는 현상만 기록하고 피로·힘 저하로 해석하지 마세요. 더 빠른 페이스, 무게 증가 또는 끝까지 밀어붙이기를 지시하지 마세요.
- 권고는 제안이 아닌 지시형으로.
- 어조: 단호하고, 요구하는, 최소한의 단어. 존댓말 유지 ("~하세요", "~입니다"). 칭찬은 확실히 잘했을 때만.`

	// ObservedSignalsPrompt instructs Gemini to emit structured workout metrics
	// for benchmarking. Appended to the chunk analysis prompt when exercise is expected.
	ObservedSignalsPrompt = `

## 관찰 신호 기록 (Observed Signals)
운동이 감지된 경우, 코칭 피드백 아래에 아래 형식의 JSON 블록을 **반드시** 추가하세요.
정확한 숫자가 아니어도 됩니다. 영상에서 관찰한 대략적인 추정치를 기록하세요.

` + "```observed_signals\n" + `{
  "movement": "운동명 (영어)",
  "activity_state": "exercise|walking|rest_setup|unknown",
  "set_duration_s": 10,
  "rep_count": 0,
  "avg_cadence_s": 0.0,
  "exertion_estimate": "unknown",
  "form_issues_seen": ["이슈1", "이슈2"],
  "fatigue_visually_established": false,
  "fatigue_evidence_types": [],
  "fatigue_evidence": []
}` + "\n```" + `

- movement: 감지된 운동 종목 (영어)
- activity_state: exercise, walking, rest_setup, unknown 중 하나
- set_duration_s: 이 클립에서 운동이 수행된 대략적인 시간 (초)
- rep_count: 이 클립에서 관찰된 대략적인 반복 횟수 (불분명하면 0)
- avg_cadence_s: 반복당 대략적인 소요 시간 (초, 불분명하면 0)
- exertion_estimate: 관찰 단계에서는 수행 강도를 평가하지 않으므로 unknown으로 기록하세요. unknown은 낮은 강도나 강도 0을 뜻하지 않습니다.
- form_issues_seen: 관찰된 자세 문제 목록 (없으면 빈 배열)
- fatigue_visually_established: 기존 응답 형식을 유지하는 필드입니다. 새 관찰 응답에서는 false로 기록하세요. 이는 피로가 없다는 뜻이 아니라 관찰 단계에서 피로를 판정하지 않았다는 뜻입니다.
- fatigue_evidence_types, fatigue_evidence: 새 관찰 응답에서는 빈 배열로 기록하세요. 보이는 정렬 문제는 form_issues_seen, 시간 정보는 set_duration_s/avg_cadence_s에 원인 해석 없이 남기세요.
- 운동이 감지되지 않은 경우에는 이 블록을 출력하지 마세요.`

	TargetPersonPrompt = `

## 대상 인물 식별 근거 (Target Person)
운동이 감지된 경우, 아래 형식의 JSON 블록을 반드시 추가하세요.

` + "```target_person\n" + `{"matched_cues":["흰색 나이키","짧은 검은 머리"],"ambiguous":false,"confidence":0.9}` + "\n```\n" + `
- matched_cues: 위 대상 인물 컨텍스트 항목 중 이 영상에서 **실제로 눈으로 확인한 것만** 나열하세요.
  컨텍스트에 있다는 이유로 나열하지 마세요. 별도 외형 컨텍스트가 없거나 확인한 항목이 없으면 빈 배열([])입니다.
- ambiguous: 조건에 맞는 인물이 여러 명이거나 대상을 확신할 수 없으면 true.
- confidence: 대상 인물이 한 명으로 명확히 특정되는 확신도(0.0~1.0). 별도 외형 컨텍스트가 없더라도 전면/중앙에서 주요 운동을 수행하는 대상 인물이 1명으로 명확하면 0.85~0.95, 후보가 여러 명이거나 불확실하면 0.5 이하로 두세요.`
)

// observedSignalsBlockRegex matches ```observed_signals ... ``` blocks in Gemini output.
// Also handles variations: ```json, ``` with extra whitespace/newlines, etc.
var observedSignalsBlockRegex = regexp.MustCompile("(?is)```(?:observed_signals|json)?\\s*\\n?(\\{[^`]*?\"movement\"[^`]*?\\})\\s*\\n?```")

// exerciseTagRegex matches [EXERCISE: <name>] tags in chunk analysis output.
var exerciseTagRegex = regexp.MustCompile(`(?i)\[EXERCISE:\s*(.+?)\]`)

// noExerciseTag is the tag indicating no exercise was detected in the chunk.
const noExerciseTag = "[NO_EXERCISE]"

// parseChunkExercise extracts the detected exercise name from chunk analysis output.
// Returns the exercise name (e.g. "Snatch") or empty string if no exercise detected.
func parseChunkExercise(analysis string) string {
	if strings.Contains(strings.ToUpper(analysis), noExerciseTag) {
		return ""
	}
	match := exerciseTagRegex.FindStringSubmatch(analysis)
	if len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// stripExerciseTag removes the [EXERCISE: ...] or [NO_EXERCISE] tag from the output,
// leaving just the coaching feedback.
func stripExerciseTag(analysis string) string {
	// Remove [EXERCISE: ...] tag
	result := exerciseTagRegex.ReplaceAllString(analysis, "")
	// Remove [NO_EXERCISE] tag (case insensitive)
	result = strings.ReplaceAll(result, noExerciseTag, "")
	result = strings.ReplaceAll(result, strings.ToLower(noExerciseTag), "")
	return strings.TrimSpace(result)
}

// parseObservedSignals extracts the observed_signals JSON block from chunk analysis output.
// Returns the raw JSON string, or "{}" if no block was found.
func parseObservedSignals(analysis string) string {
	match := observedSignalsBlockRegex.FindStringSubmatch(analysis)
	if len(match) > 1 {
		raw := strings.TrimSpace(match[1])
		return sanitizeObservedSignals(raw)
	}
	return "{}"
}

// stripObservedSignals removes the ```observed_signals ... ``` block from the output,
// leaving just the coaching feedback.
func stripObservedSignals(analysis string) string {
	return strings.TrimSpace(observedSignalsBlockRegex.ReplaceAllString(analysis, ""))
}

var (
	targetPersonExplicitBlockRegex = regexp.MustCompile("(?is)```target_person\\s*\\n?(\\{[^`]*\\})\\s*\\n?```")
	targetPersonBlockRegex         = regexp.MustCompile("(?is)```(?:target_person|json)?\\s*\\n?(\\{[^`]*?(?:\"matched_cues\"|\"boxes\")[^`]*?\\})\\s*\\n?```")
)

type targetPersonOutput struct {
	MatchedCues []string `json:"matched_cues"`
	Ambiguous   bool     `json:"ambiguous"`
	Confidence  float64  `json:"confidence"`
	// Tracking channel (only when tracking continuity is enabled): where the
	// person is, never how they move.
	Boxes          []trackingBox `json:"boxes,omitempty"`
	FrameRegion    string        `json:"frame_region,omitempty"`
	Station        string        `json:"station,omitempty"`
	ReferenceImage string        `json:"reference_image,omitempty"`
}

func extractTargetPersonJSONBlocks(analysis string) []string {
	var rawBlocks []string
	seen := map[string]struct{}{}
	for _, re := range []*regexp.Regexp{targetPersonExplicitBlockRegex, targetPersonBlockRegex} {
		for _, m := range re.FindAllStringSubmatch(analysis, -1) {
			if len(m) < 2 {
				continue
			}
			trimmed := strings.TrimSpace(m[1])
			if _, ok := seen[trimmed]; ok || trimmed == "" {
				continue
			}
			seen[trimmed] = struct{}{}
			rawBlocks = append(rawBlocks, trimmed)
		}
	}
	return rawBlocks
}

func parseTargetPerson(analysis string) (string, float64) {
	rawBlocks := extractTargetPersonJSONBlocks(analysis)
	if len(rawBlocks) == 0 {
		return "{}", 0.5
	}

	var merged targetPersonOutput
	parsedAny := false
	for _, raw := range rawBlocks {
		var candidate targetPersonOutput
		if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
			continue
		}
		if !parsedAny {
			merged = candidate
			parsedAny = true
			continue
		}
		if len(candidate.MatchedCues) > 0 {
			merged.MatchedCues = candidate.MatchedCues
		}
		if candidate.Ambiguous {
			merged.Ambiguous = true
		}
		if candidate.Confidence > 0 {
			merged.Confidence = candidate.Confidence
		}
		if boxes := validTrackingBoxes(candidate.Boxes); len(boxes) > 0 {
			merged.Boxes = boxes
		}
		if candidate.FrameRegion != "" {
			merged.FrameRegion = candidate.FrameRegion
		}
		if candidate.Station != "" {
			merged.Station = candidate.Station
		}
	}
	if !parsedAny {
		return "{}", 0.5
	}

	conf := merged.Confidence
	if conf < 0.0 {
		conf = 0.0
	}
	if conf > 1.0 {
		conf = 1.0
	}

	if merged.Ambiguous && conf > 0.5 {
		conf = 0.5
	}

	if merged.MatchedCues == nil {
		merged.MatchedCues = []string{}
	}
	merged.Confidence = conf
	merged.Boxes = validTrackingBoxes(merged.Boxes)
	merged.FrameRegion = sanitizeTrackingLabel(merged.FrameRegion)
	merged.Station = sanitizeTrackingLabel(merged.Station)
	merged.ReferenceImage = "" // set only by the worker after it renders the image
	cleanedJSON, err := json.Marshal(merged)
	if err != nil {
		return "{}", conf
	}
	return string(cleanedJSON), conf
}

func stripTargetPerson(analysis string) string {
	stripped := targetPersonExplicitBlockRegex.ReplaceAllString(analysis, "")
	stripped = targetPersonBlockRegex.ReplaceAllString(stripped, "")
	return strings.TrimSpace(stripped)
}

// levelPolicyForFitnessLevel returns the coaching policy prompt for the given fitness level.
func levelPolicyForFitnessLevel(level string) string {
	switch strings.ToLower(level) {
	case "beginner":
		return LevelPolicyBeginner
	case "advanced":
		return LevelPolicyAdvanced
	default:
		return LevelPolicyIntermediate
	}
}

func NewChunkAnalysisTask(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint, startSecs, endSecs float64, heartRateBPM int, wodDescription string, workoutConfidence float64) (*asynq.Task, error) {
	payload := VideoAnalysisPayload{
		SessionID:         sessionID,
		FilePath:          filePath,
		WorkoutType:       NormalizeWorkoutType(workoutType),
		Movements:         movements,
		Injuries:          injuries,
		ProfileID:         profileID,
		StartSecs:         startSecs,
		EndSecs:           endSecs,
		HeartRateBPM:      heartRateBPM,
		WODDescription:    wodDescription,
		WorkoutConfidence: workoutConfidence,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeChunkAnalysis, data), nil
}

func NewChunkAnalysisWithSessionTask(sessionID, filePath string, profileID uint, startSecs, endSecs float64, heartRateBPM int, workoutConfidence float64) (*asynq.Task, error) {
	payload := VideoAnalysisWithSessionPayload{
		SessionID:         sessionID,
		FilePath:          filePath,
		ProfileID:         profileID,
		StartSecs:         startSecs,
		EndSecs:           endSecs,
		HeartRateBPM:      heartRateBPM,
		WorkoutConfidence: workoutConfidence,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeChunkAnalysisWithSession, data), nil
}

func (w *Worker) HandleChunkAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	if handled, err := w.resumeLiveChunk(ctx, p.LiveAnalysisVersion, p); handled {
		return err
	}

	retryCount := getRetryCount(ctx)

	w.logger.Info("Processing chunk analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		w.logger.Error("Max retries reached for chunk analysis. Marking chunk as FAILED.",
			zap.String("session_id", p.SessionID),
			zap.String("file_path", p.FilePath))
		chunkFailed := &db.ChunkAnalysisResult{
			SessionID:         p.SessionID,
			FilePath:          p.FilePath,
			Status:            "FAILED",
			Output:            "Chunk analysis failed after max retries.",
			HeartRateBPM:      p.HeartRateBPM,
			WorkoutConfidence: p.WorkoutConfidence,
		}
		chunkFailed.ProfileID = p.ProfileID
		if p.StartSecs > 0 || p.EndSecs > 0 {
			chunkFailed.StartSecs = &p.StartSecs
			chunkFailed.EndSecs = &p.EndSecs
		}
		w.failedLiveObservations(p.LiveAnalysisVersion, chunkFailed)
		if err := w.persistChunkAnalysisResult(ctx, chunkFailed); err != nil {
			return fmt.Errorf("failed to record failed chunk analysis result: %w", err)
		}
		if err := w.refreshLiveActivity(ctx, p, chunkFailed); err != nil {
			return err
		}
		return asynq.SkipRetry
	}

	if !strings.HasPrefix(p.FilePath, "gs://") {
		return fmt.Errorf("invalid file path: %w", asynq.SkipRetry)
	}

	if err := validateSessionID(p.SessionID); err != nil {
		w.logger.Error("Invalid session ID", zap.String("session_id", p.SessionID))
		return err
	}

	localFilePath, err := createTempFile("chunk", ".mp4")
	if err != nil {
		return err
	}

	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download chunk file from GCS: %w", err)
	}

	// Start motion probe concurrently with the Gemini API call
	type probeResult struct {
		score float64
		err   error
	}
	probeChan := make(chan probeResult, 1)
	go func() {
		score, pErr := probeMotionScore(ctx, localFilePath)
		probeChan <- probeResult{score: score, err: pErr}
	}()

	prompt := w.buildChunkAnalysisPrompt(p) + w.livePrompt(p.LiveAnalysisVersion)
	trackingPrompt, trackingImages := w.trackingInputs(ctx, p.ProfileID, p.SessionID, p.WorkoutType, p.FilePath, p.StartSecs)
	prompt += trackingPrompt

	analysis, geminiFile, usage, err := w.analyzeChunkVideo(ctx, localFilePath, prompt, gemini.ModelFlash38, trackingImages)

	// Wait for motion probe to finish
	var motionScore *float64
	pRes := <-probeChan
	if pRes.err == nil {
		motionScore = &pRes.score
	} else {
		w.logger.Warn("motion probe failed for chunk analysis", zap.Error(pRes.err))
	}

	defer func() {
		os.Remove(localFilePath)
	}()

	if geminiFile != "" {
		defer func() {
			w.GeminiClient.DeleteFile(ctx, geminiFile)
		}()
	}

	// Save token usage regardless of analysis outcome
	w.saveTokenUsage(p.SessionID, p.ProfileID, "chunk:analysis", usage)

	if err != nil {
		w.logger.Error("Chunk analysis failed", zap.Error(err), zap.String("session_id", p.SessionID), zap.String("file_path", p.FilePath), zap.Int("retry_count", int(retryCount)))
		return err
	}

	if analysis == "" {
		w.logger.Warn("Chunk analysis returned empty output", zap.String("session_id", p.SessionID), zap.String("file_path", p.FilePath), zap.Int("retry_count", int(retryCount)))
		return fmt.Errorf("chunk analysis is empty")
	}

	liveRaw := analysis
	analysis = liveBlocks.ReplaceAllString(analysis, "")
	// Extract exercise type detected by the model from the response
	detectedExercise := parseChunkExercise(analysis)
	// Extract observed signals JSON for benchmarking
	observedSignals := parseObservedSignals(analysis)
	targetCues, targetConfidence := parseTargetPerson(analysis)
	// Strip tags, signals block, and target person block from the output, leaving just coaching feedback
	cleanOutput := stripExerciseTag(analysis)
	cleanOutput = stripObservedSignals(cleanOutput)
	cleanOutput = stripTargetPerson(cleanOutput)

	chunkResult := &db.ChunkAnalysisResult{
		SessionID:         p.SessionID,
		FilePath:          p.FilePath,
		ExerciseType:      detectedExercise,
		Status:            "COMPLETED",
		Output:            cleanOutput,
		ObservedSignals:   observedSignals,
		HeartRateBPM:      p.HeartRateBPM,
		WorkoutConfidence: p.WorkoutConfidence,
		TargetConfidence:  targetConfidence,
		TargetCues:        targetCues,
		MotionScore:       motionScore,
		SkipReason:        "",
	}
	chunkResult.ProfileID = p.ProfileID
	if p.StartSecs > 0 || p.EndSecs > 0 {
		chunkResult.StartSecs = &p.StartSecs
		chunkResult.EndSecs = &p.EndSecs
	}
	duration := 0.0
	if p.LiveAnalysisVersion == 1 && w.ActivityCountingEnabled {
		duration = probeVideoDuration(ctx, localFilePath)
	}
	w.parseLiveFeedback(liveRaw, p.LiveAnalysisVersion, duration, chunkResult)
	if err := w.persistChunkAnalysisResult(ctx, chunkResult); err != nil {
		return fmt.Errorf("failed to save chunk analysis result: %w", err)
	}
	// Tracking channel for the next chunk; never fails this chunk.
	w.saveTrackingReference(ctx, localFilePath, chunkResult)

	if err := w.refreshLiveActivity(ctx, p, chunkResult); err != nil {
		return err
	}
	// Bounded recent-window coaching is a separate post-observation result.
	w.addContextualCoaching(ctx, p.LiveAnalysisVersion, chunkResult)

	w.logger.Info("Chunk analysis completed",
		zap.String("session_id", p.SessionID),
		zap.String("detected_exercise", detectedExercise),
		zap.String("observed_signals", observedSignals))
	return nil
}

func (w *Worker) HandleChunkAnalysisWithSessionTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisWithSessionPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	if handled, err := w.resumeLiveChunk(ctx, p.LiveAnalysisVersion, VideoAnalysisPayload{ProfileID: p.ProfileID, SessionID: p.SessionID, FilePath: p.FilePath}); handled {
		return err
	}

	retryCount := getRetryCount(ctx)

	w.logger.Info("Processing chunk analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		w.logger.Error("Max retries reached for chunk analysis with session. Marking chunk as FAILED.",
			zap.String("session_id", p.SessionID),
			zap.String("file_path", p.FilePath))
		chunkFailed := &db.ChunkAnalysisResult{
			SessionID:         p.SessionID,
			FilePath:          p.FilePath,
			Status:            "FAILED",
			Output:            "Chunk analysis failed after max retries.",
			HeartRateBPM:      p.HeartRateBPM,
			WorkoutConfidence: p.WorkoutConfidence,
		}
		chunkFailed.ProfileID = p.ProfileID
		if p.StartSecs > 0 || p.EndSecs > 0 {
			chunkFailed.StartSecs = &p.StartSecs
			chunkFailed.EndSecs = &p.EndSecs
		}
		w.failedLiveObservations(p.LiveAnalysisVersion, chunkFailed)
		if err := w.persistChunkAnalysisResult(ctx, chunkFailed); err != nil {
			return fmt.Errorf("failed to record failed chunk analysis result: %w", err)
		}
		if err := w.refreshLiveActivity(ctx, VideoAnalysisPayload{ProfileID: p.ProfileID, SessionID: p.SessionID}, chunkFailed); err != nil {
			return err
		}
		return asynq.SkipRetry
	}

	if !strings.HasPrefix(p.FilePath, "gs://") {
		return fmt.Errorf("invalid file path: %w", asynq.SkipRetry)
	}

	var profile db.Profile
	if err := w.DB.Where("id = ?", p.ProfileID).First(&profile).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			w.logger.Error("No profile found", zap.Uint("profile_id", p.ProfileID))
			return fmt.Errorf("profile not found: %w", asynq.SkipRetry)
		}
		w.logger.Error("Failed to load profile", zap.Uint("profile_id", p.ProfileID))
		return fmt.Errorf("failed to load profile: %w", err)
	}

	var session db.Session
	if err := w.DB.Where("session_id = ?", p.SessionID).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			w.logger.Error("No session found", zap.String("session_id", p.SessionID))
			return fmt.Errorf("session not found: %w", asynq.SkipRetry)
		}
		w.logger.Error("Failed to load session", zap.String("session_id", p.SessionID))
		return fmt.Errorf("failed to load session: %w", err)
	}

	localFilePath, err := createTempFile("chunk", ".mp4")
	if err != nil {
		return err
	}

	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download chunk file from GCS: %w", err)
	}

	// Start motion probe concurrently with the Gemini API call
	type probeResult struct {
		score float64
		err   error
	}
	probeChan := make(chan probeResult, 1)
	go func() {
		score, pErr := probeMotionScore(ctx, localFilePath)
		probeChan <- probeResult{score: score, err: pErr}
	}()

	prompt := w.buildChunkAnalysisWithSessionPrompt(p, &profile, &session) + w.livePrompt(p.LiveAnalysisVersion)
	trackingPrompt, trackingImages := w.trackingInputs(ctx, p.ProfileID, p.SessionID, session.WorkoutType, p.FilePath, p.StartSecs)
	prompt += trackingPrompt

	analysis, geminiFile, usage, err := w.analyzeChunkVideo(ctx, localFilePath, prompt, gemini.ModelFlash38, trackingImages)

	// Wait for motion probe to finish
	var motionScoreWithSession *float64
	pRes := <-probeChan
	if pRes.err == nil {
		motionScoreWithSession = &pRes.score
	} else {
		w.logger.Warn("motion probe failed for chunk analysis with session", zap.Error(pRes.err))
	}

	defer func() {
		os.Remove(localFilePath)
	}()

	if geminiFile != "" {
		defer func() {
			w.GeminiClient.DeleteFile(ctx, geminiFile)
		}()
	}

	// Save token usage regardless of analysis outcome
	w.saveTokenUsage(p.SessionID, p.ProfileID, "chunk:analysis", usage)

	if err != nil {
		w.logger.Error("Chunk analysis failed", zap.Error(err), zap.String("session_id", p.SessionID), zap.String("file_path", p.FilePath), zap.Int("retry_count", int(retryCount)))
		return err
	}

	if analysis == "" {
		w.logger.Warn("Chunk analysis returned empty output", zap.String("session_id", p.SessionID), zap.String("file_path", p.FilePath), zap.Int("retry_count", int(retryCount)))
		return fmt.Errorf("chunk analysis is empty")
	}

	liveRaw := analysis
	analysis = liveBlocks.ReplaceAllString(analysis, "")
	// Extract exercise type detected by the model from the response
	detectedExercise := parseChunkExercise(analysis)
	// Extract observed signals JSON for benchmarking
	observedSignals := parseObservedSignals(analysis)
	targetCues, targetConfidence := parseTargetPerson(analysis)
	// Strip tags, signals block, and target person block from the output, leaving just coaching feedback
	cleanOutput := stripExerciseTag(analysis)
	cleanOutput = stripObservedSignals(cleanOutput)
	cleanOutput = stripTargetPerson(cleanOutput)

	chunkResult := &db.ChunkAnalysisResult{
		SessionID:         p.SessionID,
		FilePath:          p.FilePath,
		ExerciseType:      detectedExercise,
		Status:            "COMPLETED",
		Output:            cleanOutput,
		ObservedSignals:   observedSignals,
		HeartRateBPM:      p.HeartRateBPM,
		WorkoutConfidence: p.WorkoutConfidence,
		TargetConfidence:  targetConfidence,
		TargetCues:        targetCues,
		MotionScore:       motionScoreWithSession,
		SkipReason:        "",
	}
	chunkResult.ProfileID = p.ProfileID
	if p.StartSecs > 0 || p.EndSecs > 0 {
		chunkResult.StartSecs = &p.StartSecs
		chunkResult.EndSecs = &p.EndSecs
	}
	duration := 0.0
	if p.LiveAnalysisVersion == 1 && w.ActivityCountingEnabled {
		duration = probeVideoDuration(ctx, localFilePath)
	}
	w.parseLiveFeedback(liveRaw, p.LiveAnalysisVersion, duration, chunkResult)
	if err := w.persistChunkAnalysisResult(ctx, chunkResult); err != nil {
		return fmt.Errorf("failed to save chunk analysis result: %w", err)
	}
	// Tracking channel for the next chunk; never fails this chunk.
	w.saveTrackingReference(ctx, localFilePath, chunkResult)

	if err := w.refreshLiveActivity(ctx, VideoAnalysisPayload{SessionID: p.SessionID, ProfileID: p.ProfileID, WorkoutType: session.WorkoutType, WODDescription: session.WODDescription, Movements: movementHintsFromDocument(session.MovementHints)}, chunkResult); err != nil {
		return err
	}
	// Bounded recent-window coaching is a separate post-observation result.
	w.addContextualCoaching(ctx, p.LiveAnalysisVersion, chunkResult)

	w.logger.Info("Chunk analysis completed",
		zap.String("session_id", p.SessionID),
		zap.String("detected_exercise", detectedExercise),
		zap.String("observed_signals", observedSignals))
	return nil
}

func (w *Worker) buildChunkAnalysisPrompt(p VideoAnalysisPayload) string {
	prompt := baseChunkAnalysisPrompt()

	if IsRecoveryWorkoutType(p.WorkoutType) {
		prompt += RecoveryChunkPolicyPrompt
	} else {
		fitnessLevel := w.lookupFitnessLevel(p.ProfileID)
		prompt += levelPolicyForFitnessLevel(fitnessLevel)
	}

	// Inject WOD descriptor as planning context, never as visual proof.
	if wod := buildWODContext(p.WODDescription); wod != "" {
		prompt += wod
	}

	prompt += buildMovementHintsContext(p.Movements)

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("\n\n## 알려진 부상 사항\n%s\n(이 부위에 위험한 자세가 보이면 반드시 경고하세요)", strings.Join(p.Injuries, ", "))
	}

	personalProfile := w.lookupProfileString(p.ProfileID)
	prompt += fmt.Sprintf("\n\n## 개인 프로필\n%s", personalProfile)
	prompt += w.buildTargetPersonContext(p.ProfileID, p.SessionID)

	prompt += buildHeartRateContext(p.HeartRateBPM)

	// Add observed signals & target person requirement (tracking continuity supplies its own unified target_person block).
	prompt += ObservedSignalsPrompt
	if !w.TrackingContinuityEnabled {
		prompt += TargetPersonPrompt
	}
	prompt += independentSegmentPolicy
	prompt += ObservationOnlyOutputCheckPrompt

	return prompt
}

func (w *Worker) buildChunkAnalysisWithSessionPrompt(p VideoAnalysisWithSessionPayload, profile *db.Profile, session *db.Session) string {
	prompt := baseChunkAnalysisPrompt()

	if session != nil && IsRecoveryWorkoutType(session.WorkoutType) {
		prompt += RecoveryChunkPolicyPrompt
	} else {
		fitnessLevel := w.lookupFitnessLevel(p.ProfileID)
		prompt += levelPolicyForFitnessLevel(fitnessLevel)
	}

	if wod := buildWODContext(session.WODDescription); wod != "" {
		prompt += wod
	}

	prompt += buildMovementHintsContext(movementHintsFromDocument(session.MovementHints))

	if profile.Injuries != nil && *profile.Injuries != "" {
		prompt += fmt.Sprintf("\n\n## 알려진 부상 사항\n%s\n(이 부위에 위험한 자세가 보이면 반드시 경고하세요)", *profile.Injuries)
	}

	personalProfile := w.lookupProfileString(p.ProfileID)
	prompt += fmt.Sprintf("\n\n## 개인 프로필\n%s", personalProfile)
	prompt += w.buildTargetPersonContext(p.ProfileID, p.SessionID)

	prompt += buildHeartRateContext(p.HeartRateBPM)

	// Add observed signals & target person requirement (tracking continuity supplies its own unified target_person block).
	prompt += ObservedSignalsPrompt
	if !w.TrackingContinuityEnabled {
		prompt += TargetPersonPrompt
	}
	prompt += independentSegmentPolicy
	prompt += ObservationOnlyOutputCheckPrompt

	return prompt
}
