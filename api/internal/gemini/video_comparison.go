package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/genai"
)

// ComparisonUsage uses pointers because omitted usage is not measured zero.
type ComparisonUsage struct {
	Input    *int64 `json:"promptTokenCount"`
	Output   *int64 `json:"candidatesTokenCount"`
	Thinking *int64 `json:"thoughtsTokenCount"`
	ToolUse  *int64 `json:"toolUsePromptTokenCount"`
	Cached   *int64 `json:"cachedContentTokenCount"`
	Total    *int64 `json:"totalTokenCount"`
}

type VideoModeResult struct {
	Mode                 string           `json:"mode"`
	StartedAt            time.Time        `json:"started_at"`
	ElapsedSeconds       float64          `json:"elapsed_seconds"`
	HTTPStatus           int              `json:"http_status"`
	HTTPAttempts         int              `json:"http_attempts"`
	Outcome              string           `json:"outcome"`
	Error                string           `json:"error,omitempty"`
	FinishReason         string           `json:"finish_reason"`
	Usage                *ComparisonUsage `json:"usage"`
	MediaToolCalls       int              `json:"media_tool_calls"`
	MediaToolResponses   int              `json:"media_tool_responses"`
	UntypedToolCalls     int              `json:"untyped_tool_calls"`
	UntypedToolResponses int              `json:"untyped_tool_responses"`
	AgenticObserved      bool             `json:"agentic_observed"`
	Text                 string           `json:"-"`
	Request              []byte           `json:"-"`
	Response             []byte           `json:"-"`
}

type comparisonKey struct{}
type comparisonTransport struct{ base http.RoundTripper }

// Capture only generation bodies, never URLs, headers, or upload bytes. Read
// through EOF before stopping the clock, then restore the body for SDK decoding.
func (t comparisonTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r, ok := req.Context().Value(comparisonKey{}).(*VideoModeResult)
	if !ok || !strings.HasSuffix(req.URL.Path, ":generateContent") {
		return t.base.RoundTrip(req)
	}
	if r.HTTPAttempts >= 1 {
		return nil, fmt.Errorf("comparison forbids automatic generation retries")
	}
	r.HTTPAttempts++
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	r.Request = append([]byte(nil), body...)
	forward := req.Clone(req.Context())
	forward.Body = io.NopCloser(bytes.NewReader(body))
	forward.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	started := time.Now()
	r.StartedAt = started.UTC()
	defer func() { r.ElapsedSeconds = time.Since(started).Seconds() }()
	resp, err := t.base.RoundTrip(forward)
	if err != nil {
		return nil, err
	}
	r.HTTPStatus = resp.StatusCode
	r.Response, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(r.Response))
	return resp, nil
}

// NewComparisonClient shares the existing SDK configuration and Files lifecycle;
// its per-request recorder is also used for normal generation usage accounting.
func NewComparisonClient(ctx context.Context, options Options) (*Client, error) {
	options.Model = ModelFlash38
	return NewClientWithOptions(ctx, zap.NewNop(), options)
}

type ComparisonPreparation struct {
	UploadSeconds float64 `json:"upload_seconds"`
	ReadySeconds  float64 `json:"files_ready_seconds"`
}

// The non-nil UploadResult on polling failure lets the caller clean up only
// the file this experiment created.
func (c *Client) UploadComparisonVideo(ctx context.Context, path string) (*UploadResult, ComparisonPreparation, error) {
	var timing ComparisonPreparation
	f, err := os.Open(path)
	if err != nil {
		return nil, timing, err
	}
	defer f.Close()
	started := time.Now()
	file, err := c.client.Files.Upload(ctx, f, &genai.UploadFileConfig{MIMEType: "video/mp4"})
	timing.UploadSeconds = time.Since(started).Seconds()
	if err != nil {
		return nil, timing, err
	}
	result := &UploadResult{FileName: file.Name}
	started = time.Now()
	active, duration, err := c.waitForFileActive(ctx, file.Name)
	timing.ReadySeconds = time.Since(started).Seconds()
	if err != nil {
		return result, timing, err
	}
	result.FileURI, result.MIMEType, result.VideoDuration = active.URI, "video/mp4", duration
	return result, timing, nil
}

// CompareVideoMode is deliberately single-turn and has no persistence or worker
// side effects. Both modes receive the identical full-video indexing request.
func (c *Client) CompareVideoMode(ctx context.Context, fileURI, mimeType, prompt string, mode genai.MediaProcessing) VideoModeResult {
	r := VideoModeResult{Mode: string(mode), Outcome: "failed"}
	if mode != genai.MediaProcessingStatic && mode != genai.MediaProcessingAgentic {
		r.Error = "unsupported comparison mode"
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	ctx = context.WithValue(ctx, comparisonKey{}, &r)
	config := &genai.GenerateContentConfig{
		MediaResolution: genai.MediaResolutionHigh,
		ThinkingConfig:  c.thinkingConfigForModel(ModelFlash38),
		HTTPOptions:     &genai.HTTPOptions{RetryOptions: &genai.HTTPRetryOptions{Attempts: genai.Ptr(int32(1))}},
	}
	_, err := c.client.Models.GenerateContent(ctx, ModelFlash38, []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{FileData: &genai.FileData{FileURI: fileURI, MIMEType: mimeType}, MediaProcessing: mode},
			genai.NewPartFromText(prompt),
		},
	}}, config)
	parseComparisonResponse(&r)
	if err != nil {
		r.Outcome, r.Error = "failed", err.Error()
	}
	return r
}

func parseComparisonResponse(r *VideoModeResult) {
	var raw struct {
		Usage      *ComparisonUsage `json:"usageMetadata"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []struct {
					Text     string `json:"text"`
					Thought  bool   `json:"thought"`
					ToolCall *struct {
						ToolType string `json:"toolType"`
					} `json:"toolCall"`
					ToolResponse *struct {
						ToolType string `json:"toolType"`
					} `json:"toolResponse"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(r.Response, &raw); err != nil {
		r.Error = "response is empty or invalid JSON"
		return
	}
	r.Usage = raw.Usage
	if len(raw.Candidates) == 0 {
		r.Error = "response has no candidate"
		return
	}
	candidate := raw.Candidates[0]
	r.FinishReason = candidate.FinishReason
	for _, p := range candidate.Content.Parts {
		if !p.Thought {
			r.Text += p.Text
		}
	}
	// Terminal streaming events can include an additional unindexed candidate
	// containing the final tool response. Count evidence across all candidates,
	// while keeping answer text/finish reason tied to the selected first candidate.
	for _, candidate := range raw.Candidates {
		for _, p := range candidate.Content.Parts {
			if p.ToolCall != nil && p.ToolCall.ToolType == "MEDIA_PROCESSING" {
				r.MediaToolCalls++
			}
			if p.ToolResponse != nil && p.ToolResponse.ToolType == "MEDIA_PROCESSING" {
				r.MediaToolResponses++
			}
			if p.ToolCall != nil && p.ToolCall.ToolType == "" {
				r.UntypedToolCalls++
			}
			if p.ToolResponse != nil && p.ToolResponse.ToolType == "" {
				r.UntypedToolResponses++
			}
		}
	}
	r.AgenticObserved = r.Mode == "AGENTIC" && r.MediaToolCalls > 0 && r.MediaToolResponses > 0
	if strings.TrimSpace(r.Text) == "" {
		r.Error = "response has no final answer"
		return
	}
	if r.FinishReason != "STOP" {
		r.Outcome = "incomplete"
		return
	}
	r.Outcome = "completed"
}
