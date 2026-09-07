package cost

import (
	"math"
	"strings"

	"github.com/wod-strategist/api/internal/db"
)

const KRWPerUSD = 1380.0

type ModelRate struct {
	PromptPerMillion    float64 `json:"prompt_per_million"`
	CandidatePerMillion float64 `json:"candidate_per_million"`
}

var ModelPricing = map[string]ModelRate{
	"gemini-3.8-flash": {
		PromptPerMillion:    1.50,
		CandidatePerMillion: 9.00,
	},
	"gemini-3.5-flash-lite": {
		PromptPerMillion:    0.25,
		CandidatePerMillion: 1.50,
	},
	"gemini-3.1-pro-preview": {
		PromptPerMillion:    1.25,
		CandidatePerMillion: 5.00,
	},
}

var DefaultRate = ModelRate{
	PromptPerMillion:    1.50,
	CandidatePerMillion: 9.00,
}

func GetModelRate(model string) ModelRate {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if rate, exists := ModelPricing[normalized]; exists {
		return rate
	}
	// Fallbacks for known prefixes/suffixes
	switch {
	case strings.Contains(normalized, "3.5-flash-lite"):
		return ModelPricing["gemini-3.5-flash-lite"]
	case strings.Contains(normalized, "flash"):
		return ModelPricing["gemini-3.8-flash"]
	case strings.Contains(normalized, "pro"):
		return ModelPricing["gemini-3.1-pro-preview"]
	default:
		return DefaultRate
	}
}

func RoundUSD(usd float64) float64 {
	return math.Round(usd*1000000) / 1000000
}

func RoundKRW(krw float64) float64 {
	return math.Round(krw*100) / 100
}

func CalculateTokensCost(model string, promptTokens, candidateTokens int64) (float64, float64) {
	rate := GetModelRate(model)
	promptUSD := (float64(promptTokens) / 1000000.0) * rate.PromptPerMillion
	candidateUSD := (float64(candidateTokens) / 1000000.0) * rate.CandidatePerMillion
	totalUSD := RoundUSD(promptUSD + candidateUSD)
	totalKRW := RoundKRW(totalUSD * KRWPerUSD)
	return totalUSD, totalKRW
}

type BreakdownItem struct {
	Key             string  `json:"key"`
	PromptTokens    int64   `json:"prompt_tokens"`
	CandidateTokens int64   `json:"candidate_tokens"`
	TotalTokens     int64   `json:"total_tokens"`
	CostUSD         float64 `json:"cost_usd"`
	CostKRW         float64 `json:"cost_krw"`
}

type SessionCostResponse struct {
	SessionID       string          `json:"session_id"`
	PromptTokens    int64           `json:"prompt_tokens"`
	CandidateTokens int64           `json:"candidate_tokens"`
	TotalTokens     int64           `json:"total_tokens"`
	CostUSD         float64         `json:"cost_usd"`
	CostKRW         float64         `json:"cost_krw"`
	ByTaskType      []BreakdownItem `json:"by_task_type"`
	ByModel         []BreakdownItem `json:"by_model"`
}

type TotalCostResponse struct {
	PromptTokens    int64   `json:"prompt_tokens"`
	CandidateTokens int64   `json:"candidate_tokens"`
	TotalTokens     int64   `json:"total_tokens"`
	CostUSD         float64 `json:"cost_usd"`
	CostKRW         float64 `json:"cost_krw"`
}

type ModelTokenAggregate struct {
	Model           string `json:"model"`
	PromptTokens    int64  `json:"prompt_tokens"`
	CandidateTokens int64  `json:"candidate_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
}

func CalculateSessionCost(sessionID string, usages []db.TokenUsage) SessionCostResponse {
	var totalPrompt int64
	var totalCandidate int64
	var totalTokens int64
	var totalUSD float64

	type keyStats struct {
		promptTokens    int64
		candidateTokens int64
		totalTokens     int64
		costUSD         float64
	}

	byTask := make(map[string]*keyStats)
	byModel := make(map[string]*keyStats)

	// Preserve ordering
	var taskKeys []string
	var modelKeys []string

	for _, u := range usages {
		p := int64(u.PromptTokens)
		c := int64(u.CandidateTokens)
		tot := int64(u.TotalTokens)
		usd, _ := CalculateTokensCost(u.Model, p, c)

		totalPrompt += p
		totalCandidate += c
		totalTokens += tot
		totalUSD += usd

		// Task breakdown
		if _, exists := byTask[u.TaskType]; !exists {
			byTask[u.TaskType] = &keyStats{}
			taskKeys = append(taskKeys, u.TaskType)
		}
		ts := byTask[u.TaskType]
		ts.promptTokens += p
		ts.candidateTokens += c
		ts.totalTokens += tot
		ts.costUSD += usd

		// Model breakdown
		normModel := strings.ToLower(strings.TrimSpace(u.Model))
		if normModel == "" {
			normModel = "unknown"
		}
		if _, exists := byModel[normModel]; !exists {
			byModel[normModel] = &keyStats{}
			modelKeys = append(modelKeys, normModel)
		}
		ms := byModel[normModel]
		ms.promptTokens += p
		ms.candidateTokens += c
		ms.totalTokens += tot
		ms.costUSD += usd
	}

	taskItems := make([]BreakdownItem, 0, len(taskKeys))
	for _, k := range taskKeys {
		s := byTask[k]
		costUSD := RoundUSD(s.costUSD)
		costKRW := RoundKRW(costUSD * KRWPerUSD)
		taskItems = append(taskItems, BreakdownItem{
			Key:             k,
			PromptTokens:    s.promptTokens,
			CandidateTokens: s.candidateTokens,
			TotalTokens:     s.totalTokens,
			CostUSD:         costUSD,
			CostKRW:         costKRW,
		})
	}

	modelItems := make([]BreakdownItem, 0, len(modelKeys))
	for _, k := range modelKeys {
		s := byModel[k]
		costUSD := RoundUSD(s.costUSD)
		costKRW := RoundKRW(costUSD * KRWPerUSD)
		modelItems = append(modelItems, BreakdownItem{
			Key:             k,
			PromptTokens:    s.promptTokens,
			CandidateTokens: s.candidateTokens,
			TotalTokens:     s.totalTokens,
			CostUSD:         costUSD,
			CostKRW:         costKRW,
		})
	}

	costUSD := RoundUSD(totalUSD)
	costKRW := RoundKRW(costUSD * KRWPerUSD)

	return SessionCostResponse{
		SessionID:       sessionID,
		PromptTokens:    totalPrompt,
		CandidateTokens: totalCandidate,
		TotalTokens:     totalTokens,
		CostUSD:         costUSD,
		CostKRW:         costKRW,
		ByTaskType:      taskItems,
		ByModel:         modelItems,
	}
}

func CalculateTotalCost(usages []db.TokenUsage) TotalCostResponse {
	var totalPrompt int64
	var totalCandidate int64
	var totalTokens int64
	var totalUSD float64

	for _, u := range usages {
		p := int64(u.PromptTokens)
		c := int64(u.CandidateTokens)
		tot := int64(u.TotalTokens)
		usd, _ := CalculateTokensCost(u.Model, p, c)

		totalPrompt += p
		totalCandidate += c
		totalTokens += tot
		totalUSD += usd
	}

	costUSD := RoundUSD(totalUSD)
	costKRW := RoundKRW(costUSD * KRWPerUSD)

	return TotalCostResponse{
		PromptTokens:    totalPrompt,
		CandidateTokens: totalCandidate,
		TotalTokens:     totalTokens,
		CostUSD:         costUSD,
		CostKRW:         costKRW,
	}
}

func CalculateTotalCostFromAggregates(aggregates []ModelTokenAggregate) TotalCostResponse {
	var totalPrompt int64
	var totalCandidate int64
	var totalTokens int64
	var totalUSD float64

	for _, a := range aggregates {
		usd, _ := CalculateTokensCost(a.Model, a.PromptTokens, a.CandidateTokens)
		totalPrompt += a.PromptTokens
		totalCandidate += a.CandidateTokens
		totalTokens += a.TotalTokens
		totalUSD += usd
	}

	costUSD := RoundUSD(totalUSD)
	costKRW := RoundKRW(costUSD * KRWPerUSD)

	return TotalCostResponse{
		PromptTokens:    totalPrompt,
		CandidateTokens: totalCandidate,
		TotalTokens:     totalTokens,
		CostUSD:         costUSD,
		CostKRW:         costKRW,
	}
}
