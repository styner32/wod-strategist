package testhelpers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"

	"github.com/hibiken/asynq"
	g "github.com/onsi/gomega"
	"google.golang.org/api/option"

	"github.com/wod-strategist/api/internal/storage"
)

// NewStorageClient returns a real storage.Client wired through the given
// http.RoundTripper. Pass a *MockTransport to intercept and verify GCS requests
// without making actual network calls.
//
//	transport := testhelpers.NewMockTransport()
//	// register expectations…
//	client, err := testhelpers.NewStorageClient("my-bucket", transport)
func NewStorageClient(bucketName string, transport http.RoundTripper) (*storage.Client, error) {
	return storage.NewClient(
		context.Background(),
		bucketName,
		option.WithHTTPClient(&http.Client{Transport: transport}),
		option.WithoutAuthentication(),
	)
}

// NewStorageClientWithSigning returns a real storage.Client with test signing
// credentials injected, allowing GenerateSignedURL to work without real GCP auth.
// Use this in controller tests that exercise the upload-url endpoint.
func NewStorageClientWithSigning(bucketName string, transport http.RoundTripper) (*storage.Client, error) {
	client, err := NewStorageClient(bucketName, transport)
	if err != nil {
		return nil, err
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate test RSA key: %w", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	client.SetSigningCredentials("test@test.iam.gserviceaccount.com", pemKey)
	return client, nil
}

// ---------------------------------------------------------------------------
// Queue (asynq / Redis)
// ---------------------------------------------------------------------------

const (
	// testRedisDB is the Redis DB index used exclusively for tests.
	// Using a dedicated DB avoids interfering with the application's DB 5.
	testRedisDB = 15
)

func testRedisAddr() string {
	if addr := os.Getenv("TEST_REDIS_URL"); addr != "" {
		return addr
	}
	return "localhost:6379"
}

// NewQueueClient returns a real asynq.Client connected to the local test Redis.
// The environment variable TEST_REDIS_URL can override the default address.
func NewQueueClient() *asynq.Client {
	return asynq.NewClient(asynq.RedisClientOpt{
		Addr: testRedisAddr(),
		DB:   testRedisDB,
	})
}

// NewQueueInspector returns an asynq.Inspector for the same test Redis DB,
// allowing tests to inspect what was enqueued.
func NewQueueInspector() *asynq.Inspector {
	return asynq.NewInspector(asynq.RedisClientOpt{
		Addr: testRedisAddr(),
		DB:   testRedisDB,
	})
}

// CleanupQueue deletes all tasks from all queues in the test Redis DB.
// Call this in BeforeEach to ensure each test starts with a clean queue.
func CleanupQueue(inspector *asynq.Inspector) {
	queues, err := inspector.Queues()
	g.Expect(err).NotTo(g.HaveOccurred(), "CleanupQueue: failed to list queues")
	for _, q := range queues {
		// DeleteAllPendingTasks, DeleteAllScheduledTasks, etc.
		_, _ = inspector.DeleteAllPendingTasks(q)
		_, _ = inspector.DeleteAllScheduledTasks(q)
		_, _ = inspector.DeleteAllRetryTasks(q)
		_, _ = inspector.DeleteAllArchivedTasks(q)
		_, _ = inspector.DeleteAllCompletedTasks(q)
	}
}
