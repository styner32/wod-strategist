package gemini

import (
	"bytes"
	"context"
	"encoding/json"
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
}

// extractTokenUsage converts the SDK's UsageMetadata into our TokenUsage struct.
func extractTokenUsage(resp *genai.GenerateContentResponse, model string) *TokenUsage {
	if resp == nil || resp.UsageMetadata == nil {
		return nil
	}
	return &TokenUsage{
		PromptTokens:    resp.UsageMetadata.PromptTokenCount,
		CandidateTokens: resp.UsageMetadata.CandidatesTokenCount,
		TotalTokens:     resp.UsageMetadata.TotalTokenCount,
		Model:           model,
	}
}

const (
	ModelPro31Preview      = "gemini-3.1-pro-preview"
	ModelFlash35Lite       = "gemini-3.5-flash-lite"
	ModelFlash36           = "gemini-3.6-flash"
	ModelFlashTTS31Preview = "gemini-3.1-flash-tts-preview"

	// Retained for backward compatibility
	ModelFlash30Preview = ModelFlash35Lite
	ModelFlash35        = ModelFlash36
)

const defaultModel = ModelPro31Preview

type Client struct {
	client       *genai.Client
	logger       *zap.Logger
	model        string
	pollInterval time.Duration
	sleep        func(time.Duration)
}

// Model returns the configured generation model used by AnalyzeSegment.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

type Options struct {
	APIKey       string
	BaseURL      string
	APIVersion   string
	HTTPClient   *http.Client
	Model        string // e.g. "gemini-3.1-pro-preview", "gemini-3.5-flash-lite", "gemini-3.6-flash"
	PollInterval time.Duration
	Sleep        func(time.Duration)
}

type exactSegmentOffsets struct {
	start time.Duration
	end   time.Duration
}

type exactSegmentOffsetsContextKey struct{}

// exactSegmentOffsetsTransport compensates for google.golang.org/genai's
// VideoMetadata marshaler rounding offsets to whole seconds. Gemini accepts
// protobuf Duration JSON with fractional seconds, so restore the exact
// persisted interval immediately before the request is sent.
type exactSegmentOffsetsTransport struct {
	base http.RoundTripper
}

func (t exactSegmentOffsetsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	offsets, ok := req.Context().Value(exactSegmentOffsetsContextKey{}).(exactSegmentOffsets)
	if !ok || req.Body == nil {
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
	if !replaceVideoMetadataOffsets(payload, offsets) {
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
	wrappedHTTPClient.Transport = exactSegmentOffsetsTransport{base: baseTransport}

	config := &genai.ClientConfig{
		APIKey:     apiKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: &wrappedHTTPClient,
	}
	if options.BaseURL != "" || options.APIVersion != "" {
		config.HTTPOptions = genai.HTTPOptions{
			BaseURL:    options.BaseURL,
			APIVersion: options.APIVersion,
		}
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

	return &Client{
		client:       client,
		logger:       logger,
		model:        model,
		pollInterval: pollInterval,
		sleep:        sleep,
	}, nil
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
func (c *Client) AnalyzeVideoWithModel(ctx context.Context, filePath string, prompt string, model string) (string, string, *TokenUsage, error) {
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
	for {
		file, err := c.client.Files.Get(ctx, uploadResult.Name, nil)
		if err != nil {
			return "", uploadResult.Name, nil, fmt.Errorf("failed to get file info: %w", err)
		}

		if file.State == genai.FileStateActive {
			break
		}
		if file.State == genai.FileStateFailed {
			return "", uploadResult.Name, nil, fmt.Errorf("file processing failed")
		}

		c.sleep(c.pollInterval)
	}

	c.logger.Info("File uploaded", zap.Any("file", uploadResult), zap.String("mime_type", mimeType))

	// Generate content — single multimodal turn with video first for better temporal grounding
	resp, err := c.client.Models.GenerateContent(ctx, model, []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{FileData: &genai.FileData{FileURI: uploadResult.URI, MIMEType: mimeType}},
			{Text: prompt},
		},
	}}, nil)
	if err != nil {
		return "", uploadResult.Name, nil, fmt.Errorf("failed to generate content: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", uploadResult.Name, nil, fmt.Errorf("no content generated")
	}

	c.logger.Info("Gemini response", zap.Any("response", resp))

	usage := extractTokenUsage(resp, model)

	// Extract text from response
	var result string
	for _, part := range resp.Candidates[0].Content.Parts {
		result += part.Text
	}

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

// GenerateWorkoutMusic generates a music clip using the Lyria 3 model and writes
// the resulting MP3 audio to outputPath.
// Use "lyria-3-clip-preview" for a 30-second clip or "lyria-3-pro-preview" for
// a full-length song (2-3 minutes).
func (c *Client) GenerateWorkoutMusic(ctx context.Context, model, prompt, outputPath string) error {
	c.logger.Info("Generating workout music",
		zap.String("model", model),
		zap.String("prompt", prompt),
		zap.String("output", outputPath))

	resp, err := c.client.Models.GenerateContent(ctx, model, genai.Text(prompt), &genai.GenerateContentConfig{
		ResponseModalities: []string{"AUDIO", "TEXT"},
	})
	if err != nil {
		return fmt.Errorf("lyria generate_content failed: %w", err)
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return fmt.Errorf("lyria returned no candidates")
	}

	var audioData []byte
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.InlineData != nil && len(part.InlineData.Data) > 0 {
			audioData = part.InlineData.Data
			break
		}
	}

	if len(audioData) == 0 {
		return fmt.Errorf("lyria returned no audio data in part.InlineData")
	}

	if err := os.WriteFile(outputPath, audioData, 0o644); err != nil {
		return fmt.Errorf("failed to write music to %s: %w", outputPath, err)
	}

	c.logger.Info("Workout music written", zap.String("path", outputPath), zap.Int("bytes", len(audioData)))
	return nil
}

const flashModel = ModelFlash36

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

	c.logger.Info("File uploaded, polling for ACTIVE",
		zap.String("file_name", uploadResult.Name),
		zap.String("file_uri", uploadResult.URI),
		zap.Duration("upload_duration", time.Since(uploadStart)),
		zap.Int64("file_size_bytes", fileSizeBytes))

	var videoDuration time.Duration
	for {
		file, err := c.client.Files.Get(ctx, uploadResult.Name, nil)
		if err != nil {
			return &UploadResult{FileName: uploadResult.Name},
				fmt.Errorf("failed to get file info: %w", err)
		}

		if file.State == genai.FileStateActive {
			if file.VideoMetadata != nil {
				if durStr, ok := file.VideoMetadata["videoDuration"].(string); ok {
					if d, parseErr := time.ParseDuration(durStr); parseErr == nil {
						videoDuration = d
					}
				}
			}
			break
		}
		if file.State == genai.FileStateFailed {
			return &UploadResult{FileName: uploadResult.Name},
				fmt.Errorf("file processing failed")
		}

		c.sleep(c.pollInterval)
	}

	c.logger.Info("File is ACTIVE",
		zap.String("file_name", uploadResult.Name),
		zap.String("file_uri", uploadResult.URI),
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

	resp, err := c.client.Models.GenerateContent(ctx, flashModel, []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{FileData: &genai.FileData{FileURI: fileURI, MIMEType: mimeType}},
			genai.NewPartFromText(prompt),
		},
	}}, &genai.GenerateContentConfig{
		MediaResolution: genai.MediaResolutionHigh,
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to index video: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", nil, fmt.Errorf("no content from video indexing")
	}

	usage := extractTokenUsage(resp, flashModel)

	var result string
	for _, part := range resp.Candidates[0].Content.Parts {
		result += part.Text
	}

	c.logger.Info("Video indexed", zap.Int("response_length", len(result)))
	return result, usage, nil
}

// AnalyzeSegment performs deep biomechanical analysis on a specific time range
// of the video using the Pro model with VideoMetadata to constrain attention.
func (c *Client) AnalyzeSegment(ctx context.Context, fileURI, mimeType string, start, end time.Duration, prompt string) (string, *TokenUsage, error) {
	if start < 0 || end <= start {
		return "", nil, fmt.Errorf("invalid segment interval: start=%s end=%s", start, end)
	}
	fps := float64(5)

	c.logger.Info("Analyzing segment",
		zap.String("file_uri", fileURI),
		zap.String("model", c.model),
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
	resp, err := c.client.Models.GenerateContent(exactOffsetContext, c.model, []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			videoPart,
			genai.NewPartFromText(prompt),
		},
	}}, &genai.GenerateContentConfig{
		MediaResolution: genai.MediaResolution(genai.MediaResolutionMedium),
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to analyze segment: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", nil, fmt.Errorf("no content from segment analysis")
	}

	usage := extractTokenUsage(resp, c.model)

	var result string
	for _, part := range resp.Candidates[0].Content.Parts {
		result += part.Text
	}

	c.logger.Info("Segment analyzed",
		zap.Duration("start", start),
		zap.Duration("end", end),
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

	resp, err := c.client.Models.GenerateContent(ctx, flashModel, []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{FileData: &genai.FileData{FileURI: fileURI, MIMEType: mimeType}},
			genai.NewPartFromText(prompt),
		},
	}}, &genai.GenerateContentConfig{
		MediaResolution: genai.MediaResolutionHigh,
	})
	if err != nil {
		return "", nil, fmt.Errorf("flash query failed: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", nil, fmt.Errorf("no content from flash query")
	}

	usage := extractTokenUsage(resp, flashModel)

	var result string
	for _, part := range resp.Candidates[0].Content.Parts {
		result += part.Text
	}

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

	resp, err := c.client.Models.GenerateContent(ctx, ttsModel, genai.Text(text), &genai.GenerateContentConfig{
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
		return nil, fmt.Errorf("TTS generate_content failed: %w", err)
	}

	usage := extractTokenUsage(resp, ttsModel)

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
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

	resp, err := c.client.Models.GenerateContent(ctx, flashModel, []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{InlineData: &genai.Blob{MIMEType: mimeType, Data: imageBytes}},
			genai.NewPartFromText(prompt),
		},
	}}, nil)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse image: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", nil, fmt.Errorf("no content from image parsing")
	}

	usage := extractTokenUsage(resp, flashModel)

	var result string
	for _, part := range resp.Candidates[0].Content.Parts {
		result += part.Text
	}

	c.logger.Info("Image parsed", zap.Int("response_length", len(result)))
	return result, usage, nil
}
