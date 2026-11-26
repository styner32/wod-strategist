package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

type roundTripInspector struct {
	base      http.RoundTripper
	onRequest func(*http.Request)
}

func (r roundTripInspector) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.onRequest != nil {
		r.onRequest(req)
	}
	return r.base.RoundTrip(req)
}

func createRealTestMP4Bytes(t GinkgoTInterface, durationSecs int) []byte {
	filename := filepath.Join(t.TempDir(), fmt.Sprintf("sample_%ds.mp4", durationSecs))
	cmd := exec.Command(
		"ffmpeg", "-v", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=navy:s=64x64:r=4:d=%d", durationSecs),
		"-pix_fmt", "yuv420p",
		filename,
	)
	Expect(cmd.Run()).To(Succeed())
	data, err := os.ReadFile(filename)
	Expect(err).NotTo(HaveOccurred())
	return data
}

func createTinyTestJPEGBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(4, 4, color.RGBA{R: 220, G: 80, B: 40, A: 255})
	var buf bytes.Buffer
	Expect(jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})).To(Succeed())
	return buf.Bytes()
}

var _ = Describe("Activity review per-window retry and media CPU slots", Ordered, ContinueOnFailure, func() {
	var (
		conn             *gorm.DB
		w                *Worker
		transport        *testhelpers.MockTransport
		storageTransport *testhelpers.MockTransport
		recordedLogs     *observer.ObservedLogs
		profile          db.Profile
		session          db.Session
		queue            *asynq.Client
		observedBackoffs []time.Duration
		backoffMu        sync.Mutex
	)
	const base = "https://generativelanguage.googleapis.com"

	BeforeAll(func() {
		var err error
		conn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		if conn != nil {
			sqlDB, err := conn.DB()
			Expect(err).NotTo(HaveOccurred())
			Expect(sqlDB.Close()).To(Succeed())
		}
	})

	newTestWorker := func(rt http.RoundTripper) *Worker {
		core, logs := observer.New(zapcore.InfoLevel)
		recordedLogs = logs
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey:       "test-key",
			BaseURL:      base,
			HTTPClient:   &http.Client{Transport: rt},
			PollInterval: time.Millisecond,
			Sleep:        func(time.Duration) {},
		})
		Expect(err).NotTo(HaveOccurred())
		storageClient, err := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(err).NotTo(HaveOccurred())
		workerInstance := NewWorker(conn, storageClient, "test-bucket", client, queue, zap.New(core))
		workerInstance.ActivityCountingEnabled = true
		workerInstance.TrackingContinuityEnabled = true
		workerInstance.mediaLimiter = newMediaCPULimiter()
		workerInstance.activityReviewSleep = func(_ context.Context, d time.Duration) error {
			backoffMu.Lock()
			observedBackoffs = append(observedBackoffs, d)
			backoffMu.Unlock()
			return nil
		}
		return workerInstance
	}

	BeforeEach(func() {
		testhelpers.CleanupDB(conn)
		observedBackoffs = nil
		profile = testhelpers.CreateProfile(conn, &db.Profile{Name: "Retry Athlete"})
		session = testhelpers.CreateSession(conn, &db.Session{
			SessionID:   "WOD-20261106-01JWINDOWRETRY0000000001",
			ProfileID:   profile.ID,
			WorkoutType: "wod",
			Status:      db.SessionStatusStarted,
		})
		transport = testhelpers.NewMockTransport()
		storageTransport = testhelpers.NewMockTransport()
		queue = testhelpers.NewQueueClient()
		w = newTestWorker(transport)
	})

	AfterEach(func() {
		Expect(queue.Close()).To(Succeed())
	})

	seedTwoWindowSession := func() ([]db.ChunkAnalysisResult, string, string) {
		lowMotion := 0.005
		restObs, err := json.Marshal(activity.ConfirmedRest(10.0))
		Expect(err).NotTo(HaveOccurred())

		// Chunk 0: [0, 10] confirmed low-motion rest at session start -> auto-completed in ledger.
		s0, e0 := 0.0, 10.0
		c0 := testhelpers.CreateChunkAnalysisResult(conn, &db.ChunkAnalysisResult{
			ProfileID:            profile.ID,
			SessionID:            session.SessionID,
			FilePath:             fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_0.mp4", profile.ID, session.SessionID),
			Status:               "COMPLETED",
			ObservedSignals:      "{}",
			StartSecs:            &s0,
			EndSecs:              &e0,
			MediaStartSecs:       &s0,
			MediaEndSecs:         &e0,
			MotionScore:          &lowMotion,
			MovementObservations: restObs,
		})

		// Chunks 1..3: [10, 40] (3 contiguous active Air Squat chunks, hitting activityReviewMaxBatchChunks=3) -> Window 1: core (10, 40].
		refObject := fmt.Sprintf("videos/%d/%s/tracking/ref_10.jpg", profile.ID, session.SessionID)
		refURI := "gs://test-bucket/" + refObject
		cuesJSON, err := json.Marshal(map[string]any{
			"matched_cues":    []string{"black tee"},
			"ambiguous":       false,
			"confidence":      0.95,
			"station":         "center floor",
			"frame_region":    "center",
			"reference_image": refObject,
		})
		Expect(err).NotTo(HaveOccurred())

		makeSquatChunk := func(start, end float64, withRef bool) db.ChunkAnalysisResult {
			obs, oErr := json.Marshal(activity.Observations{
				Version:       1,
				DurationSecs:  10,
				TargetState:   "identified",
				ActivityState: "exercise",
				Events: []activity.Observation{
					{Movement: "Air Squat", Unit: "reps", Start: 2, End: 5, Complete: true, Evidence: "live rep"},
				},
				Unassessed: []activity.Interval{},
			})
			Expect(oErr).NotTo(HaveOccurred())
			cues := ""
			if withRef {
				cues = string(cuesJSON)
			}
			s, e := start, end
			return testhelpers.CreateChunkAnalysisResult(conn, &db.ChunkAnalysisResult{
				ProfileID:            profile.ID,
				SessionID:            session.SessionID,
				FilePath:             fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_%g.mp4", profile.ID, session.SessionID, start),
				Status:               "COMPLETED",
				ExerciseType:         "Air Squat",
				ObservedSignals:      "{}",
				TargetConfidence:     0.95,
				TargetCues:           cues,
				StartSecs:            &s,
				EndSecs:              &e,
				MediaStartSecs:       &s,
				MediaEndSecs:         &e,
				MovementObservations: obs,
			})
		}
		c1 := makeSquatChunk(10, 20, true)
		c2 := makeSquatChunk(20, 30, false)
		c3 := makeSquatChunk(30, 40, false)

		// Chunk 4: [40, 50] contiguous active Wallball Shot -> Window 2: core (40, 50], context [30, 50].
		s4, e4 := 40.0, 50.0
		obs4, err := json.Marshal(activity.Observations{
			Version:       1,
			DurationSecs:  10,
			TargetState:   "identified",
			ActivityState: "exercise",
			Events: []activity.Observation{
				{Movement: "Wallball Shot", Unit: "reps", Start: 2, End: 5, Complete: true, Evidence: "live wallball"},
			},
			Unassessed: []activity.Interval{},
		})
		Expect(err).NotTo(HaveOccurred())
		c4 := testhelpers.CreateChunkAnalysisResult(conn, &db.ChunkAnalysisResult{
			ProfileID:            profile.ID,
			SessionID:            session.SessionID,
			FilePath:             fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_40.mp4", profile.ID, session.SessionID),
			Status:               "COMPLETED",
			ExerciseType:         "Wallball Shot",
			ObservedSignals:      "{}",
			TargetConfidence:     0.95,
			StartSecs:            &s4,
			EndSecs:              &e4,
			MediaStartSecs:       &s4,
			MediaEndSecs:         &e4,
			MovementObservations: obs4,
		})

		rows := activity.CanonicalChunks([]db.ChunkAnalysisResult{c0, c1, c2, c3, c4})
		mergedObject := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, session.SessionID)
		return rows, mergedObject, refURI
	}

	It("retries only the failed window while reusing merged.mp4, extracted frame grids, and tracking images, and releases CPU slots before AI calls", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg not found in PATH")
		}
		var retrySlotHeldDuringAI atomic.Int32
		w = newTestWorker(roundTripInspector{
			base: transport,
			onRequest: func(req *http.Request) {
				backoffMu.Lock()
				inRetryPhase := len(observedBackoffs) > 0
				backoffMu.Unlock()
				if strings.Contains(req.URL.Path, ":generateContent") && inRetryPhase {
					if len(w.mediaLimiter.sharedMediaSlot) > 0 {
						retrySlotHeldDuringAI.Add(1)
					}
				}
			},
		})
		w.activityReviewSleep = func(_ context.Context, d time.Duration) error {
			// Wait briefly for the sibling window's initial FFmpeg extraction to finish so we can
			// assert that no window holds sharedMediaSlot across its Gemini HTTP calls or retries.
			Eventually(func() int { return len(w.mediaLimiter.sharedMediaSlot) }, 2*time.Second, 5*time.Millisecond).Should(Equal(0))
			backoffMu.Lock()
			observedBackoffs = append(observedBackoffs, d)
			backoffMu.Unlock()
			return nil
		}
		rows, mergedObject, refURI := seedTwoWindowSession()

		mp4Bytes := createRealTestMP4Bytes(GinkgoT(), 50)
		refBytes := createTinyTestJPEGBytes()

		// Exactly 1 merged.mp4 download and 1 tracking image download across both windows and all retries.
		testhelpers.MockGCSDownloadWithBody(storageTransport, "gs://test-bucket/"+mergedObject, mp4Bytes)
		testhelpers.MockGCSDownloadWithBody(storageTransport, refURI, refBytes)

		win1ReplyDoc, err := json.Marshal(activity.Observations{
			Version:       1,
			DurationSecs:  50,
			TargetState:   "identified",
			ActivityState: "exercise",
			Events: []activity.Observation{
				{Movement: "Air Squat", Unit: "reps", Start: 12.0, End: 15.0, Complete: true, Evidence: "rep 1"},
				{Movement: "Air Squat", Unit: "reps", Start: 22.0, End: 25.0, Complete: true, Evidence: "rep 2"},
			},
			Unassessed: []activity.Interval{},
		})
		Expect(err).NotTo(HaveOccurred())

		win2ReplyDoc, err := json.Marshal(activity.Observations{
			Version:       1,
			DurationSecs:  20,
			TargetState:   "identified",
			ActivityState: "exercise",
			Events: []activity.Observation{
				{Movement: "Wallball Shot", Unit: "reps", Start: 41.0, End: 43.0, Complete: true, Evidence: "wb 1"},
				{Movement: "Wallball Shot", Unit: "reps", Start: 44.0, End: 46.0, Complete: true, Evidence: "wb 2"},
				{Movement: "Wallball Shot", Unit: "reps", Start: 47.0, End: 49.0, Complete: true, Evidence: "wb 3"},
			},
			Unassessed: []activity.Interval{},
		})
		Expect(err).NotTo(HaveOccurred())

		// Window 1 succeeds on attempt 1 (never re-called).
		transport.New(base).
			Post("/v1beta/models/" + gemini.ModelHydrogen4 + ":generateContent").
			MatchBodyContains("(10.000000, 40.000000]").
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
					map[string]any{"text": "```movement_observations\n" + string(win1ReplyDoc) + "\n```"},
				}}}},
			})

		// Window 2 fails with 429 (Retry-After: 7) then 503 (default 15s), then succeeds on attempt 3.
		transport.New(base).
			Post("/v1beta/models/"+gemini.ModelHydrogen4+":generateContent").
			MatchBodyContains("(40.000000, 50.000000]").
			Reply(http.StatusTooManyRequests).
			Header("Retry-After", "7").
			JSON(map[string]any{"error": map[string]any{"code": 429, "message": "quota burst"}})

		transport.New(base).
			Post("/v1beta/models/" + gemini.ModelHydrogen4 + ":generateContent").
			MatchBodyContains("(40.000000, 50.000000]").
			Reply(http.StatusServiceUnavailable).
			JSON(map[string]any{"error": map[string]any{"code": 503, "message": "backend overloaded"}})

		transport.New(base).
			Post("/v1beta/models/" + gemini.ModelHydrogen4 + ":generateContent").
			MatchBodyContains("(40.000000, 50.000000]").
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
					map[string]any{"text": "```movement_observations\n" + string(win2ReplyDoc) + "\n```"},
				}}}},
			})

		payload, err := json.Marshal(activityReviewPayload{
			SessionID:     session.SessionID,
			ProfileID:     profile.ID,
			SourceVersion: activity.SourceVersion(rows),
			Object:        mergedObject,
			Generation:    1,
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(w.HandleActivityReviewTask(context.Background(), asynq.NewTask(TypeActivityReview, payload))).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(transport.Verify()).To(Succeed())
		Expect(retrySlotHeldDuringAI.Load()).To(BeZero(), "media CPU slot must be released before Gemini generateContent HTTP round-trip")
		Expect(len(w.mediaLimiter.sharedMediaSlot)).To(Equal(0))

		// Verify all 3 Window 2 requests sent the exact same extracted frame-grid + tracking image payload.
		var win2Bodies []string
		for _, req := range transport.Requests() {
			if strings.Contains(req.URL, ":generateContent") && strings.Contains(string(req.Body), "(40.000000, 50.000000]") {
				win2Bodies = append(win2Bodies, string(req.Body))
			}
		}
		Expect(win2Bodies).To(HaveLen(3))
		Expect(win2Bodies[1]).To(Equal(win2Bodies[0]))
		Expect(win2Bodies[2]).To(Equal(win2Bodies[0]))

		// Verify Retry-After (7s) took priority on attempt 1, followed by default 15s on attempt 2.
		Expect(observedBackoffs).To(Equal([]time.Duration{7 * time.Second, 15 * time.Second}))

		// Verify structured WARN logs recorded stage, attempt, reused_frames, and split latencies.
		warnEntries := recordedLogs.FilterMessage("activity review window attempt failed").All()
		Expect(warnEntries).To(HaveLen(2))
		Expect(warnEntries[0].Level).To(Equal(zapcore.WarnLevel))
		Expect(warnEntries[0].ContextMap()["stage"]).To(Equal("ai_call"))
		Expect(warnEntries[0].ContextMap()["attempt"]).To(Equal(int64(1)))
		Expect(warnEntries[0].ContextMap()["reused_frames"]).To(Equal(false))
		Expect(warnEntries[0].ContextMap()).To(HaveKey("wait_ms"))
		Expect(warnEntries[0].ContextMap()).To(HaveKey("extract_ms"))
		Expect(warnEntries[0].ContextMap()).To(HaveKey("ai_ms"))
		Expect(warnEntries[0].ContextMap()).To(HaveKey("parse_ms"))
		Expect(warnEntries[1].ContextMap()["attempt"]).To(Equal(int64(2)))
		Expect(warnEntries[1].ContextMap()["reused_frames"]).To(Equal(true))

		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		Expect(session.ActivitySummaryCacheVersion).To(Equal(activity.SummaryCacheVersion))
		var summary activity.Summary
		Expect(json.Unmarshal(session.ActivitySummary, &summary)).To(Succeed())
		Expect(summary.ReviewState).To(Equal("completed"))
		Expect(summary.Unassessed).To(BeEmpty())
		counts := map[string]int{}
		for _, m := range summary.Movements {
			counts[m.Movement] = m.Count
		}
		Expect(counts["Air Squat"]).To(Equal(2))
		Expect(counts["Wallball Shot"]).To(Equal(3))
	})

	It("deletes every owned Gemini file when Files polling fails before a successful upload retry", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg not found in PATH")
		}
		// An audio-only MP4 has a probeable duration but cannot produce frame grids,
		// exercising the real Files API fallback without mocking worker internals.
		localPath := filepath.Join(GinkgoT().TempDir(), "no-video-frames.mp4")
		Expect(exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i",
			"anullsrc=channel_layout=mono:sample_rate=8000", "-t", "10", "-vn", "-c:a", "aac", localPath).Run()).To(Succeed())
		mediaBytes, err := os.ReadFile(localPath)
		Expect(err).NotTo(HaveOccurred())

		start, end := 0.0, 10.0
		obs, err := json.Marshal(activity.Observations{
			Version:       activity.Version,
			DurationSecs:  end,
			TargetState:   "identified",
			ActivityState: "exercise",
			Events: []activity.Observation{
				{Movement: "Air Squat", Unit: "reps", Start: 2, End: 5, Complete: true, Evidence: "full squat"},
			},
			Unassessed: []activity.Interval{},
		})
		Expect(err).NotTo(HaveOccurred())
		chunk := testhelpers.CreateChunkAnalysisResult(conn, &db.ChunkAnalysisResult{
			ProfileID:            profile.ID,
			SessionID:            session.SessionID,
			FilePath:             fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_0.mp4", profile.ID, session.SessionID),
			Status:               "COMPLETED",
			ExerciseType:         "Air Squat",
			ObservedSignals:      "{}",
			StartSecs:            &start,
			EndSecs:              &end,
			MediaStartSecs:       &start,
			MediaEndSecs:         &end,
			MovementObservations: obs,
		})
		mergedObject := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, session.SessionID)
		testhelpers.MockGCSDownloadWithBody(storageTransport, "gs://test-bucket/"+mergedObject, mediaBytes)

		for _, name := range []string{"poll-failed", "retry-active"} {
			transport.New(base).Post("/upload/v1beta/files").Reply(http.StatusOK).
				Header("X-Goog-Upload-Url", base+"/upload-"+name).JSON(map[string]any{})
			transport.New(base).Post("/upload-"+name).Reply(http.StatusOK).
				Header("X-Goog-Upload-Status", "final").JSON(map[string]any{
				"file": map[string]any{"name": "files/" + name, "uri": base + "/files/" + name},
			})
			transport.New(base).Delete("/v1beta/files/" + name).Reply(http.StatusOK).JSON(map[string]any{})
		}
		// waitForFileActive exhausts its six transient polling attempts; the window
		// then retries with a new upload and must retain the first file for cleanup.
		for i := 0; i < 6; i++ {
			transport.New(base).Get("/v1beta/files/poll-failed").Reply(http.StatusServiceUnavailable).
				JSON(map[string]any{"error": map[string]any{"code": 503, "message": "poll temporarily unavailable"}})
		}
		transport.New(base).Get("/v1beta/files/retry-active").Reply(http.StatusOK).JSON(map[string]any{
			"name": "files/retry-active", "uri": base + "/files/retry-active", "state": "ACTIVE",
			"videoMetadata": map[string]any{"videoDuration": "10s"},
		})
		transport.New(base).Post("/v1beta/models/" + gemini.ModelHydrogen4 + ":generateContent").
			MatchBodyContains(base + "/files/retry-active").Reply(http.StatusOK).JSON(map[string]any{
			"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
				map[string]any{"text": "```movement_observations\n" + string(obs) + "\n```"},
			}}}},
		})

		payload, err := json.Marshal(activityReviewPayload{
			SessionID: session.SessionID, ProfileID: profile.ID,
			SourceVersion: activity.SourceVersion([]db.ChunkAnalysisResult{chunk}),
			Object:        mergedObject, Generation: 1,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(w.HandleActivityReviewTask(context.Background(), asynq.NewTask(TypeActivityReview, payload))).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(transport.Verify()).To(Succeed())
		Expect(observedBackoffs).To(Equal([]time.Duration{5 * time.Second}))
		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		var summary activity.Summary
		Expect(json.Unmarshal(session.ActivitySummary, &summary)).To(Succeed())
		Expect(summary.ReviewState).To(Equal("completed"))
	})

	It("enforces the durable 3-attempt cap across worker restart/redelivery and finalizes exhausted windows as partial", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg not found in PATH")
		}
		rows, mergedObject, refURI := seedTwoWindowSession()
		mp4Bytes := createRealTestMP4Bytes(GinkgoT(), 50)
		refBytes := createTinyTestJPEGBytes()

		win1ReplyDoc, err := json.Marshal(activity.Observations{
			Version:       1,
			DurationSecs:  50,
			TargetState:   "identified",
			ActivityState: "exercise",
			Events: []activity.Observation{
				{Movement: "Air Squat", Unit: "reps", Start: 12.0, End: 15.0, Complete: true, Evidence: "rep 1"},
				{Movement: "Air Squat", Unit: "reps", Start: 22.0, End: 25.0, Complete: true, Evidence: "rep 2"},
			},
			Unassessed: []activity.Interval{},
		})
		Expect(err).NotTo(HaveOccurred())

		payload, err := json.Marshal(activityReviewPayload{
			SessionID:     session.SessionID,
			ProfileID:     profile.ID,
			SourceVersion: activity.SourceVersion(rows),
			Object:        mergedObject,
			Generation:    1,
		})
		Expect(err).NotTo(HaveOccurred())
		task := asynq.NewTask(TypeActivityReview, payload)

		// Run 1: Window 1 succeeds; Window 2 reserves attempt 1, gets 503, and worker aborts during backoff (simulated restart).
		testhelpers.MockGCSDownloadWithBody(storageTransport, "gs://test-bucket/"+mergedObject, mp4Bytes)
		testhelpers.MockGCSDownloadWithBody(storageTransport, refURI, refBytes)
		transport.New(base).
			Post("/v1beta/models/" + gemini.ModelHydrogen4 + ":generateContent").
			MatchBodyContains("(10.000000, 40.000000]").
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
					map[string]any{"text": "```movement_observations\n" + string(win1ReplyDoc) + "\n```"},
				}}}},
			})
		transport.New(base).
			Post("/v1beta/models/" + gemini.ModelHydrogen4 + ":generateContent").
			MatchBodyContains("(40.000000, 50.000000]").
			Reply(http.StatusServiceUnavailable).
			JSON(map[string]any{"error": map[string]any{"code": 503, "message": "transient outage 1"}})

		w.activityReviewSleep = func(context.Context, time.Duration) error {
			return context.Canceled
		}
		Expect(w.HandleActivityReviewTask(context.Background(), task)).To(MatchError(context.Canceled))
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(transport.Verify()).To(Succeed())

		// Verify ledger persisted Window 1 completed + Window 2 pending with ReservedAttempts == 1 and cleared lease.
		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		var midLedger activityReviewWindowLedger
		Expect(json.Unmarshal(session.ActivityReviewAttempts, &midLedger)).To(Succeed())
		Expect(midLedger.Windows).To(HaveLen(2))
		Expect(midLedger.Windows[0].State).To(Equal("completed"))
		Expect(midLedger.Windows[0].ReservedAttempts).To(Equal(1))
		Expect(midLedger.Windows[1].State).To(Equal("pending"))
		Expect(midLedger.Windows[1].ReservedAttempts).To(Equal(1))
		Expect(midLedger.Windows[1].LeaseToken).To(BeEmpty())

		// Run 2 (redelivery after restart): Window 1 is NOT re-called; Window 2 gets ONLY 2 remaining attempts (total 3, never 9),
		// then finishes cleanly (nil error) in "partial" state preserving Window 1's counts.
		transport = testhelpers.NewMockTransport()
		storageTransport = testhelpers.NewMockTransport()
		w2 := newTestWorker(transport)

		testhelpers.MockGCSDownloadWithBody(storageTransport, "gs://test-bucket/"+mergedObject, mp4Bytes)
		testhelpers.MockGCSDownloadWithBody(storageTransport, refURI, refBytes)
		for attempt := 2; attempt <= 3; attempt++ {
			transport.New(base).
				Post("/v1beta/models/" + gemini.ModelHydrogen4 + ":generateContent").
				MatchBodyContains("(40.000000, 50.000000]").
				Reply(http.StatusServiceUnavailable).
				JSON(map[string]any{"error": map[string]any{"code": 503, "message": fmt.Sprintf("transient outage %d", attempt)}})
		}

		Expect(w2.HandleActivityReviewTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(transport.Verify()).To(Succeed())

		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		var finalLedger activityReviewWindowLedger
		Expect(json.Unmarshal(session.ActivityReviewAttempts, &finalLedger)).To(Succeed())
		Expect(finalLedger.Windows[0].State).To(Equal("completed"))
		Expect(finalLedger.Windows[0].ReservedAttempts).To(Equal(1))
		Expect(finalLedger.Windows[1].State).To(Equal("failed"))
		Expect(finalLedger.Windows[1].ReservedAttempts).To(Equal(3))

		var finalSummary activity.Summary
		Expect(json.Unmarshal(session.ActivitySummary, &finalSummary)).To(Succeed())
		Expect(finalSummary.ReviewState).To(Equal("partial"))
		Expect(session.ActivitySummaryCacheVersion).To(Equal(activity.SummaryCacheVersion))
		counts := map[string]int{}
		for _, m := range finalSummary.Movements {
			counts[m.Movement] = m.Count
		}
		Expect(counts["Air Squat"]).To(Equal(2), "completed Window 1 counts must be preserved")
		Expect(counts["Wallball Shot"]).To(Equal(1), "provisional live count remains for failed Window 2")
		Expect(finalSummary.Unassessed).To(ContainElement(SatisfyAll(
			HaveField("Reason", "review_failed"),
			HaveField("Movement", "Wallball Shot"),
		)))

		// Run 3 (duplicate delivery after budget exhaustion): 0 GCS downloads and 0 AI calls.
		transport = testhelpers.NewMockTransport()
		storageTransport = testhelpers.NewMockTransport()
		w3 := newTestWorker(transport)
		Expect(w3.HandleActivityReviewTask(context.Background(), task)).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(transport.Verify()).To(Succeed())
	})

	It("skips duplicate execution while another worker holds an active window lease and blocks stale archived publications", func() {
		rows, mergedObject, _ := seedTwoWindowSession()
		sv := activity.SourceVersion(rows)
		futureLease := time.Now().UTC().Add(2 * time.Minute)
		activeLedger := activityReviewWindowLedger{
			SourceVersion:   sv,
			MediaGeneration: 1,
			ReviewVersion:   activity.Version,
			Windows: []activityReviewWindowSlot{
				{
					Key:            fmt.Sprintf("%s:1:%d:0.000000:20.000000:10.000000:20.000000", sv, activity.Version),
					ChunkIDs:       []uint{rows[1].ID},
					Indices:        []int{1},
					WinStart:       0,
					WinEnd:         20,
					CoreStart:      10,
					CoreEnd:        20,
					Valid:          true,
					State:          "pending",
					LeaseToken:     "other-worker-token",
					LeaseExpiresAt: &futureLease,
				},
			},
		}
		rawLedger, err := json.Marshal(activeLedger)
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Model(&session).Update("activity_review_attempts", db.NullableJSONDocument(rawLedger)).Error).To(Succeed())

		payload, err := json.Marshal(activityReviewPayload{
			SessionID:     session.SessionID,
			ProfileID:     profile.ID,
			SourceVersion: sv,
			Object:        mergedObject,
			Generation:    1,
		})
		Expect(err).NotTo(HaveOccurred())
		// Active external lease: returns nil with zero GCS or Gemini requests.
		Expect(w.HandleActivityReviewTask(context.Background(), asynq.NewTask(TypeActivityReview, payload))).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(transport.Verify()).To(Succeed())

		// Archive the session's analysis_result -> HandleActivityReviewTask discards cancelled work cleanly (nil) without touching GCS/AI or publishing a summary.
		now := time.Now().UTC()
		testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{
			SessionID:                      session.SessionID,
			ProfileID:                      profile.ID,
			Status:                         "COMPLETED",
			ArchivedAt:                     &now,
			AutomaticProcessingCancelledAt: &now,
		})
		Expect(conn.Model(&session).Update("activity_review_attempts", nil).Error).To(Succeed())
		Expect(w.HandleActivityReviewTask(context.Background(), asynq.NewTask(TypeActivityReview, payload))).To(Succeed())
		Expect(storageTransport.Verify()).To(Succeed())
		Expect(transport.Verify()).To(Succeed())
		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		Expect(session.ActivitySummary).To(BeEmpty())
	})

	It("isolates the dedicated live-chunk CPU slot from contended shared media CPU tasks", func() {
		limiter := newMediaCPULimiter()
		testWorker := &Worker{
			mediaLimiter: limiter,
			logger:       zap.NewNop(),
		}
		baseCtx := withWorkerMediaRuntime(context.Background(), testWorker)

		shared1Entered := make(chan struct{})
		releaseShared1 := make(chan struct{})
		shared2Done := make(chan time.Duration, 1)
		var currentShared, maxShared atomic.Int32

		go func() {
			defer GinkgoRecover()
			_, err := runMediaExec(withMediaExecutionLane(baseCtx, mediaLaneShared), "hardsub", func(context.Context) error {
				cur := currentShared.Add(1)
				for {
					prev := maxShared.Load()
					if cur <= prev || maxShared.CompareAndSwap(prev, cur) {
						break
					}
				}
				close(shared1Entered)
				<-releaseShared1
				currentShared.Add(-1)
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		}()

		<-shared1Entered

		// Second shared-lane task must queue behind shared1 and record wait duration without failing.
		go func() {
			defer GinkgoRecover()
			trackedCtx, tracker := withMediaSlotWaitTracker(withMediaExecutionLane(baseCtx, mediaLaneShared))
			_, err := runMediaExec(trackedCtx, "extract_frame_grid", func(context.Context) error {
				cur := currentShared.Add(1)
				for {
					prev := maxShared.Load()
					if cur <= prev || maxShared.CompareAndSwap(prev, cur) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				currentShared.Add(-1)
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
			shared2Done <- tracker.WaitDuration()
		}()

		// Dedicated live-chunk lane must run immediately while shared lane is saturated!
		liveStart := time.Now()
		liveRan := false
		_, liveErr := runMediaExec(withMediaExecutionLane(baseCtx, mediaLaneLiveChunk), "prepare_chunk", func(execCtx context.Context) error {
			liveRan = true
			Expect(mediaThreadsLimited(execCtx)).To(BeTrue())
			return nil
		})
		Expect(liveErr).NotTo(HaveOccurred())
		Expect(liveRan).To(BeTrue())
		Expect(time.Since(liveStart)).To(BeNumerically("<", 100*time.Millisecond))

		time.Sleep(35 * time.Millisecond)
		close(releaseShared1)

		shared2Wait := <-shared2Done
		Expect(maxShared.Load()).To(Equal(int32(1)), "shared media CPU slot must never exceed concurrency 1")
		Expect(shared2Wait).To(BeNumerically(">=", 25*time.Millisecond))
	})
})
