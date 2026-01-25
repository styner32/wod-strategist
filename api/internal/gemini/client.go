package gemini

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type Client struct {
	client *genai.Client
}

func NewClient(ctx context.Context) (*Client, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is not set")
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, err
	}

	return &Client{client: client}, nil
}

func (c *Client) Close() {
	c.client.Close()
}

// AnalyzeVideo returns the analysis result and the name of the uploaded file on Gemini
func (c *Client) AnalyzeVideo(ctx context.Context, filePath string, prompt string) (string, string, error) {
	// Upload file
	f, err := os.Open(filePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	uploadResult, err := c.client.UploadFile(ctx, "", f, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to upload file: %w", err)
	}

	// Poll for file state to be ACTIVE
	for {
		file, err := c.client.GetFile(ctx, uploadResult.Name)
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

	// Generate content
	model := c.client.GenerativeModel("gemini-3-pro-preview")
	resp, err := model.GenerateContent(ctx, genai.FileData{URI: uploadResult.URI}, genai.Text(prompt))
	if err != nil {
		return "", uploadResult.Name, fmt.Errorf("failed to generate content: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", uploadResult.Name, fmt.Errorf("no content generated")
	}

	// Extract text from response
	var result string
	for _, part := range resp.Candidates[0].Content.Parts {
		if txt, ok := part.(genai.Text); ok {
			result += string(txt)
		}
	}

	return result, uploadResult.Name, nil
}

func (c *Client) DeleteFile(ctx context.Context, name string) error {
	return c.client.DeleteFile(ctx, name)
}
