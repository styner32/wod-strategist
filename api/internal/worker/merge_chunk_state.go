package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/storage"
)

// Longer than Asynq's default 30-minute task timeout. Live queue entries always
// take precedence over this grace period, including scheduled and retry tasks.
const orphanChunkGrace = 35 * time.Minute

func (w *Worker) resolvePendingChunks(ctx context.Context, p VideoAnalysisPayload, pending []string) error {
	if w.QueueInspector == nil {
		return errors.New("waiting for chunk analysis to complete: queue inspector is not configured")
	}
	missing := make(map[string]bool, len(pending))
	for _, uri := range pending {
		missing[uri] = true
	}
	archived := make(map[string]VideoAnalysisPayload)
	// Read archives first so a concurrent transition from live to archived
	// cannot be mistaken for proof that a live task has finished.
	lists := []func(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error){
		w.QueueInspector.ListArchivedTasks, w.QueueInspector.ListPendingTasks,
		w.QueueInspector.ListScheduledTasks, w.QueueInspector.ListRetryTasks,
		w.QueueInspector.ListActiveTasks,
	}
	for i, list := range lists {
		for page := 1; ; page++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			tasks, err := list("default", asynq.Page(page), asynq.PageSize(100))
			if errors.Is(err, asynq.ErrQueueNotFound) {
				break
			}
			if err != nil {
				return fmt.Errorf("inspect chunk tasks: %w", err)
			}
			for _, task := range tasks {
				if task.Type != TypeChunkAnalysis && task.Type != TypeChunkAnalysisWithSession {
					continue
				}
				var chunk VideoAnalysisPayload
				if err := json.Unmarshal(task.Payload, &chunk); err != nil {
					// An invalid task cannot be attributed to this session. In
					// particular, an old malformed archive must not block every merge.
					continue
				}
				if chunk.SessionID != p.SessionID || chunk.ProfileID != p.ProfileID || !missing[chunk.FilePath] {
					continue
				}
				if i != 0 {
					return fmt.Errorf("waiting for chunk analysis to complete: %s is %s", chunk.FilePath, task.State)
				}
				archived[chunk.FilePath] = chunk
			}
			if len(tasks) < 100 {
				break
			}
		}
	}
	for _, uri := range pending {
		chunk, known := archived[uri]
		if !known {
			requestedAt := p.MergeRequestedAt
			if requestedAt.IsZero() {
				// Old queued merge payloads lack an enqueue timestamp.
				_, name, err := storage.ParseGCSURI(uri)
				if err != nil {
					return err
				}
				attrs, err := w.StorageClient.ObjectAttrs(ctx, name)
				if err != nil {
					return fmt.Errorf("check orphan chunk age: %w", err)
				}
				requestedAt = attrs.Created
			}
			if requestedAt.IsZero() || time.Since(requestedAt) < orphanChunkGrace {
				return fmt.Errorf("waiting for chunk analysis to complete: %s has no terminal result", uri)
			}
		}
		result := &db.ChunkAnalysisResult{
			SessionID: p.SessionID, ProfileID: p.ProfileID, FilePath: uri,
			Status: "FAILED", Output: "Chunk analysis was archived or abandoned before merge.",
			WorkoutConfidence: chunk.WorkoutConfidence, HeartRateBPM: chunk.HeartRateBPM,
		}
		if known && chunk.EndSecs > chunk.StartSecs {
			result.StartSecs, result.EndSecs = &chunk.StartSecs, &chunk.EndSecs
		}
		if err := w.persistChunkAnalysisResult(ctx, result); err != nil {
			return err
		}
	}
	return nil
}

var numberedChunkName = regexp.MustCompile(`(?i)^chunk_(\d+)\.(mp4|mov)$`)

// Prefer capture times. Only explicitly numbered chunk files can safely fall
// back to filename order; native camera UUIDs do not encode capture order.
func orderedMergeChunks(gcsChunks []string, records []db.ChunkAnalysisResult) ([]string, error) {
	byPath := make(map[string]db.ChunkAnalysisResult, len(records))
	for _, rec := range records {
		old, exists := byPath[rec.FilePath]
		if !exists || (old.StartSecs == nil && rec.StartSecs != nil) {
			byPath[rec.FilePath] = rec
		}
	}
	objects := make([]string, 0, len(gcsChunks))
	seen := make(map[string]bool)
	allTimed := true
	for _, uri := range gcsChunks {
		if seen[uri] {
			continue
		}
		seen[uri] = true
		objects = append(objects, uri)
		if byPath[uri].StartSecs == nil {
			allTimed = false
		}
	}
	if len(objects) <= 1 {
		return objects, nil
	}
	if allTimed {
		sort.Slice(objects, func(i, j int) bool {
			a, b := *byPath[objects[i]].StartSecs, *byPath[objects[j]].StartSecs
			if a == b {
				return objects[i] < objects[j]
			}
			return a < b
		})
		return objects, nil
	}
	indices := make(map[string]uint64, len(objects))
	used := make(map[uint64]bool)
	for _, uri := range objects {
		match := numberedChunkName.FindStringSubmatch(filepath.Base(uri))
		if match == nil {
			return nil, errors.New("cannot verify original chunk order: capture timestamps are missing")
		}
		index, err := strconv.ParseUint(match[1], 10, 64)
		if err != nil || used[index] {
			return nil, errors.New("cannot verify original chunk order: ambiguous chunk numbers")
		}
		used[index], indices[uri] = true, index
	}
	sort.Slice(objects, func(i, j int) bool { return indices[objects[i]] < indices[objects[j]] })
	return objects, nil
}
