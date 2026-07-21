package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var _ = Describe("Highlight verification prompt", func() {
	It("lists exact observations without presenting the padded parent range as evidence", func() {
		segments := []HighlightSegment{{
			Version:  2,
			Start:    "0:58",
			End:      "1:04",
			Type:     "mixed_form",
			Movement: "Snatch",
			Reason:   "parent summary",
			Observations: []HighlightObservation{
				{Start: "1:00.0", End: "1:00.2", Type: "form_issue", Reason: "early arm bend"},
				{Start: "1:01.1", End: "1:01.5", Type: "positive_form", Reason: "stable catch"},
			},
		}}

		prompt := buildVerificationPrompt(segments)

		Expect(prompt).To(ContainSubstring("event=0 observation=0 [1:00.0 ~ 1:00.2]"))
		Expect(prompt).To(ContainSubstring("event=0 observation=1 [1:01.1 ~ 1:01.5]"))
		Expect(prompt).To(ContainSubstring("Do not judge the padded parent playback range"))
		Expect(prompt).NotTo(ContainSubstring("[0:58 ~ 1:04]"))
	})
})

var _ = Describe("Highlight verification result parsing", func() {
	segments := []HighlightSegment{
		{Observations: []HighlightObservation{{}, {}}},
		{Observations: []HighlightObservation{{}}},
	}

	It("parses observation verdicts and drops malformed or out-of-range coordinates", func() {
		output := "```verification\n" + `[
  {"event_index":0,"observation_index":0,"verified":true,"reason":"visible"},
  {"event_index":0,"observation_index":1,"verified":false,"reason":"not visible"},
  {"event_index":2,"observation_index":0,"verified":true,"reason":"bad event"},
  {"event_index":1,"observation_index":9,"verified":true,"reason":"bad observation"},
  {"observation_index":0,"verified":true,"reason":"missing event"}
]` + "\n```"

		results, parsed := parseVerificationResults(output, segments)

		Expect(parsed).To(BeTrue())
		Expect(results).To(Equal([]VerificationResult{
			{EventIndex: 0, ObservationIndex: 0, Verified: true, Reason: "visible"},
			{EventIndex: 0, ObservationIndex: 1, Verified: false, Reason: "not visible"},
		}))
	})

	It("distinguishes a valid empty result from unparseable output", func() {
		results, parsed := parseVerificationResults("[]", segments)
		Expect(parsed).To(BeTrue())
		Expect(results).To(BeEmpty())

		results, parsed = parseVerificationResults("no verdicts here", segments)
		Expect(parsed).To(BeFalse())
		Expect(results).To(BeNil())
	})
})

var _ = Describe("Highlight observation verification", func() {
	It("removes rejected and missing observations and rebuilds the surviving parent", func() {
		confidence := 0.91
		segments := []HighlightSegment{
			{
				Version:  2,
				Start:    "0:58",
				End:      "1:05",
				Type:     "mixed_form",
				Movement: "Snatch",
				Reason:   "mixed evidence",
				Tags:     []string{"key_moment"},
				Observations: []HighlightObservation{
					{Start: "1:00.0", End: "1:00.4", Type: "positive_form", Reason: "stable catch", Confidence: &confidence},
					{Start: "1:01.0", End: "1:01.2", Type: "form_issue", Reason: "early arm bend"},
					{Start: "1:02.0", End: "1:02.3", Type: "technique_event", Reason: "bar transition"},
				},
			},
			{
				Version:  2,
				Start:    "2:00",
				End:      "2:05",
				Type:     "fatigue_point",
				Movement: "Snatch",
				Reason:   "fatigue",
				Observations: []HighlightObservation{
					{Start: "2:02.0", End: "2:02.3", Type: "fatigue_onset", Reason: "slower turnover"},
				},
			},
		}

		verified, allVerified := applyObservationVerification(segments, []VerificationResult{
			{EventIndex: 0, ObservationIndex: 0, Verified: true},
			{EventIndex: 0, ObservationIndex: 1, Verified: false},
			// Event 0 observation 2 and event 1 observation 0 are intentionally missing.
		}, HighlightNormalizeOptions{})

		Expect(allVerified).To(BeFalse())
		Expect(verified).To(HaveLen(1))
		Expect(verified[0].Type).To(Equal("best_form"))
		Expect(verified[0].Tags).To(BeEmpty())
		Expect(verified[0].Observations).To(HaveLen(1))
		Expect(verified[0].Observations[0].Start).To(Equal("1:00.0"))
		Expect(verified[0].Observations[0].End).To(Equal("1:00.4"))
		Expect(verified[0].Observations[0].Confidence).NotTo(BeNil())
		Expect(*verified[0].Observations[0].Confidence).To(Equal(0.91))
		Expect(verified[0].Observations[0].Verified).NotTo(BeNil())
		Expect(*verified[0].Observations[0].Verified).To(BeTrue())
		parentStart, err := parseTimestampToSeconds(verified[0].Start)
		Expect(err).NotTo(HaveOccurred())
		parentEnd, err := parseTimestampToSeconds(verified[0].End)
		Expect(err).NotTo(HaveOccurred())
		Expect(parentEnd - parentStart).To(BeNumerically("==", 5))
		Expect(parentEnd).To(BeNumerically("<", 65), "the rejected later evidence must no longer keep the old parent range")
	})

	It("sets the compatibility flag only when every observation is explicitly verified", func() {
		segments := []HighlightSegment{{
			Version: 2,
			Start:   "0:10",
			End:     "0:15",
			Type:    "mixed_form",
			Observations: []HighlightObservation{
				{Start: "0:11", End: "0:12", Type: "positive_form", Reason: "good"},
				{Start: "0:12", End: "0:13", Type: "form_issue", Reason: "issue"},
			},
		}}

		verified, allVerified := applyObservationVerification(segments, []VerificationResult{
			{EventIndex: 0, ObservationIndex: 0, Verified: true},
			{EventIndex: 0, ObservationIndex: 1, Verified: true},
		}, HighlightNormalizeOptions{})

		Expect(allVerified).To(BeTrue())
		Expect(verified).To(HaveLen(1))
		Expect(verified[0].Observations).To(HaveLen(2))
		Expect(verified[0].Observations[0].Verified).NotTo(BeNil())
		Expect(*verified[0].Observations[0].Verified).To(BeTrue())
		Expect(verified[0].Observations[1].Verified).NotTo(BeNil())
		Expect(*verified[0].Observations[1].Verified).To(BeTrue())
	})

	It("fails closed when duplicate verdicts conflict", func() {
		segments := []HighlightSegment{{
			Version: 2,
			Start:   "0:10",
			End:     "0:15",
			Type:    "best_form",
			Observations: []HighlightObservation{
				{Start: "0:11", End: "0:12", Type: "positive_form", Reason: "good"},
			},
		}}

		verified, allVerified := applyObservationVerification(segments, []VerificationResult{
			{EventIndex: 0, ObservationIndex: 0, Verified: true},
			{EventIndex: 0, ObservationIndex: 0, Verified: false},
		}, HighlightNormalizeOptions{})

		Expect(allVerified).To(BeFalse())
		Expect(verified).To(BeEmpty())
	})

	It("reapplies per-movement category limits after parent types change", func() {
		segments := []HighlightSegment{
			{
				Version: 2, Start: "0:08", End: "0:14", Type: "mixed_form", Movement: "Snatch",
				Observations: []HighlightObservation{
					{Start: "0:10", End: "0:11", Type: "positive_form", Reason: "first positive"},
					{Start: "0:11", End: "0:12", Type: "form_issue", Reason: "rejected issue"},
				},
			},
			{
				Version: 2, Start: "0:28", End: "0:33", Type: "best_form", Movement: "Snatch",
				Observations: []HighlightObservation{
					{Start: "0:30", End: "0:31", Type: "positive_form", Reason: "second positive"},
				},
			},
		}

		verified, allVerified := applyObservationVerification(segments, []VerificationResult{
			{EventIndex: 0, ObservationIndex: 0, Verified: true},
			{EventIndex: 0, ObservationIndex: 1, Verified: false},
			{EventIndex: 1, ObservationIndex: 0, Verified: true},
		}, HighlightNormalizeOptions{})

		Expect(allVerified).To(BeFalse())
		Expect(verified).To(HaveLen(1))
		Expect(verified[0].Type).To(Equal("best_form"))
	})

	It("splits surviving evidence when a rejected bridge no longer connects it", func() {
		segments := []HighlightSegment{{
			Version: 2, Start: "0:00", End: "0:12", Type: "mixed_form", Movement: "Clean",
			Tags: []string{"key_moment"},
			Observations: []HighlightObservation{
				{Start: "0:00", End: "0:01", Type: "positive_form", Reason: "setup"},
				{Start: "0:01", End: "0:10", Type: "technique_event", Reason: "unsupported bridge"},
				{Start: "0:10", End: "0:11", Type: "form_issue", Reason: "lockout issue"},
			},
		}}

		verified, allVerified := applyObservationVerification(segments, []VerificationResult{
			{EventIndex: 0, ObservationIndex: 0, Verified: true},
			{EventIndex: 0, ObservationIndex: 1, Verified: false},
			{EventIndex: 0, ObservationIndex: 2, Verified: true},
		}, HighlightNormalizeOptions{VideoEndSeconds: 20})

		Expect(allVerified).To(BeFalse())
		Expect(verified).To(HaveLen(2))
		Expect(verified[0].Type).To(Equal("best_form"))
		Expect(verified[1].Type).To(Equal("worst_form"))
		Expect(verified[0].Tags).To(BeEmpty())
		Expect(verified[1].Tags).To(BeEmpty())
	})
})

var _ = Describe("findSourceVideo", func() {
	const (
		bucketName = "test-bucket"
		sessionID  = "WOD-20260720-01JVERIFYVIDEO000000000"
		profileID  = uint(42)
	)

	It("uses the exact profile-aware canonical merged object without legacy discovery", func() {
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient(bucketName, transport)
		Expect(err).NotTo(HaveOccurred())
		worker := NewWorker(nil, storageClient, bucketName, nil, nil, zap.NewNop())
		canonical := fmt.Sprintf("videos/%d/%s/merged.mp4", profileID, sessionID)
		testhelpers.MockGCSListObjects(transport, bucketName, canonical, []string{
			canonical + ".tmp",
			canonical,
		})

		uri, err := worker.findSourceVideo(context.Background(), profileID, sessionID)

		Expect(err).NotTo(HaveOccurred())
		Expect(uri).To(Equal("gs://" + bucketName + "/" + canonical))
		Expect(transport.Verify()).To(Succeed())
	})

	It("checks bounded legacy layouts only after canonical absence", func() {
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient(bucketName, transport)
		Expect(err).NotTo(HaveOccurred())
		worker := NewWorker(nil, storageClient, bucketName, nil, nil, zap.NewNop())
		canonical := fmt.Sprintf("videos/%d/%s/merged.mp4", profileID, sessionID)
		canonicalPrefix := fmt.Sprintf("videos/%d/%s/", profileID, sessionID)
		legacyUploadPrefix := fmt.Sprintf("videos/0/%s/", sessionID)
		legacyObject := legacyUploadPrefix + "workout_merged_20260720.mp4"
		testhelpers.MockGCSListObjects(transport, bucketName, canonical, nil)
		testhelpers.MockGCSListObjects(transport, bucketName, canonicalPrefix, []string{
			canonicalPrefix + "hardsubbed.mp4",
			canonicalPrefix + "chunk_001.mp4",
		})
		testhelpers.MockGCSListObjects(transport, bucketName, legacyUploadPrefix, []string{legacyObject})

		uri, err := worker.findSourceVideo(context.Background(), profileID, sessionID)

		Expect(err).NotTo(HaveOccurred())
		Expect(uri).To(Equal("gs://" + bucketName + "/" + legacyObject))
		Expect(transport.Verify()).To(Succeed())
	})

	It("rejects chunks, encoded copies, hardsubs, highlights, and temporary objects", func() {
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient(bucketName, transport)
		Expect(err).NotTo(HaveOccurred())
		worker := NewWorker(nil, storageClient, bucketName, nil, nil, zap.NewNop())
		canonical := fmt.Sprintf("videos/%d/%s/merged.mp4", profileID, sessionID)
		canonicalPrefix := fmt.Sprintf("videos/%d/%s/", profileID, sessionID)
		legacyUploadPrefix := fmt.Sprintf("videos/0/%s/", sessionID)
		legacyFlatPrefix := fmt.Sprintf("videos/%s_", sessionID)
		testhelpers.MockGCSListObjects(transport, bucketName, canonical, []string{canonical + ".tmp"})
		testhelpers.MockGCSListObjects(transport, bucketName, canonicalPrefix, []string{
			canonicalPrefix + "chunk_001.mp4",
			canonicalPrefix + "encoded.mp4",
			canonicalPrefix + "hl_full_merged_preview.mp4",
			canonicalPrefix + "chunk_merged_preview.mp4",
			canonicalPrefix + "workout_encoded_merged_preview.mp4",
			canonicalPrefix + "session_hl_full_merged_preview.mp4",
			canonicalPrefix + "workout_merged_tmp.mp4",
		})
		testhelpers.MockGCSListObjects(transport, bucketName, legacyUploadPrefix, []string{
			legacyUploadPrefix + "workout_hardsub_merged_20260720.mp4",
		})
		testhelpers.MockGCSListObjects(transport, bucketName, legacyFlatPrefix, []string{
			legacyFlatPrefix + "workout_encoded.mp4",
		})

		uri, err := worker.findSourceVideo(context.Background(), profileID, sessionID)

		Expect(err).To(HaveOccurred())
		Expect(uri).To(BeEmpty())
		Expect(transport.Verify()).To(Succeed())
	})
})

var _ = Describe("HandleVerifyHighlightsTask", func() {
	const (
		geminiBaseURL = "https://generativelanguage.googleapis.com"
		geminiAPIKey  = "test-api-key"
		sessionID     = "WOD-20260720-01JVERIFYHANDLER0000000"
	)

	var database *gorm.DB

	BeforeEach(func() {
		var err error
		database, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(database)
	})

	It("persists rebuilt events and marks the legacy flag false when an observation is rejected", func() {
		profile := testhelpers.CreateProfile(database, &db.Profile{})
		expiresAt := time.Now().Add(time.Hour)
		segments := []HighlightSegment{{
			Version:  2,
			Start:    "0:08",
			End:      "0:15",
			Type:     "mixed_form",
			Movement: "Snatch",
			Reason:   "mixed evidence",
			Tags:     []string{"key_moment"},
			Observations: []HighlightObservation{
				{Start: "0:10.0", End: "0:10.2", Type: "positive_form", Reason: "visible-positive"},
				{Start: "0:11.0", End: "0:11.3", Type: "technique_event", Reason: "unsupported-technique"},
			},
		}}
		rawSegments, err := json.Marshal(segments)
		Expect(err).NotTo(HaveOccurred())
		analysis := testhelpers.CreateAnalysisResult(database, &db.AnalysisResult{
			SessionID:           sessionID,
			ProfileID:           profile.ID,
			AnalysisType:        db.AnalysisTypeWOD,
			Status:              "COMPLETED",
			Output:              "analysis",
			HighlightSegments:   string(rawSegments),
			GeminiFileURI:       geminiBaseURL + "/files/verify-source",
			GeminiFileName:      "files/verify-source",
			GeminiMIMEType:      "video/mp4",
			GeminiFileExpiresAt: &expiresAt,
		})

		geminiTransport := testhelpers.NewMockTransport()
		geminiClient, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey:     geminiAPIKey,
			BaseURL:    geminiBaseURL,
			HTTPClient: &http.Client{Transport: geminiTransport},
		})
		Expect(err).NotTo(HaveOccurred())
		geminiTransport.New(geminiBaseURL).
			Get("/v1beta/files/verify-source").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			Reply(http.StatusOK).
			JSON(map[string]any{
				"name": "files/verify-source", "state": "ACTIVE",
				"videoMetadata": map[string]any{"videoDuration": "20s"},
			})
		geminiTransport.New(geminiBaseURL).
			Post("/v1beta/models/"+gemini.ModelFlash35+":generateContent").
			MatchHeader("X-Goog-Api-Key", geminiAPIKey).
			MatchBodyContains("event=0 observation=0").
			MatchBodyContains("visible-positive").
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"parts": []map[string]any{{"text": "```verification\n" + `[
  {"event_index":0,"observation_index":0,"verified":true,"reason":"visible"},
  {"event_index":0,"observation_index":1,"verified":false,"reason":"not visible"}
]` + "\n```"}},
					},
				}},
			})

		worker := NewWorker(database, nil, "test-bucket", geminiClient, nil, zap.NewNop())
		task, err := NewVerifyHighlightsTask(sessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(worker.HandleVerifyHighlightsTask(context.Background(), task)).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())

		var persisted db.AnalysisResult
		Expect(database.First(&persisted, analysis.ID).Error).NotTo(HaveOccurred())
		Expect(persisted.Verified).NotTo(BeNil())
		Expect(*persisted.Verified).To(BeFalse())
		var persistedSegments []HighlightSegment
		Expect(json.Unmarshal([]byte(persisted.HighlightSegments), &persistedSegments)).To(Succeed())
		Expect(persistedSegments).To(HaveLen(1))
		Expect(persistedSegments[0].Type).To(Equal("best_form"))
		Expect(persistedSegments[0].Tags).To(BeEmpty())
		Expect(persistedSegments[0].Observations).To(HaveLen(1))
		Expect(persistedSegments[0].Observations[0].Reason).To(Equal("visible-positive"))
		Expect(persistedSegments[0].Observations[0].Verified).NotTo(BeNil())
		Expect(*persistedSegments[0].Observations[0].Verified).To(BeTrue())
	})
})
