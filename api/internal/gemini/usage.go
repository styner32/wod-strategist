package gemini

import (
	"context"
	"encoding/json"

	"google.golang.org/genai"
)

// generateContent retains only usage metadata for accounting, including empty or
// rejected responses. The recorder is per request; headers and keys never persist.
func (c *Client) generateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, *TokenUsage, error) {
	captured := &VideoModeResult{}
	resp, err := c.client.Models.GenerateContent(context.WithValue(ctx, comparisonKey{}, captured), model, contents, config)
	var envelope struct {
		Usage *ComparisonUsage `json:"usageMetadata"`
	}
	_ = json.Unmarshal(captured.Response, &envelope)
	usage := UsageFromComparison(envelope.Usage, model)
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
