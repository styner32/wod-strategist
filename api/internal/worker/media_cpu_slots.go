package worker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

type mediaExecutionLane string

const (
	mediaLaneLiveChunk mediaExecutionLane = "live_chunk"
	mediaLaneShared    mediaExecutionLane = "shared"
)

type (
	mediaLaneContextKey     struct{}
	heldMediaLaneContextKey struct{}
	mediaRuntimeContextKey  struct{}
	mediaWaitTrackerKey     struct{}
)

type mediaCPULimiter struct {
	liveChunkSlot   chan struct{}
	sharedMediaSlot chan struct{}
}

func newMediaCPULimiter() *mediaCPULimiter {
	return &mediaCPULimiter{
		liveChunkSlot:   make(chan struct{}, 1),
		sharedMediaSlot: make(chan struct{}, 1),
	}
}

var defaultMediaCPULimiter = newMediaCPULimiter()

func (w *Worker) effectiveMediaLimiter() *mediaCPULimiter {
	if w != nil && w.mediaLimiter != nil {
		return w.mediaLimiter
	}
	return defaultMediaCPULimiter
}

type mediaRuntimeConfig struct {
	limiter *mediaCPULimiter
	enabled bool
	logger  *zap.Logger
}

func withWorkerMediaRuntime(ctx context.Context, w *Worker) context.Context {
	cfg := mediaRuntimeConfig{
		limiter: w.effectiveMediaLimiter(),
		enabled: w.mediaCPUSlotsEnabled(),
	}
	if w != nil {
		cfg.logger = w.logger
	}
	out := context.WithValue(ctx, mediaRuntimeContextKey{}, cfg)
	if cfg.enabled {
		out = withLimitedMediaThreads(out)
	}
	return out
}

func withMediaExecutionLane(ctx context.Context, lane mediaExecutionLane) context.Context {
	return context.WithValue(ctx, mediaLaneContextKey{}, lane)
}

func mediaExecutionLaneFromContext(ctx context.Context) mediaExecutionLane {
	if lane, ok := ctx.Value(mediaLaneContextKey{}).(mediaExecutionLane); ok && lane != "" {
		return lane
	}
	return mediaLaneShared
}

type mediaSlotWaitTracker struct {
	mu    sync.Mutex
	total time.Duration
	count int
}

func withMediaSlotWaitTracker(ctx context.Context) (context.Context, *mediaSlotWaitTracker) {
	tracker := &mediaSlotWaitTracker{}
	return context.WithValue(ctx, mediaWaitTrackerKey{}, tracker), tracker
}

func (t *mediaSlotWaitTracker) add(d time.Duration) {
	if t == nil || d <= 0 {
		return
	}
	t.mu.Lock()
	t.total += d
	t.count++
	t.mu.Unlock()
}

func (t *mediaSlotWaitTracker) WaitDuration() time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}

func (t *mediaSlotWaitTracker) WaitCount() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.count
}

// runMediaExec acquires the lane's CPU execution slot strictly for the duration
// of the underlying media command (ffmpeg/ffprobe) and releases it immediately
// afterward so AI/GCS network waits never hold a CPU slot.
func runMediaExec(ctx context.Context, stage string, fn func(execCtx context.Context) error) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	runtimeCfg := mediaRuntimeConfig{
		limiter: defaultMediaCPULimiter,
		enabled: true,
	}
	if stored, ok := ctx.Value(mediaRuntimeContextKey{}).(mediaRuntimeConfig); ok {
		runtimeCfg = stored
		if runtimeCfg.limiter == nil {
			runtimeCfg.limiter = defaultMediaCPULimiter
		}
	}
	if !runtimeCfg.enabled {
		return 0, fn(ctx)
	}

	lane := mediaExecutionLaneFromContext(ctx)
	if held, ok := ctx.Value(heldMediaLaneContextKey{}).(mediaExecutionLane); ok && held == lane {
		return 0, fn(withLimitedMediaThreads(ctx))
	}

	slot := runtimeCfg.limiter.sharedMediaSlot
	if lane == mediaLaneLiveChunk {
		slot = runtimeCfg.limiter.liveChunkSlot
	}

	waitStart := time.Now()
	select {
	case slot <- struct{}{}:
	case <-ctx.Done():
		return time.Since(waitStart), ctx.Err()
	}
	waitDur := time.Since(waitStart)
	defer func() { <-slot }()

	if tracker, ok := ctx.Value(mediaWaitTrackerKey{}).(*mediaSlotWaitTracker); ok {
		tracker.add(waitDur)
	}
	if runtimeCfg.logger != nil && waitDur >= 25*time.Millisecond {
		runtimeCfg.logger.Info("Media CPU slot acquired after wait",
			zap.String("stage", stage),
			zap.String("lane", string(lane)),
			zap.Int64("media_cpu_slot_wait_ms", waitDur.Milliseconds()),
		)
	}

	execCtx := withLimitedMediaThreads(context.WithValue(ctx, heldMediaLaneContextKey{}, lane))
	return waitDur, fn(execCtx)
}
