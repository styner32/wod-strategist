package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"go.uber.org/zap"
)

const ChunkAnalysisPrompt = `
# 운동 청크 영상 분석 요청 (실시간 피드백)

당신은 실시간 코칭을 제공하는 전문 CrossFit 코치입니다.
방금 수행된 약 10초 분량의 짧은 영상 클립이 주어집니다.

## 중요 원칙
- 이 사용자에 대한 과거 데이터가 없습니다. 평균, 벤치마크, 또래 비교를 하지 마세요.
- 현재 영상에서 관찰되는 것만 기반으로 판단하세요.
- 안전이 최우선: 부상 위험이 보이면 레벨과 관계없이 즉시 경고하세요.
- 한 번에 하나의 큐만 제시하세요.
- 모든 피드백은 **존댓말** (~요, ~세요, ~습니다)로 작성하세요. 반말(~해, ~다, ~함)을 절대 사용하지 마세요.

## 필수: 운동 종목 식별
먼저, 영상에서 수행 중인 운동 종목을 식별하세요.
- 운동이 보이면 → 첫 줄에 반드시 [EXERCISE: 영어 운동 이름] 태그를 출력하세요.
  (예: [EXERCISE: Snatch], [EXERCISE: Back Squat], [EXERCISE: Pull-up], [EXERCISE: Burpee])
- 운동이 보이지 않으면 (휴식, 걷기, 장비 세팅, 촬영 범위 밖 등) → 첫 줄에 [NO_EXERCISE] 태그만 출력하세요.

## 코칭 피드백 규칙
- 운동이 감지된 경우: [EXERCISE: ...] 태그 다음 줄에 **반드시 1~2문장**으로만 코칭 피드백을 답하세요.
- 운동이 감지되지 않은 경우: [NO_EXERCISE] 태그만 출력하고 추가 피드백을 작성하지 마세요.
- 아래 컨텍스트를 참고하되, 컨텍스트 자체를 반복하지 마세요.
- 부상 사항이 있으면 → 해당 부위에 위험한 움직임이 보일 때만 안전 경고를 우선하세요.`

// Level policy prompts appended based on user's fitness level.
const (
	LevelPolicyBeginner = `

## 레벨 정책: 초급 (Beginner)
- 자세와 좌우 균형에만 집중하세요. 페이스는 무시하세요.
- 자세 문제가 보이면 → 가장 중요한 한 가지만 교정 큐를 제시하세요.
- 자세가 좋으면 → 어떤 점이 좋은지 짧게 격려하고, 이 동작의 약한 근육군을 위한 보조 운동을 제안하세요.
- 절대로 초급자에게 속도를 올리거나 무게를 추가하라고 하지 마세요.
- 어조: 따뜻하고, 인내심 있고, 격려하는. "~요" 존댓말 사용.`

	LevelPolicyIntermediate = `

## 레벨 정책: 중급 (Intermediate)
- 자세는 의미 있는 문제가 있을 때만 언급하세요.
- 페이스와 강도를 관찰하세요:
  · 동작이 느려지는데 힘들어 보이지 않으면 → 페이스 유지를 독려하세요.
  · 전반적으로 여유로워 보이면 → 더 빠른 케이던스 또는 다음 세션 무게 증가를 권하세요.
  · 속도가 일정하고 힘들어 보이면 → 잘하고 있다고 격려하세요.
- 어조: 직접적이고 간결한 존댓말 명령형. "~하세요" 스타일. 반말 금지.`

	LevelPolicyAdvanced = `

## 레벨 정책: 고급 (Advanced)
- 자세가 깨끗하고 페이스가 일정하면 → "좋아요, 유지" 정도의 최소 피드백만. 불필요한 코칭 자제.
- 피로/반응 신호가 보이면 그때부터 적극적으로 코칭:
  · 동작이 느려지거나 케이던스가 떨어지면 → 끝까지 밀고 가라고 하세요.
  · 여유로워 보이는데 속도가 줄면 → 더 빠른 페이스를 요구하세요.
  · 자세가 무너지면 → 피로 신호로 인정하고, 밀고 갈지 멈출지 판단하세요.
- 현재 WOD 중에는 무게 증가를 지시하지 마세요. "다음 WOD에서 무게를 올려보세요"처럼 다음 세션 권고만 가능.
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
  "set_duration_s": 10,
  "rep_count": 0,
  "avg_cadence_s": 0.0,
  "exertion_estimate": "low|moderate|high|failing",
  "form_issues_seen": ["이슈1", "이슈2"]
}` + "\n```" + `

- movement: 감지된 운동 종목 (영어)
- set_duration_s: 이 클립에서 운동이 수행된 대략적인 시간 (초)
- rep_count: 이 클립에서 관찰된 대략적인 반복 횟수 (불분명하면 0)
- avg_cadence_s: 반복당 대략적인 소요 시간 (초, 불분명하면 0)
- exertion_estimate: 관찰된 운동 강도 (low/moderate/high/failing)
- form_issues_seen: 관찰된 자세 문제 목록 (없으면 빈 배열)
- 운동이 감지되지 않은 경우에는 이 블록을 출력하지 마세요.`
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
		// Validate it's parseable JSON
		var check map[string]any
		if json.Unmarshal([]byte(raw), &check) == nil {
			return raw
		}
	}
	return "{}"
}

// stripObservedSignals removes the ```observed_signals ... ``` block from the output,
// leaving just the coaching feedback.
func stripObservedSignals(analysis string) string {
	return strings.TrimSpace(observedSignalsBlockRegex.ReplaceAllString(analysis, ""))
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

func NewChunkAnalysisTask(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint, startSecs, endSecs float64, heartRateBPM int, wodDescription string) (*asynq.Task, error) {
	payload := VideoAnalysisPayload{
		SessionID:      sessionID,
		FilePath:       filePath,
		WorkoutType:    NormalizeWorkoutType(workoutType),
		Movements:      movements,
		Injuries:       injuries,
		ProfileID:      profileID,
		StartSecs:      startSecs,
		EndSecs:        endSecs,
		HeartRateBPM:   heartRateBPM,
		WODDescription: wodDescription,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeChunkAnalysis, data), nil
}

func (w *Worker) HandleChunkAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	retryCount, ok := asynq.GetRetryCount(ctx)
	if !ok {
		retryCount = 0
	}

	w.logger.Info("Processing chunk analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		w.logger.Error("Max retries reached. Skipping chunk analysis.")
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

	prompt := w.buildChunkAnalysisPrompt(p)

	analysis, geminiFile, usage, err := w.GeminiClient.AnalyzeVideoWithModel(ctx, localFilePath, prompt, gemini.ModelFlash35)

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

	if analysis == "" {
		return fmt.Errorf("chunk analysis is empty")
	}

	if err != nil {
		w.logger.Error("Chunk analysis failed", zap.Error(err))
		chunkFailed := &db.ChunkAnalysisResult{
			SessionID: p.SessionID,
			FilePath:  p.FilePath,
			Status:    "FAILED",
			Output:    "An internal error occurred during chunk analysis.",
		}
		chunkFailed.ProfileID = p.ProfileID
		if p.StartSecs > 0 || p.EndSecs > 0 {
			chunkFailed.StartSecs = &p.StartSecs
			chunkFailed.EndSecs = &p.EndSecs
		}
		w.DB.Create(chunkFailed)
		return err
	}

	// Extract exercise type detected by the model from the response
	detectedExercise := parseChunkExercise(analysis)
	// Extract observed signals JSON for benchmarking
	observedSignals := parseObservedSignals(analysis)
	// Strip tags and signals block from the output, leaving just coaching feedback
	cleanOutput := stripExerciseTag(analysis)
	cleanOutput = stripObservedSignals(cleanOutput)

	chunkResult := &db.ChunkAnalysisResult{
		SessionID:       p.SessionID,
		FilePath:        p.FilePath,
		ExerciseType:    detectedExercise,
		Status:          "COMPLETED",
		Output:          cleanOutput,
		ObservedSignals: observedSignals,
		HeartRateBPM:    p.HeartRateBPM,
	}
	chunkResult.ProfileID = p.ProfileID
	if p.StartSecs > 0 || p.EndSecs > 0 {
		chunkResult.StartSecs = &p.StartSecs
		chunkResult.EndSecs = &p.EndSecs
	}
	w.DB.Create(chunkResult)

	w.logger.Info("Chunk analysis completed",
		zap.String("session_id", p.SessionID),
		zap.String("detected_exercise", detectedExercise),
		zap.String("observed_signals", observedSignals))
	return nil
}

func (w *Worker) buildChunkAnalysisPrompt(p VideoAnalysisPayload) string {
	prompt := ChunkAnalysisPrompt

	// Inject fitness-level-specific coaching policy
	fitnessLevel := w.lookupFitnessLevel(p.ProfileID)
	prompt += levelPolicyForFitnessLevel(fitnessLevel)

	// Inject WOD descriptor context so chunk feedback is aware of the WOD type
	// (e.g. "For Time: 5 rounds" → expect fatigue in later rounds).
	if wod := buildWODContext(p.WODDescription); wod != "" {
		prompt += wod
	}

	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf("\n\n## 확인된 운동 종목 (사용자 입력)\n아래 운동들은 이 세션에서 **확실히 수행되는 운동**입니다. AI 감지가 불확실할 경우 이 목록을 우선 참고하세요.\n%s", strings.Join(p.Movements, ", "))
	}

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("\n\n## 알려진 부상 사항\n%s\n(이 부위에 위험한 자세가 보이면 반드시 경고하세요)", strings.Join(p.Injuries, ", "))
	}

	personalProfile := w.lookupProfileString(p.ProfileID)
	prompt += fmt.Sprintf("\n\n## 개인 프로필\n%s", personalProfile)

	// Add real-time heart rate context if available
	if p.HeartRateBPM > 0 {
		prompt += fmt.Sprintf("\n\n## 실시간 심박수 데이터\n현재 심박수: %d bpm\n(심박수가 높으면 피로도와 연관지어 코칭하세요. 단, 심박수 자체를 피드백 문장에 포함하지 마세요.)", p.HeartRateBPM)
	}

	// Add observed signals requirement
	prompt += ObservedSignalsPrompt

	return prompt
}
