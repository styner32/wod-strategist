package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"
)

const (
	argonMaxOutputTokens = int32(32768)
	argonRequestTimeout  = 20 * time.Minute
)

// generateContent retains only usage metadata for accounting, including empty or
// rejected responses. The recorder is per request; headers and keys never persist.
func (c *Client) generateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, *TokenUsage, error) {
	argon := strings.EqualFold(strings.TrimSpace(model), ModelArgon4)
	if argon {
		// v1.71 does not support long-decode continuation. Bound one request and
		// reject truncated output rather than passing it to downstream parsers.
		bounded := genai.GenerateContentConfig{}
		if config != nil {
			bounded = *config
		}
		bounded.MaxOutputTokens = argonMaxOutputTokens
		if bounded.ThinkingConfig == nil {
			bounded.ThinkingConfig = c.thinkingConfigForModel(model)
		} else {
			thinking := *bounded.ThinkingConfig
			thinking.ThinkingBudget = nil
			bounded.ThinkingConfig = &thinking
		}
		config = &bounded
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, argonRequestTimeout)
		defer cancel()
	}
	captured := &VideoModeResult{}
	resp, err := c.client.Models.GenerateContent(context.WithValue(ctx, comparisonKey{}, captured), model, contents, config)
	var envelope struct {
		Usage             *ComparisonUsage `json:"usageMetadata"`
		ContinuationToken string           `json:"continuationToken"`
	}
	_ = json.Unmarshal(captured.Response, &envelope)
	usage := UsageFromComparison(envelope.Usage, model)
	if argon && err == nil {
		if envelope.ContinuationToken != "" {
			return nil, usage, fmt.Errorf("Argon output is incomplete: continuation is required")
		}
		if resp != nil {
			for _, candidate := range resp.Candidates {
				if candidate != nil && candidate.FinishReason == genai.FinishReasonMaxTokens {
					return nil, usage, fmt.Errorf("Argon output is incomplete: MAX_TOKENS")
				}
			}
		}
	}
	return resp, usage, err
}

// UsageFromComparison preserves missing counters rather than inventing zeros.
// Legacy int32 counters remain for existing reanalysis response contracts.
func UsageFromComparison(details *ComparisonUsage, model string) *TokenUsage {
	usage := &TokenUsage{Model: model, Details: details}
	if details != nil {
		if details.Input != nil {
			usage.PromptTokens = int32(*details.Input)
		}
		if details.Output != nil {
			usage.CandidateTokens = int32(*details.Output)
		}
		if details.Total != nil {
			usage.TotalTokens = int32(*details.Total)
		}
	}
	return usage
}
