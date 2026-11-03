package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Read the exact bytes uploaded by the real GCS client so the downstream merge
// decodes the actual prepared artifact rather than a separately generated stand-in.
func recordingPipelineUploadedMedia(transport *testhelpers.MockTransport, object string) []byte {
	for _, request := range transport.Requests() {
		if request.Method != http.MethodPost || !strings.Contains(request.URL, "/upload/storage/v1/") {
			continue
		}
		mediaType, params, err := mime.ParseMediaType(request.Headers.Get("Content-Type"))
		Expect(err).NotTo(HaveOccurred())
		Expect(mediaType).To(Equal("multipart/related"))
		reader := multipart.NewReader(bytes.NewReader(request.Body), params["boundary"])
		var metadata struct {
			Name string `json:"name"`
		}
		var video []byte
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			body, err := io.ReadAll(part)
			Expect(err).NotTo(HaveOccurred())
			if strings.HasPrefix(part.Header.Get("Content-Type"), "application/json") {
				Expect(json.Unmarshal(body, &metadata)).To(Succeed())
			} else {
				video = body
			}
		}
		if metadata.Name == object {
			Expect(video).NotTo(BeEmpty())
			return video
		}
	}
	Fail("recording pipeline did not upload " + object)
	return nil
}

var _ = Describe("Recording media small pipeline integration", Ordered, func() {
	var database *gorm.DB
	var queue *asynq.Client
	var inspector *asynq.Inspector

	BeforeAll(func() {
		var err error
		database, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		queue = testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
		DeferCleanup(queue.Close)
		DeferCleanup(inspector.Close)
		sqlDB, err := database.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
	})
	BeforeEach(func() {
		testhelpers.CleanupDB(database)
		testhelpers.FlushRedis()
	})

	It("prepares two one-second sources, publishes their verified merge, and queues prepared AI input only after chunk results are terminal", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg is required for the two-second recording pipeline fixture")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		geminiClient, err := gemini.NewClientWithOptions(ctx, zap.NewNop(), gemini.Options{
			APIKey: "test-key", BaseURL: "https://generativelanguage.googleapis.com",
			HTTPClient: &http.Client{Transport: transport},
		})
		Expect(err).NotTo(HaveOccurred())
		w := NewWorker(database, storageClient, "test-bucket", geminiClient, queue, zap.NewNop())
		w.QueueInspector = inspector
		profile := testhelpers.CreateProfile(database, &db.Profile{})
		const sid = "WOD-20261103-01JRECORDINGPIPELINE000000"
		payload := VideoAnalysisPayload{SessionID: sid, ProfileID: profile.ID, WorkoutType: WorkoutTypeWOD, MergeRequestedAt: time.Now().UTC()}
		run := testhelpers.CreateRecordingMediaRun(database, &db.RecordingMediaRun{
			ProfileID: profile.ID, SessionID: sid, Version: db.RecordingMediaVersion,
			State: "RECORDING", Payload: recordingJSON(payload),
		})
		attrs := func(object string, generation, size int64) map[string]any {
			return map[string]any{"bucket": "test-bucket", "name": object, "generation": strconv.FormatInt(generation, 10),
				"metageneration": "1", "size": strconv.FormatInt(size, 10), "contentType": "video/mp4"}
		}
		mockUploadAndAttrs := func(object string, generation, size int64) {
			transport.New(testhelpers.GCSBaseURL).Post("/upload/storage/v1/b/test-bucket/o").
				MatchBodyContains(object).Reply(http.StatusOK).JSON(attrs(object, generation, size))
			transport.New(testhelpers.GCSBaseURL).Get("/storage/v1/b/test-bucket/o/" + url.PathEscape(object)).
				Reply(http.StatusOK).JSON(attrs(object, generation, size))
		}
		var chunks []db.RecordingChunk
		var preparations []db.ChunkMediaPreparation
		var preparedBodies [][]byte
		for i, color := range []string{"red", "blue"} {
			path := integrityTestVideo(ctx, GinkgoT().TempDir(), fmt.Sprintf("source-%d", i), "color=c="+color+":size=96x64:rate=30:duration=1")
			body, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			object := fmt.Sprintf("videos/%d/%s/chunk_%03d.mp4", profile.ID, sid, i+1)
			chunk := testhelpers.CreateRecordingChunk(database, &db.RecordingChunk{
				RunID: run.ID, CaptureOrder: i + 1, SourceObject: object, SourceURI: "gs://test-bucket/" + object,
				SourceGeneration: int64(101 + i), SourceBytes: int64(len(body)),
				CaptureStartSecs: float64(i * 10), CaptureEndSecs: float64((i + 1) * 10),
				AIType: TypeChunkAnalysis, AIPayload: recordingJSON(payload), AIDispatched: true,
			})
			prep := testhelpers.CreateChunkMediaPreparation(database, &db.ChunkMediaPreparation{ChunkID: chunk.ID})
			token, err := w.claimRecordingMedia(ctx, run, "chunk_media_preparations", prep.ID, prep.DispatchVersion, "PENDING")
			Expect(err).NotTo(HaveOccurred())
			output := fmt.Sprintf("videos/%d/%s/derived/chunks/%d/%s.mp4", profile.ID, sid, prep.ID, token)
			transport.New(testhelpers.GCSBaseURL).Get(fmt.Sprintf("/test-bucket/%s?generation=%d", object, chunk.SourceGeneration)).Reply(http.StatusOK).Body(body)
			mockUploadAndAttrs(output, int64(201+i), 1024)
			workCtx, done, err := w.recordingMediaContext(ctx, run, "chunk_media_preparations", prep.ID, token)
			Expect(err).NotTo(HaveOccurred())
			err = w.scopedSessionWorker(workCtx).prepareRecordingChunk(workCtx, run, prep, token)
			done()
			Expect(err).NotTo(HaveOccurred())
			Expect(database.First(&prep, prep.ID).Error).To(Succeed())
			Expect(prep.State).To(Equal("READY"))
			Expect(prep.ProfileKey).To(Equal("native"))
			signature, err := decodeFrameSignature(prep.Signature)
			Expect(err).NotTo(HaveOccurred())
			Expect(signature.FrameCount).To(Equal(30))
			preparedBodies = append(preparedBodies, recordingPipelineUploadedMedia(transport, output))
			chunks, preparations = append(chunks, chunk), append(preparations, prep)
			duplicate := asynq.NewTask(TypePrepareRecordingChunk, recordingJSON(recordingMediaTask{run.ID, prep.ID, prep.DispatchVersion}))
			Expect(w.HandlePrepareRecordingChunkTask(ctx, duplicate)).To(Succeed())
		}
		manifest := []db.RecordingMediaManifestEntry{{ChunkID: chunks[0].ID, SourceGeneration: chunks[0].SourceGeneration}, {ChunkID: chunks[1].ID, SourceGeneration: chunks[1].SourceGeneration}}
		raw := recordingJSON(manifest)
		digest := sha256.Sum256(raw)
		Expect(database.Model(&run).Updates(map[string]any{
			"state": "WAITING_PREPARATION", "manifest": raw, "manifest_hash": hex.EncodeToString(digest[:]),
		}).Error).To(Succeed())
		coordinate := asynq.NewTask(TypeCoordinateRecordingMedia, recordingJSON(recordingMediaTask{RunID: run.ID}))
		Expect(w.HandleCoordinateRecordingMediaTask(ctx, coordinate)).To(Succeed())
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.State).To(Equal("MERGING"))
		var preparationCount int64
		Expect(database.Model(&db.ChunkMediaPreparation{}).Where("run_id = ?", run.ID).Count(&preparationCount).Error).To(Succeed())
		Expect(preparationCount).To(Equal(int64(2)), "compatible native preparations must not be encoded again")

		token, err := w.claimRecordingMedia(ctx, run, "recording_media_runs", run.ID, run.DispatchVersion, "MERGING")
		Expect(err).NotTo(HaveOccurred())
		run.LeaseToken = token
		staging := fmt.Sprintf("videos/%d/%s/derived/merge/%d/%s.mp4", profile.ID, sid, run.ID, token)
		canonical := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, sid)
		for i, prep := range preparations {
			transport.New(testhelpers.GCSBaseURL).Get(fmt.Sprintf("/test-bucket/%s?generation=%d", prep.OutputObject, prep.OutputGeneration)).Reply(http.StatusOK).Body(preparedBodies[i])
		}
		mockUploadAndAttrs(staging, 301, 4096)
		transport.New(testhelpers.GCSBaseURL).Get("/storage/v1/b/test-bucket/o/" + url.PathEscape(canonical)).
			Reply(http.StatusNotFound).JSON(map[string]any{"error": map[string]any{"code": 404, "message": "not found"}})
		transport.New(testhelpers.GCSBaseURL).Post("/storage/v1/b/test-bucket/o/" + url.PathEscape(staging) +
			"/rewriteTo/b/test-bucket/o/" + url.PathEscape(canonical) + "?sourceGeneration=301&ifGenerationMatch=0").
			MatchBodyContains("media_run_id").MatchBodyContains(run.ManifestHash).Reply(http.StatusOK).
			JSON(map[string]any{"done": true, "resource": attrs(canonical, 302, 4096), "objectSize": "4096", "totalBytesRewritten": "4096"})
		mergeCtx, mergeDone, err := w.recordingMediaContext(ctx, run, "recording_media_runs", run.ID, token)
		Expect(err).NotTo(HaveOccurred())
		err = w.scopedSessionWorker(mergeCtx).mergeRecordingMedia(mergeCtx, run)
		mergeDone()
		Expect(err).NotTo(HaveOccurred())
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.State).To(Equal("MERGED"))
		Expect(run.MergedGeneration).To(Equal(int64(302)))
		var intervals []concatMediaInterval
		Expect(json.Unmarshal(run.Intervals, &intervals)).To(Succeed())
		Expect(intervals).To(HaveLen(2))
		Expect(intervals[0].Start).To(BeZero())
		Expect(intervals[0].End).To(BeNumerically("~", 1, 1.0/30000))
		Expect(intervals[1].Start).To(BeNumerically("~", 1, 1.0/30000))
		Expect(intervals[1].End).To(BeNumerically("~", 2, 1.0/30000))

		Expect(w.HandleCoordinateRecordingMediaTask(ctx, coordinate)).To(Succeed())
		var analysisPrep db.AnalysisVideoPreparation
		Expect(database.Where("run_id = ?", run.ID).First(&analysisPrep).Error).To(Succeed())
		prepareAnalysis := asynq.NewTask(TypePrepareAnalysisVideo, recordingJSON(recordingMediaTask{run.ID, analysisPrep.ID, analysisPrep.DispatchVersion}))
		Expect(w.HandlePrepareAnalysisVideoTask(ctx, prepareAnalysis)).To(Succeed())
		Expect(database.First(&analysisPrep, analysisPrep.ID).Error).To(Succeed())
		Expect(analysisPrep.State).To(Equal("READY"))
		Expect(analysisPrep.OutputObject).To(Equal(canonical))
		Expect(analysisPrep.OutputGeneration).To(Equal(int64(302)))
		Expect(database.First(&run, run.ID).Error).To(Succeed())
		Expect(run.AnalysisDispatched).To(BeFalse(), "full analysis waits for chunk AI terminal state")
		for _, chunk := range chunks {
			start, end := chunk.CaptureStartSecs, chunk.CaptureEndSecs
			testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
				SessionID: sid, ProfileID: profile.ID, FilePath: chunk.SourceURI, Status: "COMPLETED",
				StartSecs: &start, EndSecs: &end,
			})
		}
		Expect(w.HandleCoordinateRecordingMediaTask(ctx, coordinate)).To(Succeed())
		for i, chunk := range chunks {
			var result db.ChunkAnalysisResult
			Expect(database.Where("file_path = ?", chunk.SourceURI).First(&result).Error).To(Succeed())
			Expect(result.MediaStartSecs).NotTo(BeNil())
			Expect(result.MediaEndSecs).NotTo(BeNil())
			Expect(*result.MediaStartSecs).To(BeNumerically("~", i, 1.0/30000))
			Expect(*result.MediaEndSecs).To(BeNumerically("~", i+1, 1.0/30000))
		}
		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		var fullAnalysis []VideoAnalysisPayload
		for _, task := range pending {
			if task.Type == TypeVideoAnalysis {
				var body VideoAnalysisPayload
				Expect(json.Unmarshal(task.Payload, &body)).To(Succeed())
				fullAnalysis = append(fullAnalysis, body)
			}
		}
		Expect(fullAnalysis).To(HaveLen(1))
		Expect(fullAnalysis[0].MediaRunID).To(Equal(run.ID))
		Expect(fullAnalysis[0].AnalysisPreparationID).To(Equal(analysisPrep.ID))
		Expect(fullAnalysis[0].SourceGeneration).To(Equal(int64(302)))
		Expect(fullAnalysis[0].FilePath).To(Equal("gs://test-bucket/" + canonical))
		// A successful/ambiguously committed artifact is retained, while a
		// failed attempt may remove only its own unpublished object.
		w.cleanupRecordingAttemptObject(ctx, preparations[0].OutputObject, "chunk_media_preparations", preparations[0].ID, "output_object")
		unused := fmt.Sprintf("videos/%d/%s/derived/chunks/%d/unpublished.mp4", profile.ID, sid, preparations[0].ID)
		transport.New(testhelpers.GCSBaseURL).Delete("/storage/v1/b/test-bucket/o/" + url.PathEscape(unused)).Reply(http.StatusNoContent)
		w.cleanupRecordingAttemptObject(ctx, unused, "chunk_media_preparations", preparations[0].ID, "output_object")
		Expect(transport.Verify()).To(Succeed())
		for _, request := range transport.Requests() {
			Expect(request.URL).NotTo(ContainSubstring("generativelanguage.googleapis.com"))
		}
	})
})
