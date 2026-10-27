package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type replayClient struct {
	base  string
	token string
	http  *http.Client
}

type httpStatusError struct {
	Method, Path string
	Status       int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("%s %s returned HTTP %d", e.Method, e.Path, e.Status)
}

func newReplayClient(base, token string, client *http.Client) (*replayClient, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("LIVE_TEST_API_URL (or EXPO_PUBLIC_API_URL) must be an HTTP API URL including /api/v1")
	}
	if !strings.HasSuffix(u.Path, "/api/v1") {
		return nil, fmt.Errorf("API URL must end in /api/v1")
	}
	if client == nil {
		return nil, fmt.Errorf("HTTP client is required")
	}
	return &replayClient{base: u.String(), token: token, http: client}, nil
}

func (c *replayClient) json(ctx context.Context, method, path string, body, result any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create %s %s request", method, path)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s request failed: %s", method, path, requestFailure(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpStatusError{method, path, resp.StatusCode}
	}
	if result == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 32<<20))
	decoder.UseNumber()
	if err = decoder.Decode(result); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return nil
}

// Do not include signed URLs or credentials in errors/reports.
func requestFailure(err error) string {
	if uerr, ok := err.(*url.Error); ok {
		return uerr.Err.Error()
	}
	return err.Error()
}

func (c *replayClient) put(ctx context.Context, signedURL string, data []byte, headers map[string]string) error {
	u, err := url.Parse(signedURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return fmt.Errorf("invalid signed upload URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, signedURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create signed PUT request")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("signed PUT failed: %s", requestFailure(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("signed PUT returned HTTP %d", resp.StatusCode)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}
