package gemini

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"
	"google.golang.org/genai"
)

type Client struct {
	client *genai.Client
	logger *zap.Logger
}

func NewClient(ctx context.Context, logger *zap.Logger) (*Client, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is not set")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}

	return &Client{client: client, logger: logger}, nil
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

		time.Sleep(2 * time.Second)
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
