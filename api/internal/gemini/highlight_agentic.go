package gemini

import (
	"context"
	"google.golang.org/genai"
)

// AnalyzeHighlightAgentic fixes the experiment settings independently of the
// production segment client. Raw response/request bytes remain private.
func (c *Client) AnalyzeHighlightAgentic(ctx context.Context, fileURI, mimeType, prompt string) StreamComparisonResult {
	clone := *c
	clone.thinkingLevel = "HIGH"
	clone.thinkingBudget = nil
	return clone.CompareVideoModeStream(ctx, fileURI, mimeType, prompt, genai.MediaProcessingAgentic, StreamComparisonOptions{MediaResolution: genai.MediaResolutionLow})
}
