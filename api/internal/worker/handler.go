package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

const (
	TypeVideoAnalysis = "video:analysis"
	MovementPrompt    = `
## 운동 컨텍스트 (선택 입력)
분석의 정확도를 높이기 위해 아래 정보를 참고해주세요. 정보가 없다면 영상 자체만으로 분석해 주세요.
- **운동 종목/와드명**: {{ 예: 스쿼트, 프란(Fran), 1RM 측정, 30분 달리기 }}
- **운동 목표**: {{ 예: 자세 교정, 기록 단축, 완주, 근비대 }}
- **사용된 무게/강도**: {{ 예: 135lb, 맨몸, 70% 강도 }}
- **특이사항**: {{ 예: 최근 허리 부상 있음, 3라운드부터 급격히 지침 }}`

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
)

type VideoAnalysisPayload struct {
	SessionID string
	FilePath  string
}

func NewVideoAnalysisTask(sessionID, filePath string) (*asynq.Task, error) {
	payload := VideoAnalysisPayload{
		SessionID: sessionID,
		FilePath:  filePath,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeVideoAnalysis, data), nil
}

func HandleVideoAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	logger.Log.Info("Processing video analysis", zap.String("session_id", p.SessionID))

	// Update status to PROCESSING (optional, if we tracked specific task IDs, but here we just append results)
	// For simplicity, we just create a new result when done.

	geminiClient, err := gemini.NewClient(ctx, logger.Log)
	if err != nil {
		return fmt.Errorf("failed to create gemini client: %w", err)
	}
	defer geminiClient.Close()

	analysis, geminiFile, err := geminiClient.AnalyzeVideo(ctx, p.FilePath, AnalysisPrompt)

	// Clean up local file
	defer func() {
		if err := os.Remove(p.FilePath); err != nil {
			logger.Log.Error("Failed to remove temp file", zap.Error(err))
		}
	}()

	// Clean up Gemini file if it was uploaded
	if geminiFile != "" {
		defer func() {
			if err := geminiClient.DeleteFile(ctx, geminiFile); err != nil {
				logger.Log.Error("Failed to delete file from Gemini", zap.Error(err))
			}
		}()
	}

	if err != nil {
		logger.Log.Error("Analysis failed", zap.Error(err))
		// Save failure to DB
		db.DB.Create(&db.AnalysisResult{
			SessionID: p.SessionID,
			Status:    "FAILED",
			Output:    err.Error(),
		})
		return err
	}

	// Save success to DB
	db.DB.Create(&db.AnalysisResult{
		SessionID: p.SessionID,
		Status:    "COMPLETED",
		Output:    analysis,
	})

	logger.Log.Info("Analysis completed", zap.String("session_id", p.SessionID), zap.String("analysis", analysis))
	return nil
}
