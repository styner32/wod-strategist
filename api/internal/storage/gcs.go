package storage

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
)

type Client struct {
	bucketName string
	client     *storage.Client
}

func NewClient(ctx context.Context, bucketName string) (*Client, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &Client{
		bucketName: bucketName,
		client:     client,
	}, nil
}

func (c *Client) GenerateSignedURL(objectName string, method string, expires time.Duration) (string, error) {
	opts := &storage.SignedURLOptions{
		Scheme:  storage.SigningSchemeV4,
		Method:  method,
		Expires: time.Now().Add(expires),
	}
	u, err := c.client.Bucket(c.bucketName).SignedURL(objectName, opts)
	if err != nil {
		return "", fmt.Errorf("Bucket(%q).SignedURL: %v", c.bucketName, err)
	}
	return u, nil
}

func (c *Client) UploadFile(ctx context.Context, file multipart.File, filename string) (string, error) {
	wc := c.client.Bucket(c.bucketName).Object(filename).NewWriter(ctx)
	if _, err := io.Copy(wc, file); err != nil {
		return "", fmt.Errorf("io.Copy: %v", err)
	}
	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("Writer.Close: %v", err)
	}

	// Construct the GCS URI (gs://...) which allows internal access without public internet
	// Or use https://storage.googleapis.com/... if you need HTTP access
	// For worker processing, gs:// is usually fine if using Google SDKs, but let's return standard GCS URI
	return fmt.Sprintf("gs://%s/%s", c.bucketName, filename), nil
}

func (c *Client) DownloadFile(ctx context.Context, gcsURI, destPath string) error {
	bucket, object, err := ParseGCSURI(gcsURI)
	if err != nil {
		return err
	}

	rc, err := c.client.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		return fmt.Errorf("Object(%q).NewReader: %v", object, err)
	}
	defer rc.Close()

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("os.Create: %v", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("io.Copy: %v", err)
	}
	return nil
}

func ParseGCSURI(uri string) (bucket, object string, err error) {
	if !strings.HasPrefix(uri, "gs://") {
		return "", "", fmt.Errorf("invalid GCS URI: %s", uri)
	}
	parts := strings.SplitN(strings.TrimPrefix(uri, "gs://"), "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid GCS URI format: %s", uri)
	}
	return parts[0], parts[1], nil
}
