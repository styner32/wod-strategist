package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"
)

// StreamComparisonResult preserves partial evidence even if the stream fails.
// Response holds exact SSE bytes, not a single JSON response.
type StreamComparisonResult struct {
	VideoModeResult
	EventCount        int      `json:"event_count"`
	FirstEventSeconds *float64 `json:"first_event_seconds"`
	BodyEOF           bool     `json:"body_eof"`
}

type StreamProgress struct {
	Event                int     `json:"event"`
	Elapsed              float64 `json:"elapsed_seconds"`
	ToolCalls            int     `json:"tool_calls"`
	ToolResponses        int     `json:"tool_responses"`
	UntypedToolCalls     int     `json:"untyped_tool_calls"`
	UntypedToolResponses int     `json:"untyped_tool_responses"`
	FinishReason         string  `json:"finish_reason,omitempty"`
}

type StreamComparisonOptions struct {
	RawResponse     io.Writer // optional durable sink; never buffers before SDK consumption
	OnEvent         func(StreamProgress)
	MediaResolution genai.MediaResolution // empty preserves the comparison default (HIGH)
}

type streamComparisonKey struct{}
type streamRecording struct {
	result  *StreamComparisonResult
	writer  io.Writer
	started time.Time
}
type streamComparisonTransport struct{ base http.RoundTripper }

func (t streamComparisonTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	capture, ok := req.Context().Value(streamComparisonKey{}).(*streamRecording)
	if !ok || !strings.HasSuffix(req.URL.Path, ":streamGenerateContent") {
		return t.base.RoundTrip(req)
	}
	r := capture.result
	if r.HTTPAttempts != 0 {
		return nil, fmt.Errorf("comparison forbids automatic stream retries")
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
	capture.started = time.Now()
	r.StartedAt = capture.started.UTC()
	resp, err := t.base.RoundTrip(forward)
	if err != nil {
		r.ElapsedSeconds = time.Since(capture.started).Seconds()
		return nil, err
	}
	r.HTTPStatus = resp.StatusCode
	// Do not ReadAll here: that would turn streaming back into a buffered call.
	resp.Body = &streamRecordingBody{ReadCloser: resp.Body, capture: capture}
	return resp, nil
}

type streamRecordingBody struct {
	io.ReadCloser
	capture *streamRecording
	ended   bool
}

func (b *streamRecordingBody) end() {
	if !b.ended {
		b.capture.result.ElapsedSeconds = time.Since(b.capture.started).Seconds()
		b.ended = true
	}
}

func (b *streamRecordingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.capture.result.Response = append(b.capture.result.Response, p[:n]...)
		if b.capture.writer != nil {
			written, writeErr := b.capture.writer.Write(p[:n])
			if writeErr != nil {
				err = writeErr
			} else if written != n {
				err = io.ErrShortWrite
			}
		}
	}
	if err != nil {
		b.capture.result.BodyEOF = err == io.EOF
		b.end()
	}
	return n, err
}

func (b *streamRecordingBody) Close() error { b.end(); return b.ReadCloser.Close() }

// CompareVideoModeStream uses the same model, thinking and retry policy as
// CompareVideoMode, with an optional media resolution override. Keep the partial
// result on errors, and require final STOP plus body EOF before calling it complete.
func (c *Client) CompareVideoModeStream(ctx context.Context, fileURI, mimeType, prompt string, mode genai.MediaProcessing, opts StreamComparisonOptions) StreamComparisonResult {
	r := StreamComparisonResult{VideoModeResult: VideoModeResult{Mode: string(mode), Outcome: "failed"}}
	if mode != genai.MediaProcessingStatic && mode != genai.MediaProcessingAgentic {
		r.Error = "unsupported comparison mode"
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	capture := &streamRecording{result: &r, writer: opts.RawResponse}
	ctx = context.WithValue(ctx, streamComparisonKey{}, capture)
	resolution := opts.MediaResolution
	if resolution == "" {
		resolution = genai.MediaResolutionHigh
	}
	config := &genai.GenerateContentConfig{
		MediaResolution: resolution,
		ThinkingConfig:  c.thinkingConfigForModel(ModelFlash38),
		HTTPOptions:     &genai.HTTPOptions{RetryOptions: &genai.HTTPRetryOptions{Attempts: genai.Ptr(int32(1))}},
	}
	contents := []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{
		{FileData: &genai.FileData{FileURI: fileURI, MIMEType: mimeType}, MediaProcessing: mode},
		genai.NewPartFromText(prompt),
	}}}
	var streamErr error
	for chunk, err := range c.client.Models.GenerateContentStream(ctx, ModelFlash38, contents, config) {
		if err != nil {
			streamErr = err
			break
		}
		if chunk == nil {
			continue
		}
		r.EventCount++
		elapsed := time.Since(capture.started).Seconds()
		if r.FirstEventSeconds == nil {
			r.FirstEventSeconds = &elapsed
		}
		progress := StreamProgress{Event: r.EventCount, Elapsed: elapsed}
		if len(chunk.Candidates) > 0 && chunk.Candidates[0] != nil {
			candidate := chunk.Candidates[0]
			progress.FinishReason = string(candidate.FinishReason)
		}
		for _, candidate := range chunk.Candidates {
			if candidate == nil {
				continue
			}
			if candidate.Content != nil {
				for _, p := range candidate.Content.Parts {
					if p == nil {
						continue
					}
					if p.ToolCall != nil && string(p.ToolCall.ToolType) == "MEDIA_PROCESSING" {
						progress.ToolCalls++
					}
					if p.ToolResponse != nil && string(p.ToolResponse.ToolType) == "MEDIA_PROCESSING" {
						progress.ToolResponses++
					}
					if p.ToolCall != nil && p.ToolCall.ToolType == "" {
						progress.UntypedToolCalls++
					}
					if p.ToolResponse != nil && p.ToolResponse.ToolType == "" {
						progress.UntypedToolResponses++
					}
				}
			}
		}
		if opts.OnEvent != nil {
			opts.OnEvent(progress)
		}
	}
	// Decode the original data events so omitted usage fields stay nil rather than
	// becoming SDK numeric zero values. Last usage snapshot wins; never sum chunks.
	for _, line := range bytes.Split(r.Response, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if !json.Valid(data) {
			continue
		} // SDK surfaces the parse error; retain raw bytes.
		part := VideoModeResult{Mode: string(mode), Response: data}
		parseComparisonResponse(&part)
		if part.Usage != nil {
			r.Usage = part.Usage
		}
		if part.FinishReason != "" {
			r.FinishReason = part.FinishReason
		}
		r.Text += part.Text
		r.MediaToolCalls += part.MediaToolCalls
		r.MediaToolResponses += part.MediaToolResponses
		r.UntypedToolCalls += part.UntypedToolCalls
		r.UntypedToolResponses += part.UntypedToolResponses
	}
	r.AgenticObserved = mode == genai.MediaProcessingAgentic && r.MediaToolCalls > 0 && r.MediaToolResponses > 0
	switch {
	case streamErr != nil:
		r.Error = streamErr.Error()
	case ctx.Err() != nil:
		r.Error = ctx.Err().Error()
	case strings.TrimSpace(r.Text) == "":
		r.Error = "stream has no final answer"
	case r.FinishReason != "STOP" || !r.BodyEOF:
		r.Outcome = "incomplete"
	default:
		r.Outcome = "completed"
	}
	return r
}
