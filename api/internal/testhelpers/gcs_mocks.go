package testhelpers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

const GCSBaseURL = "https://storage.googleapis.com"

// MockGCSDownload registers a mock expectation for a DownloadFile call.
// The gcsURI should be in "gs://bucket/object" format (the same format passed
// to storage.Client.DownloadFile). The response body is a 1-byte sentinel by default.
func MockGCSDownload(transport *MockTransport, gcsURI string) {
	MockGCSDownloadWithBody(transport, gcsURI, []byte{0x00})
}

// MockGCSDownloadWithBody registers a mock expectation for a DownloadFile call
// with a specific response body.
func MockGCSDownloadWithBody(transport *MockTransport, gcsURI string, body []byte) {
	bucket, object := mustParseGCSURI(gcsURI)
	transport.New(GCSBaseURL).
		Get(fmt.Sprintf("/%s/%s", bucket, object)).
		Reply(http.StatusOK).
		Body(body)
}

// MockGCSUpload registers a mock expectation for an UploadFromFile call.
// bucketName is the bucket, objectName is the expected object path.
func MockGCSUpload(transport *MockTransport, bucketName, objectName string) {
	// The Go GCS library uses a multipart upload with specific query params.
	// We match on the path prefix; query params are validated loosely.
	transport.New(GCSBaseURL).
		Post(fmt.Sprintf("/upload/storage/v1/b/%s/o", bucketName)).
		Reply(http.StatusOK).
		JSON(map[string]any{"name": objectName})
}

// MockGCSListObjects registers a mock expectation for a ListObjects call.
// bucketName is the bucket, prefix is the query prefix, and objects are the
// object names to return.
func MockGCSListObjects(transport *MockTransport, bucketName, prefix string, objects []string) {
	var items []map[string]any
	for _, obj := range objects {
		items = append(items, map[string]any{"name": obj})
	}

	resp := map[string]any{}
	if len(items) > 0 {
		resp["items"] = items
	}

	respBody, _ := json.Marshal(resp)

	path := fmt.Sprintf("/storage/v1/b/%s/o", bucketName)
	query := url.Values{}
	query.Set("alt", "json")
	query.Set("prettyPrint", "false")
	query.Set("projection", "full")
	query.Set("prefix", prefix)

	transport.New(GCSBaseURL).
		Get(path + "?" + query.Encode()).
		Reply(http.StatusOK).
		Body(respBody).
		Header("Content-Type", "application/json")
}

// mustParseGCSURI parses "gs://bucket/object" and panics on failure.
func mustParseGCSURI(gcsURI string) (bucket, object string) {
	if len(gcsURI) < 5 || gcsURI[:5] != "gs://" {
		panic(fmt.Sprintf("invalid GCS URI: %s", gcsURI))
	}
	rest := gcsURI[5:]
	for i, c := range rest {
		if c == '/' {
			return rest[:i], rest[i+1:]
		}
	}
	panic(fmt.Sprintf("invalid GCS URI format (no object path): %s", gcsURI))
}
