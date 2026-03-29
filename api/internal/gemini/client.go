package gemini

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/genai"
)

type Client struct {
	client       *genai.Client
	logger       *zap.Logger
	pollInterval time.Duration
	sleep        func(time.Duration)
}

type Options struct {
	APIKey       string
	BaseURL      string
	APIVersion   string
	HTTPClient   *http.Client
	PollInterval time.Duration
	Sleep        func(time.Duration)
}

func NewClientWithOptions(ctx context.Context, logger *zap.Logger, options Options) (*Client, error) {
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is not set")
	}

	config := &genai.ClientConfig{
		APIKey:     apiKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: options.HTTPClient,
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
		pollInterval: pollInterval,
		sleep:        sleep,
	}, nil
}

// AnalyzeVideo returns the analysis result and the name of the uploaded file on Gemini
func (c *Client) AnalyzeVideo(ctx context.Context, filePath string, prompt string) (string, string, error) {
	// Upload file
	f, err := os.Open(filePath)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	// Detect MIME type via content sniffing
	buffer := make([]byte, 512)
	n, err := f.Read(buffer)
	if err != nil {
		return "", "", fmt.Errorf("failed to read file header: %w", err)
	}
	mimeType := http.DetectContentType(buffer[:n])

	if _, err := f.Seek(0, 0); err != nil {
		return "", "", fmt.Errorf("failed to reset file pointer: %w", err)
	}

	uploadResult, err := c.client.Files.Upload(ctx, f, &genai.UploadFileConfig{
		MIMEType: mimeType,
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to upload file: %w", err)
	}

	// Poll for file state to be ACTIVE
	for {
		file, err := c.client.Files.Get(ctx, uploadResult.Name, nil)
		if err != nil {
			return "", uploadResult.Name, fmt.Errorf("failed to get file info: %w", err)
		}

		if file.State == genai.FileStateActive {
			break
		}
		if file.State == genai.FileStateFailed {
			return "", uploadResult.Name, fmt.Errorf("file processing failed")
		}

		c.sleep(c.pollInterval)
	}

	c.logger.Info("File uploaded", zap.Any("file", uploadResult), zap.String("mime_type", mimeType))

	// Generate content
	resp, err := c.client.Models.GenerateContent(ctx, "gemini-3.1-pro-preview", []*genai.Content{{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{Text: prompt}},
	}, {
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{FileData: &genai.FileData{FileURI: uploadResult.URI, MIMEType: mimeType}}},
	}}, nil)
	if err != nil {
		return "", uploadResult.Name, fmt.Errorf("failed to generate content: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", uploadResult.Name, fmt.Errorf("no content generated")
	}

	c.logger.Info("Gemini response", zap.Any("response", resp))

	// Extract text from response
	var result string
	for _, part := range resp.Candidates[0].Content.Parts {
		result += part.Text
	}

	return result, uploadResult.Name, nil
}

func (c *Client) DeleteFile(ctx context.Context, name string) error {
	resp, err := c.client.Files.Delete(ctx, name, nil)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	c.logger.Info("File deleted", zap.Any("response", resp))
	return nil
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
