package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

	gcs "cloud.google.com/go/storage"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type limitedMediaThreadsKey struct{}

// Implemented by the real storage client and its session-fenced wrapper.
type mediaObjectPublisher interface {
	CopyObjectGeneration(context.Context, string, int64, string, int64, map[string]string) (*gcs.ObjectAttrs, error)
}

type mediaObjectCleaner interface {
	DeleteObject(context.Context, string) error
}

// An ambiguous commit is resolved by reading the durable pointer first. Never
// delete a published artifact or guess when the database cannot be reached.
func (w *Worker) cleanupRecordingAttemptObject(ctx context.Context, object, table string, id uint, column string) {
	cleaner, ok := w.StorageClient.(mediaObjectCleaner)
	if !ok {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var row struct{ Object string }
	err := w.DB.WithContext(cleanup).Table(table).Select(column+" AS object").Where("id = ?", id).Take(&row).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return
	}
	if row.Object == object {
		return
	}
	// The object name includes this attempt's unpredictable token and is never
	// reused by another attempt. Source files never pass through this helper.
	_ = cleaner.DeleteObject(cleanup, object)
}

func withLimitedMediaThreads(ctx context.Context) context.Context {
	return context.WithValue(ctx, limitedMediaThreadsKey{}, true)
}

func mediaThreadsLimited(ctx context.Context) bool {
	limited, _ := ctx.Value(limitedMediaThreadsKey{}).(bool)
	return limited
}

func mediaDecoderThreads(ctx context.Context) string {
	if mediaThreadsLimited(ctx) {
		return "1"
	}
	return "0"
}

func limitMediaCommandThreads(ctx context.Context, args []string) []string {
	if !mediaThreadsLimited(ctx) {
		return args
	}
	return append([]string{"-threads", "1", "-filter_threads", "1", "-filter_complex_threads", "1"}, args...)
}

const storedFrameSignatureVersion = "framehash-murmur3-sha256-tb30000-v1"

type storedFrameTiming struct {
	PTS      int64 `json:"pts"`
	Duration int64 `json:"duration"`
}

type storedFrameSignature struct {
	Version    string              `json:"version"`
	FrameCount int                 `json:"frame_count"`
	Digest     string              `json:"digest"`
	Start      float64             `json:"start"`
	End        float64             `json:"end"`
	Timings    []storedFrameTiming `json:"timings"`
}

func encodeFrameSignature(signature videoFrameSignature) (db.JSONDocument, error) {
	value := storedFrameSignature{Version: storedFrameSignatureVersion, FrameCount: signature.FrameCount, Digest: signature.Digest, Start: signature.Start, End: signature.End}
	for _, timing := range signature.timings {
		value.Timings = append(value.Timings, storedFrameTiming{timing.pts, timing.duration})
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if _, err := decodeFrameSignature(raw); err != nil {
		return nil, err
	}
	return db.JSONDocument(raw), nil
}

func decodeFrameSignature(raw db.JSONDocument) (videoFrameSignature, error) {
	var value storedFrameSignature
	if err := json.Unmarshal(raw, &value); err != nil {
		return videoFrameSignature{}, err
	}
	if value.Version != storedFrameSignatureVersion || value.FrameCount <= 0 || value.FrameCount != len(value.Timings) || len(value.Digest) != 64 || math.IsNaN(value.Start) || math.IsInf(value.Start, 0) || math.IsNaN(value.End) || math.IsInf(value.End, 0) || value.End <= value.Start {
		return videoFrameSignature{}, errors.New("incomplete or unsupported stored frame signature")
	}
	result := videoFrameSignature{FrameCount: value.FrameCount, Digest: value.Digest, Start: value.Start, End: value.End}
	for i, timing := range value.Timings {
		if timing.Duration <= 0 || (i > 0 && timing.PTS <= value.Timings[i-1].PTS) {
			return videoFrameSignature{}, errors.New("invalid stored frame timing")
		}
		result.timings = append(result.timings, frameTiming{timing.PTS, timing.Duration})
	}
	return result, nil
}

func (w *Worker) downloadPinnedMedia(ctx context.Context, uri string, generation int64, path string) error {
	if generation <= 0 {
		return errors.New("media source generation is missing")
	}
	bucket, object, err := storage.ParseGCSURI(uri)
	if err != nil {
		return err
	}
	if bucket != w.BucketName {
		return errors.New("media source bucket does not match worker bucket")
	}
	reader, err := w.StorageClient.NewReaderWithGeneration(ctx, object, generation)
	if err != nil {
		return err
	}
	defer reader.Close()
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return copyErr
	}
	return closeErr
}

func (w *Worker) downloadAnalysisInput(ctx context.Context, p VideoAnalysisPayload, path string) error {
	if p.SourceGeneration > 0 {
		return w.downloadPinnedMedia(ctx, p.FilePath, p.SourceGeneration, path)
	}
	return w.StorageClient.DownloadFile(ctx, p.FilePath, path)
}

// Only the short generation-conditional publication holds the session fence;
// encoding and uploading the immutable staging object happen before this call.
func (w *Worker) publishRecordingMerged(ctx context.Context, run db.RecordingMediaRun, stagingObject string, stagingGeneration int64) (*gcs.ObjectAttrs, error) {
	publisher, ok := w.StorageClient.(mediaObjectPublisher)
	if !ok {
		return nil, errors.New("storage does not support generation-conditional publication")
	}
	if stagingGeneration <= 0 || run.LeaseToken == "" {
		return nil, errors.New("merged publication has no verified staging lease")
	}
	var attrs *gcs.ObjectAttrs
	err := w.withSessionPublication(ctx, run.ProfileID, run.SessionID, func(tx *gorm.DB) error {
		var current db.RecordingMediaRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, run.ID).Error; err != nil {
			return err
		}
		if current.Version != db.RecordingMediaVersion || current.State != "MERGING" || current.LeaseToken != run.LeaseToken || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(time.Now()) || current.ManifestHash != run.ManifestHash || current.MergedStagingObject != stagingObject || current.MergedStagingGeneration != stagingGeneration {
			return db.ErrSessionWorkCancelled
		}
		object := fmt.Sprintf("videos/%d/%s/merged.mp4", run.ProfileID, run.SessionID)
		previous, err := w.StorageClient.ObjectAttrs(ctx, object)
		if err != nil && !errors.Is(err, gcs.ErrObjectNotExist) {
			return err
		}
		metadata := map[string]string{"media_run_id": strconv.FormatUint(uint64(run.ID), 10), "manifest_hash": run.ManifestHash, "staging_object": stagingObject, "staging_generation": strconv.FormatInt(stagingGeneration, 10)}
		if previous != nil && previous.Metadata["media_run_id"] == metadata["media_run_id"] && previous.Metadata["manifest_hash"] == metadata["manifest_hash"] && previous.Metadata["staging_object"] == stagingObject && previous.Metadata["staging_generation"] == metadata["staging_generation"] {
			attrs = previous
			return nil
		}
		var expected int64
		if previous != nil {
			expected = previous.Generation
		}
		publishCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		attrs, err = publisher.CopyObjectGeneration(publishCtx, stagingObject, stagingGeneration, object, expected, metadata)
		return err
	})
	return attrs, err
}
