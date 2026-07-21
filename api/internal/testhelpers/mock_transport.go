package testhelpers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

type Expectation struct {
	Method string
	URL    *url.URL

	StatusCode int
	RespBody   []byte
	Headers    http.Header

	RequestHeaders http.Header
	BodySubstrings []string

	isMatched      bool
	MismatchReason string
}

type CapturedRequest struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

type MockTransport struct {
	Expectations []*Expectation
	requests     []CapturedRequest
	mutex        sync.Mutex
}

func NewMockTransport() *MockTransport {
	return &MockTransport{
		Expectations: make([]*Expectation, 0),
		requests:     make([]CapturedRequest, 0),
	}
}

func (t *MockTransport) New(baseURL string) *Expectation {
	u, err := url.Parse(baseURL)
	if err != nil {
		panic(fmt.Sprintf("mock transport: invalid base URL provided: %v", err))
	}

	if u.Scheme == "" || u.Host == "" {
		panic(fmt.Sprintf("mock transport: base URL must include scheme and host (e.g. http://%s)", baseURL))
	}

	exp := &Expectation{
		URL:            u,
		Headers:        make(http.Header),
		RequestHeaders: make(http.Header),
	}
	t.Add(exp)
	return exp
}

func (e *Expectation) Get(path string) *Expectation {
	e.Method = http.MethodGet
	e.setPath(path)
	return e
}

func (e *Expectation) Post(path string) *Expectation {
	e.Method = http.MethodPost
	e.setPath(path)
	return e
}

func (e *Expectation) Delete(path string) *Expectation {
	e.Method = http.MethodDelete
	e.setPath(path)
	return e
}

func (e *Expectation) Reply(statusCode int) *Expectation {
	e.StatusCode = statusCode
	return e
}

func (e *Expectation) BodyString(body string) *Expectation {
	e.RespBody = []byte(body)
	return e
}

func (e *Expectation) Body(body []byte) *Expectation {
	e.RespBody = body
	return e
}

func (e *Expectation) JSON(v any) *Expectation {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mock transport: failed to marshal JSON: %v", err))
	}

	e.RespBody = data
	e.Headers.Set("Content-Type", "application/json")
	return e
}

func (e *Expectation) Header(key, value string) *Expectation {
	e.Headers.Set(key, value)
	return e
}

func (e *Expectation) MatchHeader(key, value string) *Expectation {
	e.RequestHeaders.Set(key, value)
	return e
}

// MatchBodyContains requires the request body to contain the given substring.
// JSON bodies escape newlines and quotes, so keep substrings single-line.
func (e *Expectation) MatchBodyContains(substr string) *Expectation {
	e.BodySubstrings = append(e.BodySubstrings, substr)
	return e
}

func (e *Expectation) setPath(path string) {
	u, err := url.Parse(path)
	if err != nil {
		panic(fmt.Sprintf("mock transport: invalid path provided: %v", err))
	}

	if u.Scheme != "" || u.Host != "" {
		e.URL = u
		return
	}

	e.URL.Path = u.Path
	e.URL.RawQuery = u.RawQuery
}

func (t *MockTransport) Add(exp *Expectation) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.Expectations = append(t.Expectations, exp)
}

func (t *MockTransport) Reset() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.Expectations = make([]*Expectation, 0)
	t.requests = make([]CapturedRequest, 0)
}

func (t *MockTransport) Requests() []CapturedRequest {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	out := make([]CapturedRequest, len(t.requests))
	for i, req := range t.requests {
		out[i] = CapturedRequest{
			Method:  req.Method,
			URL:     req.URL,
			Headers: req.Headers.Clone(),
			Body:    append([]byte(nil), req.Body...),
		}
	}

	return out
}

func (t *MockTransport) Verify() error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	var unmatched []string
	for _, exp := range t.Expectations {
		if exp.isMatched {
			continue
		}

		urlString := "<nil>"
		if exp.URL != nil {
			urlString = exp.URL.String()
		}

		if exp.MismatchReason != "" {
			unmatched = append(unmatched, fmt.Sprintf("%s %s (%s)", exp.Method, urlString, exp.MismatchReason))
			continue
		}

		unmatched = append(unmatched, fmt.Sprintf("%s %s", exp.Method, urlString))
	}

	if len(unmatched) > 0 {
		return fmt.Errorf("mock transport: unmatched expectations: %s", strings.Join(unmatched, "; "))
	}

	return nil
}

func (t *MockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("mock transport: read request body: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}

	t.requests = append(t.requests, CapturedRequest{
		Method:  req.Method,
		URL:     req.URL.String(),
		Headers: req.Header.Clone(),
		Body:    body,
	})

	for _, exp := range t.Expectations {
		if !exp.isMatched && t.matches(exp, req, body) {
			exp.isMatched = true
			return t.buildResponse(exp, req), nil
		}
	}

	var reasons []string
	for _, exp := range t.Expectations {
		if exp.MismatchReason != "" {
			reasons = append(reasons, exp.MismatchReason)
		}
	}

	extra := ""
	if len(reasons) > 0 {
		extra = " (" + strings.Join(reasons, "; ") + ")"
	}

	return nil, fmt.Errorf("mock transport: no match found for request %s %s%s", req.Method, req.URL, extra)
}

func (t *MockTransport) matches(exp *Expectation, req *http.Request, body []byte) bool {
	exp.MismatchReason = ""

	if exp.URL == nil {
		exp.MismatchReason = "expected URL is nil"
		return false
	}

	if exp.Method != "" && exp.Method != req.Method {
		exp.MismatchReason = fmt.Sprintf("method mismatch: expected %s got %s", exp.Method, req.Method)
		return false
	}

	if exp.URL.Scheme != req.URL.Scheme {
		exp.MismatchReason = fmt.Sprintf("scheme mismatch: expected %s got %s", exp.URL.Scheme, req.URL.Scheme)
		return false
	}

	if exp.URL.Host != req.URL.Host {
		exp.MismatchReason = fmt.Sprintf("host mismatch: expected %s got %s", exp.URL.Host, req.URL.Host)
		return false
	}

	if exp.URL.Path != req.URL.Path {
		exp.MismatchReason = fmt.Sprintf("path mismatch: expected %s got %s", exp.URL.Path, req.URL.Path)
		return false
	}

	expectedQuery := exp.URL.Query()
	actualQuery := req.URL.Query()

	for key, values := range expectedQuery {
		actualValues, ok := actualQuery[key]
		if !ok {
			exp.MismatchReason = fmt.Sprintf("missing query key %s", key)
			return false
		}

		if len(actualValues) != len(values) {
			exp.MismatchReason = fmt.Sprintf("query value count mismatch for %s: expected %v got %v", key, values, actualValues)
			return false
		}

		for i, value := range values {
			if actualValues[i] != value {
				exp.MismatchReason = fmt.Sprintf("query mismatch for %s: expected %s got %s", key, value, actualValues[i])
				return false
			}
		}
	}

	for _, substr := range exp.BodySubstrings {
		if !bytes.Contains(body, []byte(substr)) {
			exp.MismatchReason = fmt.Sprintf("body does not contain %q", substr)
			return false
		}
	}

	for key, values := range exp.RequestHeaders {
		actualValues := req.Header.Values(key)
		if len(actualValues) == 0 {
			exp.MismatchReason = fmt.Sprintf("missing request header %s", key)
			return false
		}

		if len(actualValues) != len(values) {
			exp.MismatchReason = fmt.Sprintf("header value count mismatch for %s: expected %v got %v", key, values, actualValues)
			return false
		}

		for i, value := range values {
			if actualValues[i] != value {
				exp.MismatchReason = fmt.Sprintf("header mismatch for %s: expected %s got %s", key, value, actualValues[i])
				return false
			}
		}
	}

	return true
}

func (t *MockTransport) buildResponse(exp *Expectation, req *http.Request) *http.Response {
	statusCode := exp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	return &http.Response{
		StatusCode:    statusCode,
		Body:          io.NopCloser(bytes.NewReader(exp.RespBody)),
		Header:        exp.Headers.Clone(),
		Request:       req,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		ContentLength: int64(len(exp.RespBody)),
	}
}

func CreateMockZipArchive(filename string, data []byte) ([]byte, error) {
	buf := new(bytes.Buffer)
	zipWriter := zip.NewWriter(buf)

	f, err := zipWriter.Create(filename)
	if err != nil {
		return nil, err
	}

	if _, err := f.Write(data); err != nil {
		return nil, err
	}

	if err := zipWriter.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
