package testhelpers

import (
	"context"
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
