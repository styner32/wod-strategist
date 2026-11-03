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
	"sync"

	"github.com/hibiken/asynq"
	g "github.com/onsi/gomega"
	"github.com/redis/go-redis/v9"
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

	asynqAllQueuesKey    = "asynq:queues"
	asynqDefaultQueueKey = "default"
)

var (
	redisMu             sync.Mutex
	testRedisClient     *redis.Client
	testRedisClientAddr string
)

func testRedisAddr() string {
	loadTestEnv()

	if addr := os.Getenv("TEST_REDIS_URL"); addr != "" {
		return addr
	}
	if addr := os.Getenv("REDIS_URL"); addr != "" {
		return addr
	}
	if port := os.Getenv("REDIS_PORT"); port != "" {
		return "localhost:" + port
	}
	return "localhost:6379"
}

func testRedisClientOpt() asynq.RedisClientOpt {
	return asynq.RedisClientOpt{
		Addr: testRedisAddr(),
		DB:   testRedisDB,
	}
}

func getTestRedisClient() *redis.Client {
	opt := testRedisClientOpt()
	redisMu.Lock()
	defer redisMu.Unlock()

	if testRedisClient == nil || testRedisClientAddr != opt.Addr {
		if testRedisClient != nil {
			_ = testRedisClient.Close()
		}
		client, ok := opt.MakeRedisClient().(*redis.Client)
		g.Expect(ok).To(g.BeTrue(), "FlushRedis: expected *redis.Client from asynq.RedisClientOpt")
		testRedisClient = client
		testRedisClientAddr = opt.Addr
	}
	return testRedisClient
}

// NewQueueClient returns a real asynq.Client connected to the local test Redis.
// The environment variable TEST_REDIS_URL can override the default address.
func NewQueueClient() *asynq.Client {
	return asynq.NewClient(testRedisClientOpt())
}

// NewQueueInspector returns an asynq.Inspector for the same test Redis DB,
// allowing tests to inspect what was enqueued.
func NewQueueInspector() *asynq.Inspector {
	return asynq.NewInspector(testRedisClientOpt())
}

// FlushRedis flushes all keys (queues, tasks, asynq.Unique locks, and any other
// locks/state) from the dedicated test Redis DB (DB 15).
// It restores registered queue names in "asynq:queues" (including "default")
// so reused asynq.Client instances (which cache published queue names in memory)
// and inspector.ListPendingTasks("default") continue to work on empty queues.
func FlushRedis() {
	ctx := context.Background()
	client := getTestRedisClient()
	g.Expect(client.Options().DB).To(g.Equal(testRedisDB), "FlushRedis: refusing to flush non-test Redis DB")

	existingQueues, err := client.SMembers(ctx, asynqAllQueuesKey).Result()
	g.Expect(err).NotTo(g.HaveOccurred(), "FlushRedis: failed to read asynq queues")

	err = client.FlushDB(ctx).Err()
	g.Expect(err).NotTo(g.HaveOccurred(), "FlushRedis: failed to flush test Redis DB")

	queuesToRestore := make([]any, 0, len(existingQueues)+1)
	queuesToRestore = append(queuesToRestore, asynqDefaultQueueKey)
	for _, q := range existingQueues {
		if q != asynqDefaultQueueKey {
			queuesToRestore = append(queuesToRestore, q)
		}
	}
	err = client.SAdd(ctx, asynqAllQueuesKey, queuesToRestore...).Err()
	g.Expect(err).NotTo(g.HaveOccurred(), "FlushRedis: failed to restore asynq queue registry")
}

// CleanupQueue flushes the dedicated test Redis DB so all queues, tasks, and
// uniqueness/lock keys are cleared before each test.
func CleanupQueue(_ *asynq.Inspector) {
	FlushRedis()
}

func CleanupQueueWithInspector(inspector *asynq.Inspector) {
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
