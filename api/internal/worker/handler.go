package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	TypeVideoAnalysis     = "video:analysis"
	WorkoutTypeWOD        = "wod"
	WorkoutTypeRehab      = "rehab"
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

1. **동작 분석 (Movement Analysis)**:
   - 전반적인 자세의 정확도와 가동 범위를 평가해주세요.
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

	RehabilitationPrompt = `
# 운동 영상 분석 요청 (재활 및 안전 복귀)

## 분석 요청 사항
당신은 전문 스포츠 재활 치료사(Physical Therapist)이자 교정 운동 전문가입니다. 위 컨텍스트와 첨부된 영상을 바탕으로, 퍼포먼스 향상보다는 **부상 악화 방지, 통제력 확보, 그리고 안전한 기능 회복**에 초점을 맞추어 다음 항목을 분석해 주세요.
(※ 참고: 사용자는 현재 부상 회복 중이거나 좌식(Seated) 등 제한된 환경에서 운동 중일 수 있습니다. 해당 부위에 무리가 가지 않는지 철저히 관찰하세요.)

1. **안정성 및 통제력 분석 (Stability & Control Analysis)**:
   - 동작이 관절에 무리가 없는 안전한 가동 범위(Pain-free ROM) 내에서 부드럽게 수행되고 있는지 평가해 주세요.
   - 제한된 자세(예: 의자에 앉아 하체 고정)에서도 척추 중립(Neutral Spine)과 코어의 안정성이 흔들림 없이 유지되는지 분석해 주세요.

2. **보상 작용 및 불균형 (Compensations & Imbalances)**:
   - 통증을 피하거나 근력 저하를 메우기 위해 다른 관절을 무리하게 사용하는 **'보상 작용'**(예: 어깨 으쓱임, 허리 과신전, 목 빠짐, 상체 반동 등)이 관찰되는지 꼼꼼히 확인해 주세요.
   - 신체 좌우의 움직임 궤적, 가동 범위, 밸런스에 비대칭이 발생하는지 평가해 주세요.

3. **템포 및 긴장도 분석 (Tempo & Tension Analysis)**:
   - 동작의 속도, 특히 중량을 버티며 내리는 구간(신장성 수축)에서 템포가 차분하고 일정하게 통제되고 있는지 평가해 주세요.
   - 근육의 피로나 관절의 불안정성으로 인해 텐션이 풀리거나 반동을 쓰기 시작하여 부상 위험이 높아지는 **정확한 시점(분:초)**을 지목해 주세요.

4. **안전 복귀 솔루션 (Safe Return-to-Play Feedback)**:
   - 부상 부위에 스트레스를 주지 않으면서 현재 동작의 질을 높일 수 있는 즉각적인 자세/호흡 교정 팁 3가지를 제안해 주세요.
   - 무리한 동작이 관찰되었다면 가동 범위를 제한하거나 난이도를 낮춘 대체 운동(Regression)을 조언하고, 향후 본 훈련(Active WOD)으로 돌아가기 위한 점진적 과부하 가이드를 포함해 주세요.

5. **핵심 모니터링 구간 타임스탬프 (Key Timestamps)**:
   - 훌륭한 통제력으로 자세가 가장 안정적인 '모범 구간'과, 보상 작용이나 불안정성이 노출되어 '주의가 필요한 구간'(시작 시간 - 종료 시간)을 나열해 주세요.
   - 각 구간을 왜 주의 깊게 모니터링해야 하는지 부상 방지 관점에서 한 문장으로 요약해 주세요.
	`
)

type VideoAnalysisPayload struct {
	SessionID   string
	FilePath    string
	WorkoutType string
	Movements   []string
	Injuries    []string
}

type Worker struct {
	DB            *gorm.DB
	StorageClient *storage.Client
	BucketName    string
	GeminiClient  *gemini.Client
}

func NewWorker(db *gorm.DB, storageClient *storage.Client, bucketName string, geminiClient *gemini.Client) *Worker {
	return &Worker{
		DB:            db,
		StorageClient: storageClient,
		BucketName:    bucketName,
		GeminiClient:  geminiClient,
	}
}

func NormalizeWorkoutType(workoutType string) string {
	if strings.EqualFold(workoutType, WorkoutTypeRehab) {
		return WorkoutTypeRehab
	}
	return WorkoutTypeWOD
}

func IsValidWorkoutType(workoutType string) bool {
	return workoutType == "" ||
		strings.EqualFold(workoutType, WorkoutTypeWOD) ||
		strings.EqualFold(workoutType, WorkoutTypeRehab)
}

func NewVideoAnalysisTask(sessionID, filePath, workoutType string, movements []string, injuries []string) (*asynq.Task, error) {
	payload := VideoAnalysisPayload{
		SessionID:   sessionID,
		FilePath:    filePath,
		WorkoutType: NormalizeWorkoutType(workoutType),
		Movements:   movements,
		Injuries:    injuries,
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

	logger.Log.Info("Processing video analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.String("workout_type", NormalizeWorkoutType(p.WorkoutType)),
		zap.Strings("movements", p.Movements),
		zap.Strings("injuries", p.Injuries),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		logger.Log.Error("Max retries reached. Skipping analysis.")
		return asynq.SkipRetry
	}

	// Determine file path (download from GCS if needed)
	localFilePath := p.FilePath
	if strings.HasPrefix(p.FilePath, "gs://") {
		// Create temp file path
		// Sanitize SessionID and FilePath to prevent path traversal
		tmpFile := filepath.Join("/tmp", fmt.Sprintf("%s_%s", filepath.Base(p.SessionID), filepath.Base(p.FilePath)))

		logger.Log.Info("Downloading file from GCS", zap.String("uri", p.FilePath), zap.String("dest", tmpFile))
		if err := w.StorageClient.DownloadFile(ctx, p.FilePath, tmpFile); err != nil {
			return fmt.Errorf("failed to download file from GCS: %w", err)
		}
		localFilePath = tmpFile
	}

	// Update status to PROCESSING (optional, if we tracked specific task IDs, but here we just append results)
	// For simplicity, we just create a new result when done.
	prompt := buildAnalysisPrompt(p)

	analysis, geminiFile, err := w.GeminiClient.AnalyzeVideo(ctx, localFilePath, prompt)

	// retry if analysis is empty
	if analysis == "" {
		logger.Log.Warn("Analysis failed. Retrying...", zap.Error(err))
		return fmt.Errorf("analysis is empty")
	}

	// Clean up local file
	defer func() {
		if err := os.Remove(localFilePath); err != nil {
			logger.Log.Error("Failed to remove temp file", zap.Error(err))
		}
	}()

	// Clean up Gemini file if it was uploaded
	if geminiFile != "" {
		defer func() {
			if err := w.GeminiClient.DeleteFile(ctx, geminiFile); err != nil {
				logger.Log.Error("Failed to delete file from Gemini", zap.Error(err))
			}
		}()
	}

	if err != nil {
		if os.IsNotExist(err) {
			logger.Log.Error("File not found. Skipping analysis.", zap.Error(err))
			return asynq.SkipRetry
		}

		logger.Log.Error("Analysis failed", zap.Error(err))
		// Save failure to DB
		w.DB.Create(&db.AnalysisResult{
			SessionID: p.SessionID,
			Status:    "FAILED",
			Output:    err.Error(),
		})
		return err
	}

	// Save success to DB
	w.DB.Create(&db.AnalysisResult{
		SessionID: p.SessionID,
		Status:    "COMPLETED",
		Output:    analysis,
	})

	logger.Log.Info("Analysis completed", zap.String("session_id", p.SessionID), zap.String("analysis", analysis))
	return nil
}

func buildAnalysisPrompt(p VideoAnalysisPayload) string {
	prompt := AnalysisPrompt
	if NormalizeWorkoutType(p.WorkoutType) == WorkoutTypeRehab {
		prompt = RehabilitationPrompt
	}

	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf("%s\n## 운동 종목: %s", MovementPrompt, strings.Join(p.Movements, ", "))
	}

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n## 알려진 부상 사항: %s", KnownInjuriesPrompt, strings.Join(p.Injuries, ", "))
	}

	personalProfile := "생년월일: 1984년 10월 17일, 성별: 남, 키: 164cm, 몸무게: 72kg" // customize later when auth is ready
	return prompt + fmt.Sprintf("%s\n## 개인 프로필: %s", PersonalProfilePrompt, personalProfile)
}
