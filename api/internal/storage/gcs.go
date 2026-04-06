package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"sort"
	"strings"
	"time"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type Client struct {
	bucketName string
	client     *gcs.Client
	signEmail  string
	signKey    []byte
}

type ObjectInfo struct {
	Name    string
	Created time.Time
}

func NewClient(ctx context.Context, bucketName string, opts ...option.ClientOption) (*Client, error) {
	client, err := gcs.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{
		bucketName: bucketName,
		client:     client,
	}, nil
}

// SetSigningCredentials injects signing credentials for GenerateSignedURL.
// In production these remain empty and the GCS library uses ADC.
// In tests, inject a test RSA key so signing works without real GCP auth.
func (c *Client) SetSigningCredentials(email string, key []byte) {
	c.signEmail = email
	c.signKey = key
}

func (c *Client) GenerateSignedURL(objectName string, method string, expires time.Duration) (string, error) {
	opts := &gcs.SignedURLOptions{
		Scheme:  gcs.SigningSchemeV4,
		Method:  method,
		Expires: time.Now().Add(expires),
	}
	if c.signEmail != "" && len(c.signKey) > 0 {
		opts.GoogleAccessID = c.signEmail
		opts.PrivateKey = c.signKey
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

func (c *Client) ListObjectInfos(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	it := c.client.Bucket(c.bucketName).Objects(ctx, &gcs.Query{Prefix: prefix})
	var objects []ObjectInfo
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Bucket(%q).Objects(%q): %w", c.bucketName, prefix, err)
		}
		objects = append(objects, ObjectInfo{
			Name:    attrs.Name,
			Created: attrs.Created,
		})
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Created.Before(objects[j].Created)
	})

	return objects, nil
}

// ListObjects returns the object names under the given prefix in the client's bucket,
// sorted by creation time (oldest first) to guarantee chronological order.
func (c *Client) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	infos, err := c.ListObjectInfos(ctx, prefix)
	if err != nil {
		return nil, err
	}

	objects := make([]string, len(infos))
	for i, info := range infos {
		objects[i] = info.Name
	}
	return objects, nil
}

// UploadFromFile uploads a local file to GCS and returns the gs:// URI.
func (c *Client) UploadFromFile(ctx context.Context, localPath, objectName string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("os.Open: %v", err)
	}
	defer f.Close()

	wc := c.client.Bucket(c.bucketName).Object(objectName).NewWriter(ctx)
	if _, err := io.Copy(wc, f); err != nil {
		return "", fmt.Errorf("io.Copy: %v", err)
	}
	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("Writer.Close: %v", err)
	}
	return fmt.Sprintf("gs://%s/%s", c.bucketName, objectName), nil
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
