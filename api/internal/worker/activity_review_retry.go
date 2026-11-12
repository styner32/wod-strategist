package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	activityReviewMaxWindowAttempts = 3
	activityReviewAICallTimeout     = 180 * time.Second
	activityReviewLeaseDuration     = 210 * time.Second
	activityReviewMaxRetryAfter     = 5 * time.Minute
)

var (
	errActivitySourceSuperseded = errors.New("activity review source or media superseded")
	errActivityWindowLeaseLost  = errors.New("activity review window lease lost or already completed")
	errActivityWindowExhausted  = errors.New("activity review window attempt budget exhausted")
)

type activityReviewWindowLedger struct {
	SourceVersion   string                     `json:"source_version"`
	MediaGeneration int64                      `json:"media_generation"`
	ReviewVersion   int                        `json:"review_version"`
	AutoReviews     []activity.ReviewedChunk   `json:"auto_reviews,omitempty"`
	Windows         []activityReviewWindowSlot `json:"windows"`
}

type activityReviewWindowSlot struct {
	Key              string                   `json:"key"`
	ChunkIDs         []uint                   `json:"chunk_ids"`
	Indices          []int                    `json:"indices"`
	WinStart         float64                  `json:"win_start"`
	WinEnd           float64                  `json:"win_end"`
	CoreStart        float64                  `json:"core_start"`
	CoreEnd          float64                  `json:"core_end"`
	Valid            bool                     `json:"valid"`
	State            string                   `json:"state"` // pending, completed, failed
	ReservedAttempts int                      `json:"reserved_attempts"`
	NextAttemptAt    *time.Time               `json:"next_attempt_at,omitempty"`
	LeaseToken       string                   `json:"lease_token,omitempty"`
	LeaseExpiresAt   *time.Time               `json:"lease_expires_at,omitempty"`
	LastStage        string                   `json:"last_stage,omitempty"`
	LastError        string                   `json:"last_error,omitempty"`
	ChunkReviews     []activity.ReviewedChunk `json:"chunk_reviews,omitempty"`
}

func newActivityReviewLeaseToken() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("lease-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

func formatActivityReviewWindowKey(p activityReviewPayload, b activityReviewBatch, rows []db.ChunkAnalysisResult) string {
	if !b.Valid {
		var chunkID uint
		if len(b.Indices) > 0 && b.Indices[0] >= 0 && b.Indices[0] < len(rows) {
			chunkID = rows[b.Indices[0]].ID
		}
		return fmt.Sprintf("%s:%d:%d:invalid:%d", p.SourceVersion, p.Generation, activity.Version, chunkID)
	}
	return fmt.Sprintf(
		"%s:%d:%d:%.6f:%.6f:%.6f:%.6f",
		p.SourceVersion, p.Generation, activity.Version,
		b.WinStart, b.WinEnd, b.CoreStart, b.CoreEnd,
	)
}

func failedActivityWindowReviews(chunkIDs []uint) []activity.ReviewedChunk {
	out := make([]activity.ReviewedChunk, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		out = append(out, activity.ReviewedChunk{
			ChunkID: id,
			State:   "failed",
		})
	}
	return out
}

func mergeActivityLedgerIntoSummary(
	rows []db.ChunkAnalysisResult,
	current *activity.Summary,
	p activityReviewPayload,
	ledger *activityReviewWindowLedger,
	reviewState string,
) activity.Summary {
	var base activity.Summary
	if current != nil && current.SourceVersion == p.SourceVersion && current.MediaGeneration == strconv.FormatInt(p.Generation, 10) {
		base = *current
	}
	byChunk := make(map[uint]activity.ReviewedChunk, len(base.Reviews)+len(ledger.AutoReviews))
	for _, r := range base.Reviews {
		if r.State == "completed" || r.State == "failed" {
			byChunk[r.ChunkID] = r
		}
	}
	for _, r := range ledger.AutoReviews {
		byChunk[r.ChunkID] = r
	}
	for _, win := range ledger.Windows {
		switch win.State {
		case "completed":
			for _, r := range win.ChunkReviews {
				byChunk[r.ChunkID] = r
			}
		case "failed":
			if len(win.ChunkReviews) > 0 {
				for _, r := range win.ChunkReviews {
					byChunk[r.ChunkID] = r
				}
			} else {
				for _, r := range failedActivityWindowReviews(win.ChunkIDs) {
					byChunk[r.ChunkID] = r
				}
			}
		}
	}
	reviews := make([]activity.ReviewedChunk, 0, len(byChunk))
	for _, row := range rows {
		if r, ok := byChunk[row.ID]; ok {
			reviews = append(reviews, r)
		}
	}
	base.SourceVersion = p.SourceVersion
	base.ReviewVersion = activity.Version
	base.MediaGeneration = strconv.FormatInt(p.Generation, 10)
	base.ReviewState = reviewState
	base.Reviews = reviews
	return activity.Build(rows, &base)
}

func (w *Worker) sleepActivityReview(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	if w != nil && w.activityReviewSleep != nil {
		return w.activityReviewSleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func activityReviewRetryDelay(attempt int, err error) time.Duration {
	if retryAfter, ok := gemini.RetryAfterDuration(err); ok && retryAfter > 0 {
		if retryAfter > activityReviewMaxRetryAfter {
			return activityReviewMaxRetryAfter
		}
		return retryAfter
	}
	switch attempt {
	case 1:
		return 5 * time.Second
	default:
		return 15 * time.Second
	}
}

func isRetryableActivityReviewAIError(parentCtx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if parentCtx != nil && parentCtx.Err() != nil {
		return false
	}
	if errors.Is(err, db.ErrSessionWorkCancelled) || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if code, ok := gemini.HTTPStatusCode(err); ok {
		if code == 429 || (code >= 500 && code <= 599) {
			return true
		}
		if code >= 400 && code < 500 {
			return false
		}
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection reset",
		"broken pipe",
		"tls handshake timeout",
		"unexpected eof",
		"temporary failure",
		"server closed idle connection",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func (w *Worker) handleActivityReviewTaskWindowRetry(ctx context.Context, t *asynq.Task) error {
	var p activityReviewPayload
	if t == nil || json.Unmarshal(t.Payload(), &p) != nil || p.ProfileID == 0 || validateSessionID(p.SessionID) != nil || p.Generation <= 0 || p.Object != fmt.Sprintf("videos/%d/%s/merged.mp4", p.ProfileID, p.SessionID) {
		return fmt.Errorf("invalid activity review payload: %w", asynq.SkipRetry)
	}

	timeline := w.loadSessionSensorTimeline(ctx, p.ProfileID, p.SessionID)
	workerToken := newActivityReviewLeaseToken()

	var (
		session        db.Session
		rows           []db.ChunkAnalysisResult
		ledger         activityReviewWindowLedger
		claimedIndices []int
		doneEarly      bool
	)

	err := w.withSessionPublication(ctx, p.ProfileID, p.SessionID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("profile_id = ? AND session_id = ?", p.ProfileID, p.SessionID).
			First(&session).Error; err != nil {
			return err
		}
		var txRows []db.ChunkAnalysisResult
		if err := tx.Where("profile_id = ? AND session_id = ?", p.ProfileID, p.SessionID).Find(&txRows).Error; err != nil {
			return err
		}
		rows = activity.CanonicalChunks(txRows)
		if activity.SourceVersion(rows) != p.SourceVersion {
			doneEarly = true
			return nil
		}

		var stored activity.Summary
		_ = json.Unmarshal(session.ActivitySummary, &stored)
		currentGeneration, _ := strconv.ParseInt(stored.MediaGeneration, 10, 64)
		if stored.SourceVersion == p.SourceVersion && currentGeneration > p.Generation {
			doneEarly = true
			return nil
		}
		if currentGeneration != p.Generation {
			stored = activity.Summary{}
		}

		initialSummary := activity.Build(rows, &stored)
		if initialSummary.ReviewState == "completed" {
			doneEarly = true
			return nil
		}
		if !w.ActivityCountingEnabled {
			initialSummary.MediaGeneration = strconv.FormatInt(p.Generation, 10)
			initialSummary.ReviewState = "disabled"
			disabledSummary := activity.Build(rows, &initialSummary)
			rawSummary, mErr := json.Marshal(disabledSummary)
			if mErr != nil {
				return mErr
			}
			doneEarly = true
			return tx.Model(&session).Updates(map[string]any{
				"activity_summary":               db.JSONDocument(rawSummary),
				"activity_summary_cache_version": activity.SummaryCacheVersion,
			}).Error
		}

		var existingLedger activityReviewWindowLedger
		ledgerLoaded := len(session.ActivityReviewAttempts) > 0 &&
			json.Unmarshal(session.ActivityReviewAttempts, &existingLedger) == nil &&
			existingLedger.SourceVersion == p.SourceVersion &&
			existingLedger.MediaGeneration == p.Generation &&
			existingLedger.ReviewVersion == activity.Version

		if !ledgerLoaded && stored.SourceVersion == p.SourceVersion && currentGeneration == p.Generation && stored.ReviewState == "partial" && len(stored.Reviews) > 0 {
			allTerminal := true
			for _, r := range stored.Reviews {
				if r.State != "completed" && r.State != "failed" {
					allTerminal = false
					break
				}
			}
			if allTerminal {
				doneEarly = true
				return nil
			}
		}

		if ledgerLoaded {
			ledger = existingLedger
			completedFromSummary := make(map[uint]activity.ReviewedChunk, len(initialSummary.Reviews))
			for _, r := range initialSummary.Reviews {
				if r.State == "completed" {
					completedFromSummary[r.ChunkID] = r
				}
			}
			autoByChunk := make(map[uint]activity.ReviewedChunk, len(ledger.AutoReviews))
			for _, r := range ledger.AutoReviews {
				if saved, ok := completedFromSummary[r.ChunkID]; ok {
					autoByChunk[r.ChunkID] = saved
				} else if len(initialSummary.Reviews) == 0 {
					autoByChunk[r.ChunkID] = r
				}
			}
			completedIDs := make(map[uint]bool, len(rows))
			for cid := range autoByChunk {
				completedIDs[cid] = true
			}
			needsWindowRebuild := false
			maxInheritedAttempts := 0
			for i := range ledger.Windows {
				win := &ledger.Windows[i]
				if len(win.ChunkIDs) == 0 {
					continue
				}
				allCompletedInSummary := true
				someCompletedInSummary := false
				reconstructed := make([]activity.ReviewedChunk, 0, len(win.ChunkIDs))
				for _, cid := range win.ChunkIDs {
					if r, ok := completedFromSummary[cid]; ok {
						someCompletedInSummary = true
						reconstructed = append(reconstructed, r)
					} else {
						allCompletedInSummary = false
					}
				}
				if allCompletedInSummary {
					win.State = "completed"
					win.ChunkReviews = reconstructed
					win.LeaseToken = ""
					win.LeaseExpiresAt = nil
					for _, r := range reconstructed {
						autoByChunk[r.ChunkID] = r
						completedIDs[r.ChunkID] = true
					}
					continue
				}
				if win.State == "completed" || someCompletedInSummary {
					// Summary was externally modified (e.g. partial reset of one chunk);
					// preserve completed chunks in AutoReviews and rebuild pending window bounds
					// while carrying forward ReservedAttempts so the 3-attempt cap stays intact.
					needsWindowRebuild = true
					if win.ReservedAttempts > maxInheritedAttempts {
						maxInheritedAttempts = win.ReservedAttempts
					}
					for _, cid := range win.ChunkIDs {
						if r, ok := completedFromSummary[cid]; ok {
							autoByChunk[cid] = r
							completedIDs[cid] = true
						}
					}
				}
			}
			if needsWindowRebuild {
				rebuiltAuto := make([]activity.ReviewedChunk, 0, len(autoByChunk))
				for _, row := range rows {
					if r, ok := autoByChunk[row.ID]; ok {
						rebuiltAuto = append(rebuiltAuto, r)
					}
				}
				batches := buildActivityReviewBatches(rows, completedIDs)
				rebuiltWindows := make([]activityReviewWindowSlot, 0, len(batches))
				for _, b := range batches {
					chunkIDs := make([]uint, 0, len(b.Indices))
					for _, idx := range b.Indices {
						chunkIDs = append(chunkIDs, rows[idx].ID)
					}
					slot := activityReviewWindowSlot{
						Key:              formatActivityReviewWindowKey(p, b, rows),
						ChunkIDs:         chunkIDs,
						Indices:          append([]int(nil), b.Indices...),
						WinStart:         b.WinStart,
						WinEnd:           b.WinEnd,
						CoreStart:        b.CoreStart,
						CoreEnd:          b.CoreEnd,
						Valid:            b.Valid,
						State:            "pending",
						ReservedAttempts: maxInheritedAttempts,
					}
					if !b.Valid {
						slot.State = "failed"
						slot.LastStage = "invalid_clock"
						slot.ChunkReviews = failedActivityWindowReviews(chunkIDs)
					} else if slot.ReservedAttempts >= activityReviewMaxWindowAttempts {
						slot.State = "failed"
						slot.ChunkReviews = failedActivityWindowReviews(chunkIDs)
					}
					rebuiltWindows = append(rebuiltWindows, slot)
				}
				ledger.AutoReviews = rebuiltAuto
				ledger.Windows = rebuiltWindows
			}
		} else {
			previous := make(map[uint]activity.ReviewedChunk, len(initialSummary.Reviews))
			for _, r := range initialSummary.Reviews {
				previous[r.ChunkID] = r
			}
			restSkipCandidates := make([]bool, len(rows))
			for i := range rows {
				restSkipCandidates[i] = shouldSkipActivityReviewRestChunk(rows, i, session.WorkoutType, timeline)
			}
			completedIDs := make(map[uint]bool, len(rows))
			autoReviews := make([]activity.ReviewedChunk, 0, len(rows))
			skippedRestChunks := 0
			skippedNotVisibleChunks := 0
			for i, row := range rows {
				if saved, ok := previous[row.ID]; ok && saved.State == "completed" {
					autoReviews = append(autoReviews, saved)
					completedIDs[row.ID] = true
					continue
				}
				if isNotVisibleRunInterior(rows, i) {
					acc := chunkAccStats(timeline, *row.StartSecs, *row.EndSecs)
					sensorActive := acc.hasValid && (acc.maxG >= activityReviewAccVetoMaxG || acc.meanG >= activityReviewAccVetoMeanG)
					autoReviews = append(autoReviews, activity.ReviewedChunk{
						ChunkID:      row.ID,
						State:        "completed",
						Observations: activity.ConfirmedNotVisible(*row.MediaStartSecs, *row.MediaEndSecs, sensorActive),
					})
					completedIDs[row.ID] = true
					skippedNotVisibleChunks++
					continue
				}
				skipRest := restSkipCandidates[i]
				if !IsRecoveryWorkoutType(session.WorkoutType) && i > 0 &&
					activity.Adjacent(rows[i-1], row) && !restSkipCandidates[i-1] {
					skipRest = false
				}
				if skipRest {
					dur := *row.MediaEndSecs - *row.MediaStartSecs
					autoReviews = append(autoReviews, activity.ReviewedChunk{
						ChunkID:      row.ID,
						State:        "completed",
						Observations: activity.ConfirmedRest(dur),
					})
					completedIDs[row.ID] = true
					skippedRestChunks++
					continue
				}
			}

			batches := buildActivityReviewBatches(rows, completedIDs)
			w.logger.Info("activity review plan",
				zap.String("session_id", p.SessionID),
				zap.Int("total_chunks", len(rows)),
				zap.Int("skipped_rest_chunks", skippedRestChunks),
				zap.Int("skipped_not_visible_chunks", skippedNotVisibleChunks),
				zap.Int("batch_count", len(batches)),
				zap.Bool("has_sensor_timeline", timeline != nil),
			)

			windows := make([]activityReviewWindowSlot, 0, len(batches))
			for _, b := range batches {
				chunkIDs := make([]uint, 0, len(b.Indices))
				for _, idx := range b.Indices {
					chunkIDs = append(chunkIDs, rows[idx].ID)
				}
				slot := activityReviewWindowSlot{
					Key:       formatActivityReviewWindowKey(p, b, rows),
					ChunkIDs:  chunkIDs,
					Indices:   append([]int(nil), b.Indices...),
					WinStart:  b.WinStart,
					WinEnd:    b.WinEnd,
					CoreStart: b.CoreStart,
					CoreEnd:   b.CoreEnd,
					Valid:     b.Valid,
					State:     "pending",
				}
				if !b.Valid {
					slot.State = "failed"
					slot.LastStage = "invalid_clock"
					slot.ChunkReviews = failedActivityWindowReviews(chunkIDs)
				}
				windows = append(windows, slot)
			}
			ledger = activityReviewWindowLedger{
				SourceVersion:   p.SourceVersion,
				MediaGeneration: p.Generation,
				ReviewVersion:   activity.Version,
				AutoReviews:     autoReviews,
				Windows:         windows,
			}
		}

		now := time.Now().UTC()
		activeExternalLeases := 0
		claimedIndices = nil
		for i := range ledger.Windows {
			win := &ledger.Windows[i]
			if win.State != "pending" {
				continue
			}
			if win.LeaseToken != "" && win.LeaseExpiresAt != nil && win.LeaseExpiresAt.After(now) {
				activeExternalLeases++
				continue
			}
			if win.ReservedAttempts >= activityReviewMaxWindowAttempts {
				win.State = "failed"
				win.LeaseToken = ""
				win.LeaseExpiresAt = nil
				win.ChunkReviews = failedActivityWindowReviews(win.ChunkIDs)
				continue
			}
			exp := now.Add(activityReviewLeaseDuration)
			win.LeaseToken = workerToken
			win.LeaseExpiresAt = &exp
			claimedIndices = append(claimedIndices, i)
		}

		if len(claimedIndices) == 0 {
			doneEarly = true
			if activeExternalLeases > 0 {
				return nil
			}
			anyFailed := false
			for _, win := range ledger.Windows {
				if win.State != "completed" {
					anyFailed = true
					break
				}
			}
			finalSummary := mergeActivityLedgerIntoSummary(rows, &stored, p, &ledger, "completed")
			if anyFailed || len(finalSummary.Unassessed) > 0 {
				finalSummary = mergeActivityLedgerIntoSummary(rows, &stored, p, &ledger, "partial")
			}
			rawSummary, mErr := json.Marshal(finalSummary)
			if mErr != nil {
				return mErr
			}
			rawLedger, lErr := json.Marshal(ledger)
			if lErr != nil {
				return lErr
			}
			return tx.Model(&session).Updates(map[string]any{
				"activity_summary":               db.JSONDocument(rawSummary),
				"activity_review_attempts":       db.NullableJSONDocument(rawLedger),
				"activity_summary_cache_version": activity.SummaryCacheVersion,
			}).Error
		}

		runningSummary := mergeActivityLedgerIntoSummary(rows, &stored, p, &ledger, "running")
		rawSummary, mErr := json.Marshal(runningSummary)
		if mErr != nil {
			return mErr
		}
		rawLedger, lErr := json.Marshal(ledger)
		if lErr != nil {
			return lErr
		}
		return tx.Model(&session).Updates(map[string]any{
			"activity_summary":               db.JSONDocument(rawSummary),
			"activity_review_attempts":       db.NullableJSONDocument(rawLedger),
			"activity_summary_cache_version": activity.SummaryCacheVersion,
		}).Error
	})
	if err != nil {
		return err
	}
	if doneEarly {
		return nil
	}

	releaseActiveLeasesOnAbort := true
	defer func() {
		if !releaseActiveLeasesOnAbort {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = w.releaseActivityReviewWorkerLeases(cleanupCtx, p, workerToken)
	}()

	// Download merged.mp4 once for all claimed windows in this task execution.
	reader, err := w.StorageClient.NewReaderWithGeneration(ctx, p.Object, p.Generation)
	if err != nil {
		return err
	}
	local, err := os.CreateTemp("", "activity-review-*.mp4")
	if err != nil {
		_ = reader.Close()
		return err
	}
	localPath := local.Name()
	defer os.Remove(localPath)

	_, copyErr := io.Copy(local, reader)
	closeReaderErr := reader.Close()
	closeFileErr := local.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeReaderErr != nil {
		return closeReaderErr
	}
	if closeFileErr != nil {
		return closeFileErr
	}

	var (
		uploadMu          sync.Mutex
		uploaded          *gemini.UploadResult
		uploadedFileNames = make(map[string]struct{})
	)
	defer func() {
		for fileName := range uploadedFileNames {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			_ = w.GeminiClient.DeleteFile(cleanup, fileName)
			cancel()
		}
	}()
	ensureUploaded := func(callCtx context.Context) (*gemini.UploadResult, error) {
		uploadMu.Lock()
		defer uploadMu.Unlock()
		if uploaded != nil && uploaded.FileURI != "" {
			return uploaded, nil
		}
		res, upErr := w.GeminiClient.UploadVideo(callCtx, localPath)
		if res != nil && res.FileName != "" {
			// A failed Files processing/poll can still return an owned FileName.
			// Retain every name so a later upload cannot orphan an earlier file.
			uploadedFileNames[res.FileName] = struct{}{}
			uploaded = res
		}
		if upErr != nil {
			return nil, upErr
		}
		return uploaded, nil
	}

	duration := probeVideoDuration(ctx, localPath)
	localProbed := duration > 0
	if !localProbed {
		up, upErr := ensureUploaded(ctx)
		if upErr != nil {
			return upErr
		}
		duration = up.VideoDuration.Seconds()
	}
	if duration <= 0 {
		return errors.New("activity review media duration unavailable")
	}
	if duration > maxAnalysisVideoDuration.Seconds() {
		w.logger.Warn("Activity review skipped: video duration exceeds maximum analysis limit",
			zap.String("session_id", p.SessionID),
			zap.Float64("duration_secs", duration),
			zap.Float64("max_duration_secs", maxAnalysisVideoDuration.Seconds()))
		if err := w.failOverlongActivityReviewWindows(ctx, p, workerToken); err != nil {
			return err
		}
		releaseActiveLeasesOnAbort = false
		return nil
	}

	target := w.buildTargetPersonContext(p.ProfileID, p.SessionID)
	trackingRefs := w.loadTrackingReferences(ctx, p.SessionID, p.ProfileID)
	trackingCache := make(map[string][]byte, len(trackingRefs))

	type preparedWindowInputs struct {
		slotIdx        int
		slot           activityReviewWindowSlot
		trackingPrompt string
		trackingImages [][]byte
	}
	workItems := make([]preparedWindowInputs, 0, len(claimedIndices))
	for _, slotIdx := range claimedIndices {
		slot := ledger.Windows[slotIdx]
		var trackingPrompt string
		var trackingImages [][]byte
		if slot.Valid && slot.WinEnd <= duration+0.001 {
			trackingPrompt, trackingImages = w.cachedPass2TrackingInputs(ctx, trackingRefs, trackingCache, slot.WinStart, slot.WinEnd)
		}
		workItems = append(workItems, preparedWindowInputs{
			slotIdx:        slotIdx,
			slot:           slot,
			trackingPrompt: trackingPrompt,
			trackingImages: trackingImages,
		})
	}

	sem := make(chan struct{}, maxConcurrentSegmentAnalyses)
	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		fatalErr error
	)
	recordFatal := func(e error) {
		if e == nil || errors.Is(e, errActivitySourceSuperseded) || errors.Is(e, errActivityWindowLeaseLost) {
			return
		}
		errMu.Lock()
		if fatalErr == nil {
			fatalErr = e
		}
		errMu.Unlock()
	}

	for _, item := range workItems {
		wg.Add(1)
		go func(wi preparedWindowInputs) {
			defer wg.Done()
			if err := w.executeActivityReviewWindowWithRetry(
				ctx, p, session, rows, localPath, duration, localProbed,
				ensureUploaded, target, wi.slot, wi.trackingPrompt, wi.trackingImages,
				workerToken, sem,
			); err != nil {
				recordFatal(err)
			}
		}(item)
	}
	wg.Wait()

	if fatalErr != nil {
		return fatalErr
	}
	if err := checkSessionWork(ctx); err != nil {
		return err
	}
	if err := w.finalizeActivityReviewLedger(ctx, p, workerToken); err != nil {
		if errors.Is(err, errActivitySourceSuperseded) {
			releaseActiveLeasesOnAbort = false
			return nil
		}
		return err
	}
	releaseActiveLeasesOnAbort = false
	return nil
}

func (w *Worker) executeActivityReviewWindowWithRetry(
	ctx context.Context,
	p activityReviewPayload,
	session db.Session,
	rows []db.ChunkAnalysisResult,
	localPath string,
	duration float64,
	localProbed bool,
	ensureUploaded func(context.Context) (*gemini.UploadResult, error),
	target string,
	slot activityReviewWindowSlot,
	trackingPrompt string,
	trackingImages [][]byte,
	workerToken string,
	sem chan struct{},
) error {
	if !slot.Valid || slot.WinEnd > duration+0.001 {
		return w.markActivityReviewWindowFailed(ctx, p, slot.Key, workerToken, "out_of_bounds", "invalid or out-of-bounds activity review window")
	}

	batchPolicy := ""
	if hasActivityBatchTravelingPolicyContext(session, rows, slot.Indices) {
		batchPolicy = TravelingMovementPolicyPrompt
	}
	prompt := movementObservationPrompt + batchPolicy + target + trackingPrompt + fmt.Sprintf(
		"\n검토 영상의 절대 media 초를 사용하세요. 문맥 구간 %.6f~%.6f초. 현재 집계 소유 구간은 (%.6f, %.6f]초입니다. 문맥에서 사이클 시작과 끝을 확인하고 기록하되, 이전 합계나 목표 횟수로 추측하지 마세요. 구간 중 일부에서만 대상이나 동작이 확인되고 다른 하위 구간만 가림·이탈·판독불가인 경우, 확인된 구간이 있으면 target_state는 identified로 유지하고 판독불가 하위 구간만 unassessed 배열에 기록하세요. 전체 구간 내내 대상을 식별할 수 없을 때만 unknown입니다.",
		slot.WinStart, slot.WinEnd, slot.CoreStart, slot.CoreEnd,
	)

	segStart := time.Duration(slot.WinStart * float64(time.Second))
	segEnd := time.Duration(slot.WinEnd * float64(time.Second))
	gridOpts := resolveFrameGridOptionsForDuration(segEnd-segStart, activityReviewFrameGridOptions())
	gridOpts.ReferenceImages = trackingImages

	var (
		preparedSheets   []FrameGridSheet
		framesReady      bool
		useVideoFallback bool
		firstChunkID     uint
	)
	if len(slot.ChunkIDs) > 0 {
		firstChunkID = slot.ChunkIDs[0]
	}

	if slot.NextAttemptAt != nil {
		if delay := time.Until(*slot.NextAttemptAt); delay > 0 {
			if err := w.sleepActivityReview(ctx, delay); err != nil {
				return err
			}
		}
	}

	for {
		if err := checkSessionWork(ctx); err != nil {
			return err
		}

		waitStart := time.Now()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sem <- struct{}{}:
		}
		semWait := time.Since(waitStart)

		var (
			slotWait   time.Duration
			extractDur time.Duration
			reusedPrep bool
		)

		if !framesReady && !useVideoFallback {
			if localProbed && frameGridEligible(segEnd-segStart, gridOpts) {
				sheets, mediaWait, extDur, extErr := ExtractFrameGridsForInterval(
					withWorkerMediaRuntime(ctx, w), localPath, segStart, segEnd, gridOpts,
				)
				slotWait = mediaWait
				extractDur = extDur
				if extErr != nil {
					w.logger.Warn("frame grid extraction failed for activity review window; falling back to video segment analysis",
						zap.String("session_id", p.SessionID),
						zap.String("window_key", slot.Key),
						zap.Uint("chunk_id", firstChunkID),
						zap.Float64("win_start", slot.WinStart),
						zap.Float64("win_end", slot.WinEnd),
						zap.Int64("wait_ms", (semWait+slotWait).Milliseconds()),
						zap.Int64("extract_ms", extractDur.Milliseconds()),
						zap.Error(extErr))
					useVideoFallback = true
				} else {
					preparedSheets = sheets
					framesReady = true
				}
			} else {
				useVideoFallback = true
			}
		} else {
			reusedPrep = true
		}

		totalWaitMs := (semWait + slotWait).Milliseconds()
		extractMs := extractDur.Milliseconds()

		attemptNum, reserveErr := w.reserveActivityReviewWindowAttempt(ctx, p, slot.Key, workerToken)
		if reserveErr != nil {
			<-sem
			if errors.Is(reserveErr, errActivityWindowExhausted) || errors.Is(reserveErr, errActivityWindowLeaseLost) || errors.Is(reserveErr, errActivitySourceSuperseded) {
				return nil
			}
			return reserveErr
		}

		// Start the 180s AI call deadline strictly AFTER semaphore wait, frame extraction,
		// and short-TX attempt reservation complete.
		aiStart := time.Now()
		callCtx, cancel := context.WithTimeout(ctx, activityReviewAICallTimeout)
		var (
			raw     string
			usage   *gemini.TokenUsage
			callErr error
		)
		if framesReady {
			raw, usage, callErr = w.AnalyzePreparedFrameGrids(callCtx, preparedSheets, prompt, gemini.ModelHydrogen4, gridOpts)
		} else {
			up, upErr := ensureUploaded(callCtx)
			if upErr != nil {
				callErr = upErr
			} else {
				segCtx := gemini.WithThinkingLevel(callCtx, "MEDIUM")
				if len(trackingImages) > 0 {
					raw, usage, callErr = w.GeminiClient.AnalyzeSegmentWithModelAndImages(
						segCtx, up.FileURI, up.MIMEType, segStart, segEnd, prompt, gemini.ModelHydrogen4, trackingImages,
					)
				} else {
					raw, usage, callErr = w.GeminiClient.AnalyzeSegmentWithModel(
						segCtx, up.FileURI, up.MIMEType, segStart, segEnd, prompt, gemini.ModelHydrogen4,
					)
				}
			}
		}
		cancel()
		aiMs := time.Since(aiStart).Milliseconds()
		<-sem

		reqKey := fmt.Sprintf("activity_review:%d:%s:attempt:%d", p.ProfileID, slot.Key, attemptNum)
		w.saveTokenUsageForRequest(p.SessionID, p.ProfileID, "activity:review", reqKey, usage)

		if callErr != nil {
			if err := checkSessionWork(ctx); err != nil {
				return err
			}
			retryable := isRetryableActivityReviewAIError(ctx, callErr) && attemptNum < activityReviewMaxWindowAttempts
			delay := time.Duration(0)
			if retryable {
				delay = activityReviewRetryDelay(attemptNum, callErr)
			}
			w.logger.Warn("activity review window attempt failed",
				zap.String("session_id", p.SessionID),
				zap.String("window_key", slot.Key),
				zap.Uint("chunk_id", firstChunkID),
				zap.Int("batch_chunks", len(slot.Indices)),
				zap.Float64("win_start", slot.WinStart),
				zap.Float64("win_end", slot.WinEnd),
				zap.Float64("core_start", slot.CoreStart),
				zap.Float64("core_end", slot.CoreEnd),
				zap.String("stage", "ai_call"),
				zap.Int("attempt", attemptNum),
				zap.Int("max_attempts", activityReviewMaxWindowAttempts),
				zap.Bool("retryable", retryable),
				zap.Bool("reused_frames", reusedPrep),
				zap.Int64("wait_ms", totalWaitMs),
				zap.Int64("extract_ms", extractMs),
				zap.Int64("ai_ms", aiMs),
				zap.Int64("parse_ms", 0),
				zap.Int64("backoff_ms", delay.Milliseconds()),
				zap.Error(callErr),
			)
			if !retryable {
				return w.markActivityReviewWindowFailed(ctx, p, slot.Key, workerToken, "ai_call", callErr.Error())
			}
			nextAt := time.Now().UTC().Add(delay)
			if err := w.recordActivityReviewWindowBackoff(ctx, p, slot.Key, workerToken, "ai_call", callErr.Error(), nextAt); err != nil {
				if errors.Is(err, errActivityWindowLeaseLost) || errors.Is(err, errActivitySourceSuperseded) {
					return nil
				}
				return err
			}
			if err := w.sleepActivityReview(ctx, delay); err != nil {
				return err
			}
			continue
		}

		parseStart := time.Now()
		document := strings.TrimSpace(raw)
		for _, m := range liveBlocks.FindAllStringSubmatch(raw, -1) {
			if strings.EqualFold(m[1], "movement_observations") {
				document = m[2]
				break
			}
		}
		doc, parseErr := activity.Decode([]byte(stripJSONFence(document)), slot.WinStart, slot.WinEnd)
		parseMs := time.Since(parseStart).Milliseconds()
		if parseErr != nil {
			w.logger.Warn("activity review window attempt failed",
				zap.String("session_id", p.SessionID),
				zap.String("window_key", slot.Key),
				zap.Uint("chunk_id", firstChunkID),
				zap.Int("batch_chunks", len(slot.Indices)),
				zap.Float64("win_start", slot.WinStart),
				zap.Float64("win_end", slot.WinEnd),
				zap.Float64("core_start", slot.CoreStart),
				zap.Float64("core_end", slot.CoreEnd),
				zap.String("stage", "parse"),
				zap.Int("attempt", attemptNum),
				zap.Int("max_attempts", activityReviewMaxWindowAttempts),
				zap.Bool("retryable", false),
				zap.Bool("reused_frames", reusedPrep),
				zap.Int64("wait_ms", totalWaitMs),
				zap.Int64("extract_ms", extractMs),
				zap.Int64("ai_ms", aiMs),
				zap.Int64("parse_ms", parseMs),
				zap.Error(parseErr),
			)
			return w.markActivityReviewWindowFailed(ctx, p, slot.Key, workerToken, "parse", parseErr.Error())
		}

		chunkReviews := make([]activity.ReviewedChunk, 0, len(slot.Indices))
		for _, idx := range slot.Indices {
			chunkRow := rows[idx]
			chunkReviews = append(chunkReviews, activity.ReviewedChunk{
				ChunkID:      chunkRow.ID,
				State:        "completed",
				Observations: activity.Own(doc, *chunkRow.MediaStartSecs, *chunkRow.MediaEndSecs),
			})
		}

		w.logger.Info("activity review window",
			zap.String("session_id", p.SessionID),
			zap.String("window_key", slot.Key),
			zap.Uint("chunk_id", firstChunkID),
			zap.Int("batch_chunks", len(slot.Indices)),
			zap.Float64("win_start", slot.WinStart),
			zap.Float64("win_end", slot.WinEnd),
			zap.Float64("core_start", slot.CoreStart),
			zap.Float64("core_end", slot.CoreEnd),
			zap.Int("attempt", attemptNum),
			zap.Bool("reused_frames", reusedPrep),
			zap.Int64("wait_ms", totalWaitMs),
			zap.Int64("extract_ms", extractMs),
			zap.Int64("ai_ms", aiMs),
			zap.Int64("parse_ms", parseMs),
			zap.Bool("failed", false),
		)

		if err := w.saveActivityReviewWindowSuccess(ctx, p, slot.Key, workerToken, chunkReviews); err != nil {
			if errors.Is(err, errActivityWindowLeaseLost) || errors.Is(err, errActivitySourceSuperseded) {
				return nil
			}
			return err
		}
		return nil
	}
}

func (w *Worker) updateActivityReviewLedgerTx(
	ctx context.Context,
	p activityReviewPayload,
	mutate func(tx *gorm.DB, session *db.Session, rows []db.ChunkAnalysisResult, stored *activity.Summary, ledger *activityReviewWindowLedger) (persistSummary bool, summaryState string, err error),
) error {
	return w.withSessionPublication(ctx, p.ProfileID, p.SessionID, func(tx *gorm.DB) error {
		var session db.Session
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("profile_id = ? AND session_id = ?", p.ProfileID, p.SessionID).
			First(&session).Error; err != nil {
			return err
		}
		var txRows []db.ChunkAnalysisResult
		if err := tx.Where("profile_id = ? AND session_id = ?", p.ProfileID, p.SessionID).Find(&txRows).Error; err != nil {
			return err
		}
		rows := activity.CanonicalChunks(txRows)
		if activity.SourceVersion(rows) != p.SourceVersion {
			return errActivitySourceSuperseded
		}
		var stored activity.Summary
		_ = json.Unmarshal(session.ActivitySummary, &stored)
		currentGeneration, _ := strconv.ParseInt(stored.MediaGeneration, 10, 64)
		if stored.SourceVersion == p.SourceVersion && currentGeneration > p.Generation {
			return errActivitySourceSuperseded
		}

		var ledger activityReviewWindowLedger
		if len(session.ActivityReviewAttempts) == 0 ||
			json.Unmarshal(session.ActivityReviewAttempts, &ledger) != nil ||
			ledger.SourceVersion != p.SourceVersion ||
			ledger.MediaGeneration != p.Generation ||
			ledger.ReviewVersion != activity.Version {
			return errActivitySourceSuperseded
		}

		persistSummary, summaryState, err := mutate(tx, &session, rows, &stored, &ledger)
		if err != nil {
			return err
		}

		rawLedger, err := json.Marshal(ledger)
		if err != nil {
			return err
		}
		updates := map[string]any{
			"activity_review_attempts": db.NullableJSONDocument(rawLedger),
		}
		if persistSummary {
			merged := mergeActivityLedgerIntoSummary(rows, &stored, p, &ledger, summaryState)
			rawSummary, mErr := json.Marshal(merged)
			if mErr != nil {
				return mErr
			}
			updates["activity_summary"] = db.JSONDocument(rawSummary)
			updates["activity_summary_cache_version"] = activity.SummaryCacheVersion
		}
		return tx.Model(&session).Updates(updates).Error
	})
}

func findActivityReviewWindowByKey(ledger *activityReviewWindowLedger, key string) *activityReviewWindowSlot {
	for i := range ledger.Windows {
		if ledger.Windows[i].Key == key {
			return &ledger.Windows[i]
		}
	}
	return nil
}

func (w *Worker) reserveActivityReviewWindowAttempt(ctx context.Context, p activityReviewPayload, windowKey, workerToken string) (int, error) {
	var attempt int
	err := w.updateActivityReviewLedgerTx(ctx, p, func(_ *gorm.DB, _ *db.Session, _ []db.ChunkAnalysisResult, stored *activity.Summary, ledger *activityReviewWindowLedger) (bool, string, error) {
		slot := findActivityReviewWindowByKey(ledger, windowKey)
		if slot == nil || slot.State != "pending" || slot.LeaseToken != workerToken {
			return false, "", errActivityWindowLeaseLost
		}
		if slot.ReservedAttempts >= activityReviewMaxWindowAttempts {
			slot.State = "failed"
			slot.LeaseToken = ""
			slot.LeaseExpiresAt = nil
			slot.ChunkReviews = failedActivityWindowReviews(slot.ChunkIDs)
			state := stored.ReviewState
			if state == "" {
				state = "running"
			}
			return true, state, nil
		}
		slot.ReservedAttempts++
		attempt = slot.ReservedAttempts
		exp := time.Now().UTC().Add(activityReviewLeaseDuration)
		slot.LeaseExpiresAt = &exp
		slot.NextAttemptAt = nil
		return false, "", nil
	})
	if err != nil {
		return 0, err
	}
	if attempt == 0 {
		return 0, errActivityWindowExhausted
	}
	return attempt, nil
}

func (w *Worker) recordActivityReviewWindowBackoff(
	ctx context.Context,
	p activityReviewPayload,
	windowKey, workerToken, stage, errMsg string,
	nextAttemptAt time.Time,
) error {
	return w.updateActivityReviewLedgerTx(ctx, p, func(_ *gorm.DB, _ *db.Session, _ []db.ChunkAnalysisResult, _ *activity.Summary, ledger *activityReviewWindowLedger) (bool, string, error) {
		slot := findActivityReviewWindowByKey(ledger, windowKey)
		if slot == nil || slot.State != "pending" || slot.LeaseToken != workerToken {
			return false, "", errActivityWindowLeaseLost
		}
		exp := nextAttemptAt.Add(activityReviewLeaseDuration)
		slot.LeaseExpiresAt = &exp
		slot.NextAttemptAt = &nextAttemptAt
		slot.LastStage = stage
		slot.LastError = truncateActivityReviewError(errMsg)
		return false, "", nil
	})
}

func (w *Worker) saveActivityReviewWindowSuccess(
	ctx context.Context,
	p activityReviewPayload,
	windowKey, workerToken string,
	chunkReviews []activity.ReviewedChunk,
) error {
	return w.updateActivityReviewLedgerTx(ctx, p, func(_ *gorm.DB, _ *db.Session, _ []db.ChunkAnalysisResult, stored *activity.Summary, ledger *activityReviewWindowLedger) (bool, string, error) {
		slot := findActivityReviewWindowByKey(ledger, windowKey)
		if slot == nil || slot.State != "pending" || slot.LeaseToken != workerToken {
			return false, "", errActivityWindowLeaseLost
		}
		slot.State = "completed"
		slot.ChunkReviews = chunkReviews
		slot.LeaseToken = ""
		slot.LeaseExpiresAt = nil
		slot.NextAttemptAt = nil
		slot.LastStage = ""
		slot.LastError = ""
		state := stored.ReviewState
		if state == "" || state == "queued" {
			state = "running"
		}
		return true, state, nil
	})
}

func (w *Worker) markActivityReviewWindowFailed(
	ctx context.Context,
	p activityReviewPayload,
	windowKey, workerToken, stage, errMsg string,
) error {
	err := w.updateActivityReviewLedgerTx(ctx, p, func(_ *gorm.DB, _ *db.Session, _ []db.ChunkAnalysisResult, stored *activity.Summary, ledger *activityReviewWindowLedger) (bool, string, error) {
		slot := findActivityReviewWindowByKey(ledger, windowKey)
		if slot == nil || slot.State != "pending" || slot.LeaseToken != workerToken {
			return false, "", errActivityWindowLeaseLost
		}
		slot.State = "failed"
		slot.ChunkReviews = failedActivityWindowReviews(slot.ChunkIDs)
		slot.LeaseToken = ""
		slot.LeaseExpiresAt = nil
		slot.NextAttemptAt = nil
		slot.LastStage = stage
		slot.LastError = truncateActivityReviewError(errMsg)
		state := stored.ReviewState
		if state == "" || state == "queued" {
			state = "running"
		}
		return true, state, nil
	})
	if errors.Is(err, errActivityWindowLeaseLost) || errors.Is(err, errActivitySourceSuperseded) {
		return nil
	}
	return err
}

func (w *Worker) failOverlongActivityReviewWindows(ctx context.Context, p activityReviewPayload, workerToken string) error {
	err := w.updateActivityReviewLedgerTx(ctx, p, func(_ *gorm.DB, _ *db.Session, _ []db.ChunkAnalysisResult, _ *activity.Summary, ledger *activityReviewWindowLedger) (bool, string, error) {
		for i := range ledger.Windows {
			slot := &ledger.Windows[i]
			if slot.State == "completed" {
				continue
			}
			if slot.LeaseToken == workerToken || slot.State == "pending" {
				slot.State = "failed"
				slot.ChunkReviews = failedActivityWindowReviews(slot.ChunkIDs)
				slot.LeaseToken = ""
				slot.LeaseExpiresAt = nil
				slot.NextAttemptAt = nil
				slot.LastStage = "duration_exceeded"
			}
		}
		return true, "partial", nil
	})
	if errors.Is(err, errActivitySourceSuperseded) {
		return nil
	}
	return err
}

func (w *Worker) finalizeActivityReviewLedger(ctx context.Context, p activityReviewPayload, workerToken string) error {
	return w.updateActivityReviewLedgerTx(ctx, p, func(_ *gorm.DB, _ *db.Session, rows []db.ChunkAnalysisResult, stored *activity.Summary, ledger *activityReviewWindowLedger) (bool, string, error) {
		now := time.Now().UTC()
		hasPending := false
		hasFailed := false
		for i := range ledger.Windows {
			slot := &ledger.Windows[i]
			if slot.State == "pending" && slot.LeaseToken == workerToken {
				if slot.ReservedAttempts >= activityReviewMaxWindowAttempts {
					slot.State = "failed"
					slot.ChunkReviews = failedActivityWindowReviews(slot.ChunkIDs)
				}
				slot.LeaseToken = ""
				slot.LeaseExpiresAt = nil
			}
			if slot.State == "pending" {
				if slot.ReservedAttempts >= activityReviewMaxWindowAttempts && (slot.LeaseExpiresAt == nil || !slot.LeaseExpiresAt.After(now)) {
					slot.State = "failed"
					slot.ChunkReviews = failedActivityWindowReviews(slot.ChunkIDs)
					slot.LeaseToken = ""
					slot.LeaseExpiresAt = nil
				} else {
					hasPending = true
				}
			}
			if slot.State == "failed" {
				hasFailed = true
			}
		}
		if hasPending {
			return true, "running", nil
		}
		provisional := mergeActivityLedgerIntoSummary(rows, stored, p, ledger, "completed")
		if hasFailed || len(provisional.Unassessed) > 0 {
			return true, "partial", nil
		}
		return true, "completed", nil
	})
}

func (w *Worker) releaseActivityReviewWorkerLeases(ctx context.Context, p activityReviewPayload, workerToken string) error {
	if w.DB == nil {
		return nil
	}
	return w.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session db.Session
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("profile_id = ? AND session_id = ?", p.ProfileID, p.SessionID).
			First(&session).Error; err != nil {
			return nil
		}
		var ledger activityReviewWindowLedger
		if len(session.ActivityReviewAttempts) == 0 ||
			json.Unmarshal(session.ActivityReviewAttempts, &ledger) != nil ||
			ledger.SourceVersion != p.SourceVersion ||
			ledger.MediaGeneration != p.Generation {
			return nil
		}
		changed := false
		for i := range ledger.Windows {
			slot := &ledger.Windows[i]
			if slot.LeaseToken == workerToken {
				slot.LeaseToken = ""
				slot.LeaseExpiresAt = nil
				changed = true
			}
		}
		if !changed {
			return nil
		}
		rawLedger, err := json.Marshal(ledger)
		if err != nil {
			return nil
		}
		return tx.Model(&session).Update("activity_review_attempts", db.NullableJSONDocument(rawLedger)).Error
	})
}

func truncateActivityReviewError(msg string) string {
	trimmed := strings.TrimSpace(msg)
	if len(trimmed) <= 512 {
		return trimmed
	}
	return trimmed[:512]
}
