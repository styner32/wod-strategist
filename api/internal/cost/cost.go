package cost

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
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
	// Lyria is priced per generation, not using the fallback text-token rate.
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "lyria-") {
		return 0, 0
	}
	rate := GetModelRate(model)
	promptUSD := (float64(promptTokens) / 1000000.0) * rate.PromptPerMillion
	candidateUSD := (float64(candidateTokens) / 1000000.0) * rate.CandidatePerMillion
	totalUSD := RoundUSD(promptUSD + candidateUSD)
	totalKRW := RoundKRW(totalUSD * KRWPerUSD)
	return totalUSD, totalKRW
}

type BreakdownItem struct {
	UnpricedCalls   int64   `json:"unpriced_calls"`
	ThinkingTokens  int64   `json:"thinking_tokens"`
	ToolUseTokens   int64   `json:"tool_use_tokens"`
	CachedTokens    int64   `json:"cached_tokens"`
	UnmeasuredCalls int64   `json:"unmeasured_calls"`
	Key             string  `json:"key"`
	PromptTokens    int64   `json:"prompt_tokens"`
	CandidateTokens int64   `json:"candidate_tokens"`
	TotalTokens     int64   `json:"total_tokens"`
	CostUSD         float64 `json:"cost_usd"`
	CostKRW         float64 `json:"cost_krw"`
}

type SessionCostResponse struct {
	UnpricedCalls   int64           `json:"unpriced_calls"`
	ThinkingTokens  int64           `json:"thinking_tokens"`
	ToolUseTokens   int64           `json:"tool_use_tokens"`
	CachedTokens    int64           `json:"cached_tokens"`
	UnmeasuredCalls int64           `json:"unmeasured_calls"`
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
	UnpricedCalls   int64   `json:"unpriced_calls"`
	ThinkingTokens  int64   `json:"thinking_tokens"`
	ToolUseTokens   int64   `json:"tool_use_tokens"`
	CachedTokens    int64   `json:"cached_tokens"`
	UnmeasuredCalls int64   `json:"unmeasured_calls"`
	PromptTokens    int64   `json:"prompt_tokens"`
	CandidateTokens int64   `json:"candidate_tokens"`
	TotalTokens     int64   `json:"total_tokens"`
	CostUSD         float64 `json:"cost_usd"`
	CostKRW         float64 `json:"cost_krw"`
}

type ModelTokenAggregate struct {
	UnpricedCalls   int64  `json:"unpriced_calls"`
	ThinkingTokens  int64  `json:"thinking_tokens"`
	ToolUseTokens   int64  `json:"tool_use_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	UnmeasuredCalls int64  `json:"unmeasured_calls"`
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
	var thinking, tool, cached, unmeasured, unpriced int64

	type keyStats struct {
		thinking, tool, cached, unmeasured, unpriced int64
		promptTokens                                 int64
		candidateTokens                              int64
		totalTokens                                  int64
		costUSD                                      float64
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
		t, v, cache, missing := UsageExtras(u)
		unknownPrice := int64(0)
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(u.Model)), "lyria-") {
			unknownPrice = 1
		}
		unpriced += unknownPrice
		thinking += t
		tool += v
		cached += cache
		unmeasured += missing
		usd, _ := CalculateTokensCost(u.Model, p+v, c+t)

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
		ts.thinking += t
		ts.tool += v
		ts.cached += cache
		ts.unmeasured += missing
		ts.unpriced += unknownPrice

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
		ms.thinking += t
		ms.tool += v
		ms.cached += cache
		ms.unmeasured += missing
		ms.unpriced += unknownPrice
	}

	taskItems := make([]BreakdownItem, 0, len(taskKeys))
	for _, k := range taskKeys {
		s := byTask[k]
		costUSD := RoundUSD(s.costUSD)
		costKRW := RoundKRW(costUSD * KRWPerUSD)
		taskItems = append(taskItems, BreakdownItem{ThinkingTokens: s.thinking, ToolUseTokens: s.tool, CachedTokens: s.cached, UnmeasuredCalls: s.unmeasured, UnpricedCalls: s.unpriced,
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
		modelItems = append(modelItems, BreakdownItem{ThinkingTokens: s.thinking, ToolUseTokens: s.tool, CachedTokens: s.cached, UnmeasuredCalls: s.unmeasured, UnpricedCalls: s.unpriced,
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

	return SessionCostResponse{ThinkingTokens: thinking, ToolUseTokens: tool, CachedTokens: cached, UnmeasuredCalls: unmeasured, UnpricedCalls: unpriced,
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
	var thinking, tool, cached, unmeasured, unpriced int64

	for _, u := range usages {
		p := int64(u.PromptTokens)
		c := int64(u.CandidateTokens)
		tot := int64(u.TotalTokens)
		t, v, cache, missing := UsageExtras(u)
		unknownPrice := int64(0)
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(u.Model)), "lyria-") {
			unknownPrice = 1
		}
		unpriced += unknownPrice
		thinking += t
		tool += v
		cached += cache
		unmeasured += missing
		usd, _ := CalculateTokensCost(u.Model, p+v, c+t)

		totalPrompt += p
		totalCandidate += c
		totalTokens += tot
		totalUSD += usd
	}

	costUSD := RoundUSD(totalUSD)
	costKRW := RoundKRW(costUSD * KRWPerUSD)

	return TotalCostResponse{ThinkingTokens: thinking, ToolUseTokens: tool, CachedTokens: cached, UnmeasuredCalls: unmeasured, UnpricedCalls: unpriced,
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
	var thinking, tool, cached, unmeasured, unpriced int64

	for _, a := range aggregates {
		thinking += a.ThinkingTokens
		tool += a.ToolUseTokens
		cached += a.CachedTokens
		unmeasured += a.UnmeasuredCalls
		unpriced += a.UnpricedCalls
		usd, _ := CalculateTokensCost(a.Model, a.PromptTokens+a.ToolUseTokens, a.CandidateTokens+a.ThinkingTokens)
		totalPrompt += a.PromptTokens
		totalCandidate += a.CandidateTokens
		totalTokens += a.TotalTokens
		totalUSD += usd
	}

	costUSD := RoundUSD(totalUSD)
	costKRW := RoundKRW(costUSD * KRWPerUSD)

	return TotalCostResponse{ThinkingTokens: thinking, ToolUseTokens: tool, CachedTokens: cached, UnmeasuredCalls: unmeasured, UnpricedCalls: unpriced,
		PromptTokens:    totalPrompt,
		CandidateTokens: totalCandidate,
		TotalTokens:     totalTokens,
		CostUSD:         costUSD,
		CostKRW:         costKRW,
	}
}

// UsageExtras returns measured subtotals. Cached tokens are a subset of prompt
// tokens and must never be added a second time. Missing fields remain nullable
// in the ledger; unmeasured calls flag that these sums are incomplete.
func UsageExtras(u db.TokenUsage) (thinking, tool, cached, unmeasured int64) {
	if len(u.UsageMetadata) == 0 {
		return
	} // legacy row, no finer metadata retained
	var d gemini.ComparisonUsage
	if json.Unmarshal(u.UsageMetadata, &d) != nil {
		return 0, 0, 0, 1
	}
	if d.Thinking != nil {
		thinking = *d.Thinking
	}
	if d.ToolUse != nil {
		tool = *d.ToolUse
	}
	if d.Cached != nil {
		cached = *d.Cached
	}
	if d.Input == nil || d.Output == nil || d.Total == nil {
		unmeasured = 1
	}
	return
}
