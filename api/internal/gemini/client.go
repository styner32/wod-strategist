package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/genai"
)

// TokenUsage holds the token consumption metrics returned by GenerateContent.
type TokenUsage struct {
	PromptTokens    int32
	CandidateTokens int32
	TotalTokens     int32
	Model           string
	Details         *ComparisonUsage
	ThoughtSummary  string `json:"-"`
}

const (
	ModelArgon4            = "gemini-4-argon"
	ModelPro31Preview      = "gemini-3.1-pro-preview"
	ModelFlash35Lite       = "gemini-3.5-flash-lite"
	ModelFlash37           = "gemini-3.7-flash"
	ModelFlash38           = "gemini-3.8-flash"
	ModelFlashTTS31Preview = "gemini-3.1-flash-tts-preview"

	// Retained for backward compatibility
	ModelFlash30Preview = ModelFlash35Lite
)

const defaultModel = ModelArgon4

type Client struct {
	client             *genai.Client
	logger             *zap.Logger
	model              string
	thinkingLevel      string
	thinkingLevelChunk string
	thinkingBudget     *int32
	includeThoughts    bool
	pollInterval       time.Duration
	sleep              func(time.Duration)
}

// Model returns the configured generation model used by AnalyzeSegment.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

type Options struct {
	APIKey             string
	BaseURL            string
	APIVersion         string
	HTTPClient         *http.Client
	Model              string // e.g. "gemini-4-argon", "gemini-3.1-pro-preview", "gemini-3.8-flash"
	ThinkingLevel      string // e.g. "HIGH", "MEDIUM", "LOW", "MINIMAL" (default / full video analysis)
	ThinkingLevelChunk string // e.g. "LOW", "MEDIUM" (for short chunk analysis)
	ThinkingBudget     *int32 // Optional token budget for thinking
	IncludeThoughts    bool   // Optional: request Gemini thought summaries in responses
	PollInterval       time.Duration
	Sleep              func(time.Duration)
}

type exactSegmentOffsets struct {
	start time.Duration
	end   time.Duration
}

type exactSegmentOffsetsContextKey struct{}
type exactBatchSegmentOffsetsContextKey struct{}
type thinkingLevelContextKey struct{}

// WithThinkingLevel returns a context that overrides the client's default
// ThinkingLevel for a single GenerateContent call (such as AnalyzeFrameGridsWithModel).
func WithThinkingLevel(ctx context.Context, level string) context.Context {
	return context.WithValue(ctx, thinkingLevelContextKey{}, strings.TrimSpace(level))
}

func thinkingLevelFromContext(ctx context.Context, fallback string) string {
	if ctx != nil {
		if level, ok := ctx.Value(thinkingLevelContextKey{}).(string); ok && strings.TrimSpace(level) != "" {
			return strings.TrimSpace(level)
		}
	}
	return fallback
}

// exactSegmentOffsetsTransport compensates for google.golang.org/genai's
// VideoMetadata marshaler rounding offsets to whole seconds. Gemini accepts
// protobuf Duration JSON with fractional seconds, so restore the exact
// persisted interval immediately before the request is sent.
type exactSegmentOffsetsTransport struct {
	base http.RoundTripper
}

func (t exactSegmentOffsetsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	offsets, ok := req.Context().Value(exactSegmentOffsetsContextKey{}).(exactSegmentOffsets)
	batchOffsets, batchOK := req.Context().Value(exactBatchSegmentOffsetsContextKey{}).([]exactSegmentOffsets)
	if (!ok && !batchOK) || req.Body == nil {
		return t.base.RoundTrip(req)
	}

	body, err := io.ReadAll(req.Body)
	closeErr := req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read segment analysis request: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close segment analysis request: %w", closeErr)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode segment analysis request: %w", err)
	}
	if batchOK {
		if !replaceBatchVideoMetadataOffsets(payload, batchOffsets) {
			return nil, fmt.Errorf("batch segment analysis request is missing video metadata")
		}
	} else if !replaceVideoMetadataOffsets(payload, offsets) {
		return nil, fmt.Errorf("segment analysis request is missing video metadata")
	}
	body, err = json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode segment analysis request: %w", err)
	}

	forward := req.Clone(req.Context())
	forward.Body = io.NopCloser(bytes.NewReader(body))
	forward.ContentLength = int64(len(body))
	forward.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return t.base.RoundTrip(forward)
}

func replaceBatchVideoMetadataOffsets(payload map[string]any, batchOffsets []exactSegmentOffsets) bool {
	if len(batchOffsets) == 0 {
		return false
	}
	idx := 0
	var walk func(node any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if _, hasContents := v["contents"]; hasContents && idx < len(batchOffsets) {
				if replaceVideoMetadataOffsets(v, batchOffsets[idx]) {
					idx++
					return
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		}
	}
	walk(payload)
	return idx == len(batchOffsets)
}

func replaceVideoMetadataOffsets(payload map[string]any, offsets exactSegmentOffsets) bool {
	contents, _ := payload["contents"].([]any)
	for _, rawContent := range contents {
		content, _ := rawContent.(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			metadata, _ := part["videoMetadata"].(map[string]any)
			if metadata == nil {
				continue
			}
			metadata["startOffset"] = protobufDurationJSON(offsets.start)
			metadata["endOffset"] = protobufDurationJSON(offsets.end)
			return true
		}
	}
	return false
}

func protobufDurationJSON(duration time.Duration) string {
	sign := ""
	if duration < 0 {
		sign = "-"
		duration = -duration
	}
	seconds := duration / time.Second
	nanos := duration % time.Second
	if nanos == 0 {
		return sign + strconv.FormatInt(int64(seconds), 10) + "s"
	}

	var fraction string
	switch {
	case nanos%time.Millisecond == 0:
		fraction = fmt.Sprintf("%03d", nanos/time.Millisecond)
	case nanos%time.Microsecond == 0:
		fraction = fmt.Sprintf("%06d", nanos/time.Microsecond)
	default:
		fraction = fmt.Sprintf("%09d", nanos)
	}
	return fmt.Sprintf("%s%d.%ss", sign, seconds, fraction)
}

func NewClientWithOptions(ctx context.Context, logger *zap.Logger, options Options) (*Client, error) {
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is not set")
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	wrappedHTTPClient := *httpClient
	baseTransport := wrappedHTTPClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	wrappedHTTPClient.Transport = exactSegmentOffsetsTransport{base: streamComparisonTransport{base: comparisonTransport{base: baseTransport}}}

	config := &genai.ClientConfig{
		APIKey:     apiKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: &wrappedHTTPClient,
		// v1.71 adds automatic retries. Preserve the existing worker-owned retry
		// policy and avoid hidden duplicate inference requests.
		HTTPOptions: genai.HTTPOptions{RetryOptions: &genai.HTTPRetryOptions{Attempts: genai.Ptr(int32(1))}},
	}
	if options.BaseURL != "" || options.APIVersion != "" {
		config.HTTPOptions.BaseURL = options.BaseURL
		config.HTTPOptions.APIVersion = options.APIVersion
	}

	client, err := genai.NewClient(ctx, config)
	if err != nil {
		return nil, err
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	model := options.Model
	if model == "" {
		model = defaultModel
	}

	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}

	sleep := options.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	thinkingLevel := options.ThinkingLevel
	thinkingLevelChunk := options.ThinkingLevelChunk
	if thinkingLevelChunk == "" {
		thinkingLevelChunk = thinkingLevel
	}

	return &Client{
		client:             client,
		logger:             logger,
		model:              model,
		thinkingLevel:      thinkingLevel,
		thinkingLevelChunk: thinkingLevelChunk,
		thinkingBudget:     options.ThinkingBudget,
		includeThoughts:    options.IncludeThoughts,
		pollInterval:       pollInterval,
		sleep:              sleep,
	}, nil
}

func supportsThinking(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return m == ModelArgon4 || strings.Contains(m, "3.8") || strings.Contains(m, "3.7") || strings.Contains(m, "2.5")
}

func (c *Client) thinkingConfigForModel(model string) *genai.ThinkingConfig {
	if c == nil {
		return nil
	}
	return c.thinkingConfigForModelAndLevel(model, c.thinkingLevel)
}

func (c *Client) thinkingConfigForModelAndLevel(model string, level string) *genai.ThinkingConfig {
	if c == nil || !supportsThinking(model) {
		return nil
	}
	argon := strings.EqualFold(strings.TrimSpace(model), ModelArgon4)
	if argon && strings.TrimSpace(level) == "" {
		level = "HIGH"
	}
	if level == "" && c.thinkingBudget == nil && !c.includeThoughts {
		return nil
	}
	tc := &genai.ThinkingConfig{
		IncludeThoughts: c.includeThoughts,
	}
	if c.thinkingBudget != nil && !argon {
		tc.ThinkingBudget = c.thinkingBudget
	}
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "HIGH":
		tc.ThinkingLevel = genai.ThinkingLevelHigh
	case "MEDIUM":
		tc.ThinkingLevel = genai.ThinkingLevelMedium
	case "LOW":
		tc.ThinkingLevel = genai.ThinkingLevelLow
	case "MINIMAL":
		tc.ThinkingLevel = genai.ThinkingLevelMinimal
	}
	return tc
}

func extractCandidateText(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if part != nil && !part.Thought {
			sb.WriteString(part.Text)
		}
	}
	return sb.String()
}

func extractCandidateThoughts(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if part != nil && part.Thought {
			sb.WriteString(part.Text)
		}
	}
	return sb.String()
}

// mimeTypeFromExtension returns a MIME type based on the file extension.
// Returns empty string if the extension is not recognized.
func mimeTypeFromExtension(filePath string) string {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	default:
		return ""
	}
}

// formatFileSize returns a human-readable file size string.
func formatFileSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// AnalyzeVideo returns the analysis result, the name of the uploaded file on Gemini, and token usage.
func (c *Client) AnalyzeVideo(ctx context.Context, filePath string, prompt string) (string, string, *TokenUsage, error) {
	return c.AnalyzeVideoWithModel(ctx, filePath, prompt, c.model)
}

// AnalyzeVideoWithModel returns the analysis result, the name of the uploaded file on Gemini, and token usage using the specified model.
// It uses the default thinking level (c.thinkingLevel).
func (c *Client) AnalyzeVideoWithModel(ctx context.Context, filePath string, prompt string, model string) (string, string, *TokenUsage, error) {
	level := ""
	if c != nil {
		level = c.thinkingLevel
	}
	return c.AnalyzeVideoWithThinking(ctx, filePath, prompt, model, level)
}

// AnalyzeChunkVideo analyzes a short video chunk using the configured chunk thinking level (c.thinkingLevelChunk).
func (c *Client) AnalyzeChunkVideo(ctx context.Context, filePath string, prompt string, model string) (string, string, *TokenUsage, error) {
	level := ""
	if c != nil {
		level = c.thinkingLevelChunk
	}
	return c.AnalyzeVideoWithThinking(ctx, filePath, prompt, model, level)
}

// AnalyzeChunkVideoWithImages is AnalyzeChunkVideo with reference images
// (e.g. the tracked person marked in a previous frame) placed after the
// video so temporal video tokens lead attention. The prompt must explain what each image is.
func (c *Client) AnalyzeChunkVideoWithImages(ctx context.Context, filePath string, prompt string, model string, images [][]byte) (string, string, *TokenUsage, error) {
	level := ""
	if c != nil {
		level = c.thinkingLevelChunk
	}
	return c.analyzeVideoWithParts(ctx, filePath, prompt, model, level, images)
}

// AnalyzeVideoWithThinking returns the analysis result, the name of the uploaded file on Gemini, and token usage using the specified model and thinking level.
func (c *Client) AnalyzeVideoWithThinking(ctx context.Context, filePath string, prompt string, model string, thinkingLevel string) (string, string, *TokenUsage, error) {
	return c.analyzeVideoWithParts(ctx, filePath, prompt, model, thinkingLevel, nil)
}

func (c *Client) analyzeVideoWithParts(ctx context.Context, filePath string, prompt string, model string, thinkingLevel string, images [][]byte) (string, string, *TokenUsage, error) {
	// Upload file
	f, err := os.Open(filePath)
	if err != nil {
		return "", "", nil, err
	}
	defer f.Close()

	// Detect MIME type: prefer extension-based (reliable for .mp4), fall back to content sniffing
	mimeType := mimeTypeFromExtension(filePath)
	if mimeType == "" {
		buffer := make([]byte, 512)
		n, err := f.Read(buffer)
		if err != nil {
			return "", "", nil, fmt.Errorf("failed to read file header: %w", err)
		}
		mimeType = http.DetectContentType(buffer[:n])
		if _, err := f.Seek(0, 0); err != nil {
			return "", "", nil, fmt.Errorf("failed to reset file pointer: %w", err)
		}
	}

	uploadResult, err := c.client.Files.Upload(ctx, f, &genai.UploadFileConfig{
		MIMEType: mimeType,
	})
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to upload file: %w", err)
	}

	// Poll for file state to be ACTIVE
	file, _, err := c.waitForFileActive(ctx, uploadResult.Name)
	if err != nil {
		return "", uploadResult.Name, nil, err
	}

	c.logger.Info("File uploaded", zap.Any("file", file), zap.String("mime_type", mimeType))

	// Generate content — single multimodal turn with video first for better temporal grounding
	var genConfig *genai.GenerateContentConfig
	if tc := c.thinkingConfigForModelAndLevel(model, thinkingLevel); tc != nil {
		genConfig = &genai.GenerateContentConfig{
			ThinkingConfig: tc,
		}
	}
	parts := make([]*genai.Part, 0, len(images)+2)
	parts = append(parts, &genai.Part{FileData: &genai.FileData{FileURI: uploadResult.URI, MIMEType: mimeType}})
	for _, img := range images {
		if len(img) == 0 {
			continue
		}
		mime := DetectImageMIME(img)
		if mime == "" {
			mime = "image/jpeg"
		}
		parts = append(parts, &genai.Part{InlineData: &genai.Blob{MIMEType: mime, Data: img}})
	}
	parts = append(parts, &genai.Part{Text: prompt})
	resp, usage, err := c.generateContent(ctx, model, []*genai.Content{{
		Role:  genai.RoleUser,
		Parts: parts,
	}}, genConfig)
	if err != nil {
		return "", uploadResult.Name, usage, fmt.Errorf("failed to generate content: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", uploadResult.Name, usage, fmt.Errorf("no content generated")
	}

	c.logger.Info("Gemini response", zap.Any("response", resp))

	// Extract text from response
	result := extractCandidateText(resp)

	return result, uploadResult.Name, usage, nil
}

func (c *Client) DeleteFile(ctx context.Context, name string) error {
	resp, err := c.client.Files.Delete(ctx, name, nil)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	c.logger.Info("File deleted", zap.Any("response", resp))
	return nil
}

func (c *Client) FileExists(ctx context.Context, name string) (bool, error) {
	_, err := c.client.Files.Get(ctx, name, nil)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, fmt.Errorf("failed to check file existence: %w", err)
	}
	return true, nil
}

// FileVideoDuration returns an existing Files API video's processed duration.
// The bool is false when the file no longer exists.
func (c *Client) FileVideoDuration(ctx context.Context, name string) (time.Duration, bool, error) {
	file, err := c.client.Files.Get(ctx, name, nil)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("failed to get file duration: %w", err)
	}
	if file.VideoMetadata == nil {
		return 0, true, nil
	}
	durationText, _ := file.VideoMetadata["videoDuration"].(string)
	duration, parseErr := time.ParseDuration(durationText)
	if parseErr != nil {
		return 0, true, nil
	}
	return duration, true, nil
}

const maxConsecutiveFileGetErrors = 5

// isRetryableFileGetError returns true if err is a transient server error, rate limit, or connection issue.
func isRetryableFileGetError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Code >= 500 || apiErr.Code == 429 {
			return true
		}
		status := strings.ToUpper(apiErr.Status)
		if status == "INTERNAL" || status == "UNAVAILABLE" || status == "RESOURCE_EXHAUSTED" {
			return true
		}
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "504") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "internal") ||
		strings.Contains(errStr, "unavailable") ||
		strings.Contains(errStr, "resource_exhausted") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "eof") ||
		strings.Contains(errStr, "timeout")
}

// IsFileGoneError reports whether a Files API error means the file no longer
// exists. The Files API answers 403 PERMISSION_DENIED for missing or expired files.
func IsFileGoneError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		status := strings.ToUpper(apiErr.Status)
		return apiErr.Code == 403 || apiErr.Code == 404 || status == "PERMISSION_DENIED" || status == "NOT_FOUND"
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "403") || strings.Contains(errStr, "404") || strings.Contains(errStr, "not found") || strings.Contains(errStr, "permission_denied")
}

// waitForFileActive polls until the uploaded file reaches FileStateActive.
// It retries transient errors (e.g. HTTP 500, 503, 429) with backoff up to maxConsecutiveFileGetErrors times.
func (c *Client) waitForFileActive(ctx context.Context, fileName string) (*genai.File, time.Duration, error) {
	var videoDuration time.Duration
	consecutiveErrors := 0

	for {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}

		file, err := c.client.Files.Get(ctx, fileName, nil)
		if err != nil {
			if isRetryableFileGetError(err) && consecutiveErrors < maxConsecutiveFileGetErrors {
				consecutiveErrors++
				if c.logger != nil {
					c.logger.Warn("Transient error getting file state from Gemini, retrying...",
						zap.String("file_name", fileName),
						zap.Int("attempt", consecutiveErrors),
						zap.Int("max_attempts", maxConsecutiveFileGetErrors),
						zap.Error(err))
				}
				c.sleep(c.pollInterval * time.Duration(consecutiveErrors))
				continue
			}
			return nil, 0, fmt.Errorf("failed to get file info: %w", err)
		}
		consecutiveErrors = 0

		if file.State == genai.FileStateActive {
			if file.VideoMetadata != nil {
				if durStr, ok := file.VideoMetadata["videoDuration"].(string); ok {
					if d, parseErr := time.ParseDuration(durStr); parseErr == nil {
						videoDuration = d
					}
				}
			}
			return file, videoDuration, nil
		}
		if file.State == genai.FileStateFailed {
			return nil, 0, fmt.Errorf("file processing failed")
		}

		c.sleep(c.pollInterval)
	}
}

// GenerateWorkoutMusic generates a music clip using the Lyria 3 model and writes
// the resulting MP3 audio to outputPath.
// Use "lyria-3-clip-preview" for a 30-second clip or "lyria-3-pro-preview" for
// a full-length song (2-3 minutes).
func (c *Client) GenerateWorkoutMusic(ctx context.Context, model, prompt, outputPath string) (*TokenUsage, error) {
	c.logger.Info("Generating workout music",
		zap.String("model", model),
		zap.String("prompt", prompt),
		zap.String("output", outputPath))

	resp, usage, err := c.generateContent(ctx, model, genai.Text(prompt), &genai.GenerateContentConfig{
		ResponseModalities: []string{"AUDIO", "TEXT"},
	})
	if err != nil {
		return usage, fmt.Errorf("lyria generate_content failed: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil {
		return usage, fmt.Errorf("lyria returned no candidates")
	}

	var audioData []byte
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.InlineData != nil && len(part.InlineData.Data) > 0 {
			audioData = part.InlineData.Data
			break
		}
	}

	if len(audioData) == 0 {
		return usage, fmt.Errorf("lyria returned no audio data in part.InlineData")
	}

	if err := os.WriteFile(outputPath, audioData, 0o644); err != nil {
		return usage, fmt.Errorf("failed to write music to %s: %w", outputPath, err)
	}

	c.logger.Info("Workout music written", zap.String("path", outputPath), zap.Int("bytes", len(audioData)))
	return usage, nil
}

const flashModel = ModelFlash38

// UploadResult holds info about an uploaded file for use across multiple passes.
type UploadResult struct {
	FileName      string        // files/xxx — for DeleteFile cleanup
	FileURI       string        // https://... — for referencing in GenerateContent
	MIMEType      string        // video/mp4 etc.
	VideoDuration time.Duration // actual video length from Gemini processing
}

// UploadVideo uploads a local file to the Gemini Files API and polls until it's
// ACTIVE. The caller should defer DeleteFile(result.FileName) after use.
func (c *Client) UploadVideo(ctx context.Context, filePath string) (*UploadResult, error) {
	return c.UploadVideoWithObserver(ctx, filePath, nil)
}

// UploadVideoWithObserver journals file ownership immediately after upload,
// before potentially long Files processing. It never retries generation.
func (c *Client) UploadVideoWithObserver(ctx context.Context, filePath string, onUploaded func(*UploadResult) error) (*UploadResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer f.Close()

	mimeType := mimeTypeFromExtension(filePath)
	if mimeType == "" {
		buffer := make([]byte, 512)
		n, readErr := f.Read(buffer)
		if readErr != nil {
			return nil, fmt.Errorf("failed to read file header: %w", readErr)
		}
		mimeType = http.DetectContentType(buffer[:n])
		if _, seekErr := f.Seek(0, 0); seekErr != nil {
			return nil, fmt.Errorf("failed to reset file pointer: %w", seekErr)
		}
	}

	// Log file size for observability — critical for diagnosing upload timeouts
	var fileSizeBytes int64
	if fi, statErr := f.Stat(); statErr == nil {
		fileSizeBytes = fi.Size()
	}

	c.logger.Info("Uploading video file",
		zap.String("file_path", filePath),
		zap.String("mime_type", mimeType),
		zap.Int64("file_size_bytes", fileSizeBytes),
		zap.String("file_size_human", formatFileSize(fileSizeBytes)))

	uploadStart := time.Now()
	uploadResult, err := c.client.Files.Upload(ctx, f, &genai.UploadFileConfig{
		MIMEType: mimeType,
	})
	if err != nil {
		c.logger.Error("Video file upload failed",
			zap.String("file_path", filePath),
			zap.Int64("file_size_bytes", fileSizeBytes),
			zap.Duration("upload_duration", time.Since(uploadStart)),
			zap.Error(err))
		return nil, fmt.Errorf("failed to upload file (%s): %w", formatFileSize(fileSizeBytes), err)
	}

	if onUploaded != nil {
		uploaded := &UploadResult{FileName: uploadResult.Name, FileURI: uploadResult.URI, MIMEType: mimeType}
		if err := onUploaded(uploaded); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			_ = c.DeleteFile(cleanupCtx, uploadResult.Name)
			return uploaded, fmt.Errorf("journal uploaded file: %w", err)
		}
	}
	c.logger.Info("File uploaded, polling for ACTIVE",
		zap.String("file_name", uploadResult.Name),
		zap.String("file_uri", uploadResult.URI),
		zap.Duration("upload_duration", time.Since(uploadStart)),
		zap.Int64("file_size_bytes", fileSizeBytes))

	file, videoDuration, err := c.waitForFileActive(ctx, uploadResult.Name)
	if err != nil {
		return &UploadResult{FileName: uploadResult.Name}, err
	}

	c.logger.Info("File is ACTIVE",
		zap.String("file_name", file.Name),
		zap.String("file_uri", file.URI),
		zap.Duration("video_duration", videoDuration))

	return &UploadResult{
		FileName:      uploadResult.Name,
		FileURI:       uploadResult.URI,
		MIMEType:      mimeType,
		VideoDuration: videoDuration,
	}, nil
}

// IndexVideo uses the fast Flash model to scan the full video and identify
// exercise segments. Returns the raw model output as text (caller parses the
// JSON segment array).
func (c *Client) IndexVideo(ctx context.Context, fileURI, mimeType, prompt string) (string, *TokenUsage, error) {
	c.logger.Info("Indexing video with Flash",
		zap.String("file_uri", fileURI),
		zap.String("model", flashModel))

	genConfig := &genai.GenerateContentConfig{
		MediaResolution: genai.MediaResolutionHigh,
	}
	if tc := c.thinkingConfigForModel(flashModel); tc != nil {
		genConfig.ThinkingConfig = tc
	}
	resp, usage, err := c.generateContent(ctx, flashModel, []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{FileData: &genai.FileData{FileURI: fileURI, MIMEType: mimeType}},
			genai.NewPartFromText(prompt),
		},
	}}, genConfig)
	if err != nil {
		return "", usage, fmt.Errorf("failed to index video: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", usage, fmt.Errorf("no content from video indexing")
	}

	result := extractCandidateText(resp)

	c.logger.Info("Video indexed", zap.Int("response_length", len(result)))
	return result, usage, nil
}

// AnalyzeSegment performs deep biomechanical analysis on a specific time range
// of the video using the default model with VideoMetadata to constrain attention.
func (c *Client) AnalyzeSegment(ctx context.Context, fileURI, mimeType string, start, end time.Duration, prompt string) (string, *TokenUsage, error) {
	return c.AnalyzeSegmentWithModel(ctx, fileURI, mimeType, start, end, prompt, c.model)
}

// AnalyzeSegmentWithModel performs deep biomechanical analysis on a specific time range
// of the video using a specified model (e.g. ModelArgon4 or ModelPro31Preview).
func (c *Client) AnalyzeSegmentWithModel(ctx context.Context, fileURI, mimeType string, start, end time.Duration, prompt, model string) (string, *TokenUsage, error) {
	return c.AnalyzeSegmentWithModelAndImages(ctx, fileURI, mimeType, start, end, prompt, model, nil)
}

// AnalyzeSegmentWithModelAndImages is AnalyzeSegmentWithModel with reference
// images (tracked-person markers) placed after the video segment so temporal
// video tokens lead attention.
func (c *Client) AnalyzeSegmentWithModelAndImages(ctx context.Context, fileURI, mimeType string, start, end time.Duration, prompt, model string, images [][]byte) (string, *TokenUsage, error) {
	if model == "" {
		model = c.model
	}
	if start < 0 || end <= start {
		return "", nil, fmt.Errorf("invalid segment interval: start=%s end=%s", start, end)
	}
	fps := float64(5)

	c.logger.Info("Analyzing segment",
		zap.String("file_uri", fileURI),
		zap.String("model", model),
		zap.Duration("start", start),
		zap.Duration("end", end),
		zap.Float64("fps", fps))

	videoPart := &genai.Part{
		FileData: &genai.FileData{
			FileURI:  fileURI,
			MIMEType: mimeType,
		},
		VideoMetadata: &genai.VideoMetadata{
			StartOffset: start,
			EndOffset:   end,
			FPS:         &fps,
		},
	}

	exactOffsetContext := context.WithValue(ctx, exactSegmentOffsetsContextKey{}, exactSegmentOffsets{start: start, end: end})
	genConfig := &genai.GenerateContentConfig{
		MediaResolution: genai.MediaResolution(genai.MediaResolutionMedium),
	}
	thinkingLevel := thinkingLevelFromContext(ctx, c.thinkingLevel)
	if strings.TrimSpace(thinkingLevel) == "" {
		thinkingLevel = "HIGH"
	}
	if tc := c.thinkingConfigForModelAndLevel(model, thinkingLevel); tc != nil {
		genConfig.ThinkingConfig = tc
	}
	segmentParts := make([]*genai.Part, 0, len(images)+2)
	segmentParts = append(segmentParts, videoPart)
	for _, img := range images {
		if len(img) == 0 {
			continue
		}
		imgMIME := DetectImageMIME(img)
		if imgMIME == "" {
			imgMIME = "image/jpeg"
		}
		segmentParts = append(segmentParts, &genai.Part{InlineData: &genai.Blob{MIMEType: imgMIME, Data: img}})
	}
	segmentParts = append(segmentParts, genai.NewPartFromText(prompt))
	resp, usage, err := c.generateContent(exactOffsetContext, model, []*genai.Content{{
		Role:  genai.RoleUser,
		Parts: segmentParts,
	}}, genConfig)
	if err != nil {
		return "", usage, fmt.Errorf("failed to analyze segment: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", usage, fmt.Errorf("no content from segment analysis")
	}

	result := extractCandidateText(resp)

	c.logger.Info("Segment analyzed",
		zap.Duration("start", start),
		zap.Duration("end", end),
		zap.Int("response_length", len(result)))

	return result, usage, nil
}

// AnalyzeFrameGrids performs deep analysis on one or more stitched frame-grid
// images (contact sheets) using the default model.
func (c *Client) AnalyzeFrameGrids(ctx context.Context, images [][]byte, mimeType, prompt string) (string, *TokenUsage, error) {
	return c.AnalyzeFrameGridsWithModel(ctx, images, mimeType, prompt, c.model)
}

// AnalyzeFrameGridsWithModel performs deep analysis on one or more stitched
// frame-grid images using a specified model (e.g. ModelArgon4 or ModelFlash38)
// with MediaResolutionHigh so burned-in timestamps and pose details remain legible.
func (c *Client) AnalyzeFrameGridsWithModel(ctx context.Context, images [][]byte, mimeType, prompt, model string) (string, *TokenUsage, error) {
	if model == "" {
		model = c.model
	}
	if len(images) == 0 {
		return "", nil, fmt.Errorf("at least one frame grid image is required")
	}

	parts := make([]*genai.Part, 0, len(images)+1)
	totalBytes := 0
	for i, imgBytes := range images {
		if len(imgBytes) == 0 {
			return "", nil, fmt.Errorf("frame grid image at index %d is empty", i)
		}
		partMIME := strings.TrimSpace(mimeType)
		if partMIME == "" {
			partMIME = DetectImageMIME(imgBytes)
		}
		if partMIME == "" {
			partMIME = "image/jpeg"
		}
		parts = append(parts, &genai.Part{
			InlineData: &genai.Blob{
				MIMEType: partMIME,
				Data:     imgBytes,
			},
		})
		totalBytes += len(imgBytes)
	}
	parts = append(parts, genai.NewPartFromText(prompt))

	c.logger.Info("Analyzing frame grids",
		zap.String("model", model),
		zap.Int("image_count", len(images)),
		zap.Int("total_image_bytes", totalBytes))

	genConfig := &genai.GenerateContentConfig{
		MediaResolution: genai.MediaResolutionHigh,
	}
	thinkingLevel := thinkingLevelFromContext(ctx, c.thinkingLevel)
	if strings.TrimSpace(thinkingLevel) == "" {
		thinkingLevel = "HIGH"
	}
	if tc := c.thinkingConfigForModelAndLevel(model, thinkingLevel); tc != nil {
		genConfig.ThinkingConfig = tc
	}
	resp, usage, err := c.generateContent(ctx, model, []*genai.Content{{
		Role:  genai.RoleUser,
		Parts: parts,
	}}, genConfig)
	if err != nil {
		return "", usage, fmt.Errorf("failed to analyze frame grids: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", usage, fmt.Errorf("no content from frame grid analysis")
	}

	result := extractCandidateText(resp)

	c.logger.Info("Frame grids analyzed",
		zap.Int("image_count", len(images)),
		zap.Int("response_length", len(result)))

	return result, usage, nil
}

// QueryVideoFlash sends a prompt against an already-uploaded video using the
// lightweight Flash model. Ideal for simple verification tasks (e.g. confirming
// whether a movement is visible at a given timestamp) where Pro-level depth
// is unnecessary.
func (c *Client) QueryVideoFlash(ctx context.Context, fileURI, mimeType, prompt string) (string, *TokenUsage, error) {
	c.logger.Info("Querying video with Flash",
		zap.String("file_uri", fileURI),
		zap.String("model", flashModel))

	resp, usage, err := c.generateContent(ctx, flashModel, []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{FileData: &genai.FileData{FileURI: fileURI, MIMEType: mimeType}},
			genai.NewPartFromText(prompt),
		},
	}}, &genai.GenerateContentConfig{
		MediaResolution: genai.MediaResolutionHigh,
	})
	if err != nil {
		return "", usage, fmt.Errorf("flash query failed: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", usage, fmt.Errorf("no content from flash query")
	}

	result := extractCandidateText(resp)

	c.logger.Info("Flash query completed", zap.Int("response_length", len(result)))
	return result, usage, nil
}

const ttsModel = ModelFlashTTS31Preview

// GenerateSpeech converts text to speech using the Gemini TTS model and writes
// the output as a WAV file (24kHz, 16-bit, mono PCM) to outputPath.
//
// voiceName selects one of the 30 prebuilt voices (e.g. "Kore", "Aoede", "Charon").
// The model auto-detects the input language, so Korean text will produce Korean speech.
//
// NOTE (cost): Each call consumes TTS quota. Chunk feedback is typically 1-2 sentences
// (~100 chars), so per-video cost should be modest. Monitor usage if scaling up.
func (c *Client) GenerateSpeech(ctx context.Context, text, voiceName, outputPath string) (*TokenUsage, error) {
	c.logger.Info("Generating speech via TTS",
		zap.String("model", ttsModel),
		zap.String("voice", voiceName),
		zap.Int("text_length", len(text)))

	resp, usage, err := c.generateContent(ctx, ttsModel, genai.Text(text), &genai.GenerateContentConfig{
		ResponseModalities: []string{"AUDIO"},
		SpeechConfig: &genai.SpeechConfig{
			LanguageCode: "ko-KR",
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{
					VoiceName: voiceName,
				},
			},
		},
	})
	if err != nil {
		return usage, fmt.Errorf("TTS generate_content failed: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil {
		return usage, fmt.Errorf("TTS returned no candidates")
	}

	// Extract raw PCM audio data from inline response
	var pcmData []byte
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.InlineData != nil && len(part.InlineData.Data) > 0 {
			pcmData = part.InlineData.Data
			break
		}
	}

	if len(pcmData) == 0 {
		return usage, fmt.Errorf("TTS returned no audio data")
	}

	// Write as WAV (PCM 16-bit LE, 24kHz, mono) for FFmpeg compatibility
	if err := writePCMAsWAV(outputPath, pcmData, 24000, 1, 16); err != nil {
		return usage, fmt.Errorf("failed to write TTS WAV: %w", err)
	}

	c.logger.Info("TTS audio written",
		zap.String("path", outputPath),
		zap.Int("pcm_bytes", len(pcmData)))
	return usage, nil
}

// writePCMAsWAV wraps raw PCM data in a standard RIFF WAV header.
func writePCMAsWAV(path string, pcm []byte, sampleRate, channels, bitsPerSample int) error {
	dataLen := len(pcm)
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// RIFF header
	f.Write([]byte("RIFF"))
	writeLE32(f, uint32(36+dataLen)) // file size - 8
	f.Write([]byte("WAVE"))

	// fmt sub-chunk
	f.Write([]byte("fmt "))
	writeLE32(f, 16)                    // sub-chunk size
	writeLE16(f, 1)                     // PCM format
	writeLE16(f, uint16(channels))      // channels
	writeLE32(f, uint32(sampleRate))    // sample rate
	writeLE32(f, uint32(byteRate))      // byte rate
	writeLE16(f, uint16(blockAlign))    // block align
	writeLE16(f, uint16(bitsPerSample)) // bits per sample

	// data sub-chunk
	f.Write([]byte("data"))
	writeLE32(f, uint32(dataLen))
	_, err = f.Write(pcm)
	return err
}

// writeLE32 writes a uint32 in little-endian byte order.
func writeLE32(f *os.File, v uint32) {
	b := make([]byte, 4)
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	f.Write(b)
}

// writeLE16 writes a uint16 in little-endian byte order.
func writeLE16(f *os.File, v uint16) {
	b := make([]byte, 2)
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	f.Write(b)
}

// ParseImage sends an image inline (no file upload) to the Flash model and
// returns the raw text response. Ideal for lightweight OCR tasks like reading
// a gym whiteboard photo.
//
// imageBytes should already be normalized (resized/compressed) by NormalizeImage.
func (c *Client) ParseImage(ctx context.Context, imageBytes []byte, mimeType string, prompt string) (string, *TokenUsage, error) {
	c.logger.Info("Parsing image with Flash",
		zap.String("model", flashModel),
		zap.Int("image_bytes", len(imageBytes)),
		zap.String("mime_type", mimeType))

	resp, usage, err := c.generateContent(ctx, flashModel, []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{InlineData: &genai.Blob{MIMEType: mimeType, Data: imageBytes}},
			genai.NewPartFromText(prompt),
		},
	}}, nil)
	if err != nil {
		return "", usage, fmt.Errorf("failed to parse image: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", usage, fmt.Errorf("no content from image parsing")
	}

	result := extractCandidateText(resp)

	c.logger.Info("Image parsed", zap.Int("response_length", len(result)))
	return result, usage, nil
}

// ParseText sends a text prompt to the Flash model and returns the raw text response.
func (c *Client) ParseText(ctx context.Context, prompt string) (string, *TokenUsage, error) {
	c.logger.Info("Parsing text with Flash",
		zap.String("model", flashModel),
		zap.Int("prompt_length", len(prompt)))

	resp, usage, err := c.generateContent(ctx, flashModel, []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			genai.NewPartFromText(prompt),
		},
	}}, nil)
	if err != nil {
		return "", usage, fmt.Errorf("failed to parse text: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", usage, fmt.Errorf("no content from text parsing")
	}

	result := extractCandidateText(resp)

	c.logger.Info("Text parsed", zap.Int("response_length", len(result)))
	return result, usage, nil
}

// BatchSegmentRequest describes a single video segment analysis item in a Gemini Batch API job.
type BatchSegmentRequest struct {
	FileURI  string
	MIMEType string
	Start    time.Duration
	End      time.Duration
	Prompt   string
	Metadata map[string]string
}

// BatchSegmentResult holds the output and token usage for one item in an AnalyzeSegmentsBatch job.
type BatchSegmentResult struct {
	Analysis string
	Usage    *TokenUsage
	Err      error
}

// AnalyzeSegmentsBatch submits multiple video segment requests in a single Gemini Batch API job
// (InlinedRequests) for a 50% token cost discount and polls until completion.
func (c *Client) AnalyzeSegmentsBatch(ctx context.Context, model string, requests []BatchSegmentRequest) ([]BatchSegmentResult, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	if model == "" {
		model = c.model
	}
	fps := float64(5)
	thinkingLevel := thinkingLevelFromContext(ctx, c.thinkingLevel)
	if strings.TrimSpace(thinkingLevel) == "" {
		thinkingLevel = "HIGH"
	}
	genConfig := &genai.GenerateContentConfig{
		MediaResolution: genai.MediaResolution(genai.MediaResolutionMedium),
	}
	if tc := c.thinkingConfigForModelAndLevel(model, thinkingLevel); tc != nil {
		genConfig.ThinkingConfig = tc
	}
	if strings.EqualFold(strings.TrimSpace(model), ModelArgon4) {
		genConfig.MaxOutputTokens = argonMaxOutputTokens
		if genConfig.ThinkingConfig != nil {
			thinking := *genConfig.ThinkingConfig
			thinking.ThinkingBudget = nil
			genConfig.ThinkingConfig = &thinking
		}
	}

	inlined := make([]*genai.InlinedRequest, 0, len(requests))
	offsets := make([]exactSegmentOffsets, 0, len(requests))
	for i, req := range requests {
		if req.Start < 0 || req.End <= req.Start {
			return nil, fmt.Errorf("invalid batch segment interval at index %d: start=%s end=%s", i, req.Start, req.End)
		}
		videoPart := &genai.Part{
			FileData: &genai.FileData{
				FileURI:  req.FileURI,
				MIMEType: req.MIMEType,
			},
			VideoMetadata: &genai.VideoMetadata{
				StartOffset: req.Start,
				EndOffset:   req.End,
				FPS:         &fps,
			},
		}
		cfgCopy := *genConfig
		inlined = append(inlined, &genai.InlinedRequest{
			Model: model,
			Contents: []*genai.Content{{
				Role: genai.RoleUser,
				Parts: []*genai.Part{
					videoPart,
					genai.NewPartFromText(req.Prompt),
				},
			}},
			Config:   &cfgCopy,
			Metadata: req.Metadata,
		})
		offsets = append(offsets, exactSegmentOffsets{start: req.Start, end: req.End})
	}

	batchCtx := context.WithValue(ctx, exactBatchSegmentOffsetsContextKey{}, offsets)
	job, err := c.client.Batches.Create(batchCtx, model, &genai.BatchJobSource{
		InlinedRequests: inlined,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("create batch segment job: %w", err)
	}
	if job == nil || strings.TrimSpace(job.Name) == "" {
		return nil, errors.New("batch segment job returned empty job name")
	}

	consecutiveErrors := 0
	for {
		if job.State == genai.JobStateSucceeded {
			break
		}
		if job.State == genai.JobStateFailed || job.State == genai.JobStateCancelled || job.State == genai.JobStateExpired {
			if job.Error != nil && job.Error.Message != "" {
				return nil, fmt.Errorf("batch segment job %s ended with %s: %s", job.Name, job.State, job.Error.Message)
			}
			return nil, fmt.Errorf("batch segment job %s ended with state %s", job.Name, job.State)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c.sleep(c.pollInterval)
		polled, getErr := c.client.Batches.Get(ctx, job.Name, nil)
		if getErr != nil {
			if isRetryableFileGetError(getErr) && consecutiveErrors < maxConsecutiveFileGetErrors {
				consecutiveErrors++
				c.sleep(c.pollInterval * time.Duration(consecutiveErrors))
				continue
			}
			return nil, fmt.Errorf("poll batch segment job %s: %w", job.Name, getErr)
		}
		consecutiveErrors = 0
		job = polled
	}

	if job.Dest == nil || len(job.Dest.InlinedResponses) != len(requests) {
		got := 0
		if job.Dest != nil {
			got = len(job.Dest.InlinedResponses)
		}
		return nil, fmt.Errorf("batch segment job %s returned %d responses for %d requests", job.Name, got, len(requests))
	}

	results := make([]BatchSegmentResult, len(requests))
	for i, item := range job.Dest.InlinedResponses {
		if item == nil {
			results[i] = BatchSegmentResult{Err: errors.New("nil batch inlined response")}
			continue
		}
		if item.Error != nil && ((item.Error.Code != nil && *item.Error.Code != 0) || item.Error.Message != "") {
			code := int32(0)
			if item.Error.Code != nil {
				code = *item.Error.Code
			}
			results[i] = BatchSegmentResult{
				Err: fmt.Errorf("batch item %d failed (%d): %s", i, code, item.Error.Message),
			}
			continue
		}
		resp := item.Response
		thoughts := extractCandidateThoughts(resp)
		var usage *TokenUsage
		if resp != nil && resp.UsageMetadata != nil {
			input := int64(resp.UsageMetadata.PromptTokenCount)
			output := int64(resp.UsageMetadata.CandidatesTokenCount)
			total := int64(resp.UsageMetadata.TotalTokenCount)
			details := &ComparisonUsage{
				Input:  &input,
				Output: &output,
				Total:  &total,
			}
			if resp.UsageMetadata.ThoughtsTokenCount > 0 {
				tCount := int64(resp.UsageMetadata.ThoughtsTokenCount)
				details.Thinking = &tCount
			}
			if resp.UsageMetadata.CachedContentTokenCount > 0 {
				cached := int64(resp.UsageMetadata.CachedContentTokenCount)
				details.Cached = &cached
			}
			if resp.UsageMetadata.ToolUsePromptTokenCount > 0 {
				toolUse := int64(resp.UsageMetadata.ToolUsePromptTokenCount)
				details.ToolUse = &toolUse
			}
			usage = UsageFromComparisonWithThoughts(details, model, thoughts)
		} else if thoughts != "" {
			usage = UsageFromComparisonWithThoughts(nil, model, thoughts)
		}
		if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
			results[i] = BatchSegmentResult{Usage: usage, Err: errors.New("no content from batch segment analysis")}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(model), ModelArgon4) && resp.Candidates[0].FinishReason == genai.FinishReasonMaxTokens {
			results[i] = BatchSegmentResult{Usage: usage, Err: errors.New("Argon output is incomplete: MAX_TOKENS")}
			continue
		}
		results[i] = BatchSegmentResult{
			Analysis: extractCandidateText(resp),
			Usage:    usage,
		}
	}
	return results, nil
}

