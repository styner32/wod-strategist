package controllers

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("chunk re-analysis response helpers", func() {
	It("accepts only a client request ID for enqueueing", func() {
		request, err := decodeCreateChunkReanalysisRequest(strings.NewReader(`{"client_request_id":"request-1"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(request.ClientRequestID).To(Equal("request-1"))

		_, err = decodeCreateChunkReanalysisRequest(strings.NewReader(`{"client_request_id":"request-2","model":"attacker-selected"}`))
		Expect(err).To(HaveOccurred())
		_, err = decodeCreateChunkReanalysisRequest(strings.NewReader(`{"client_request_id":"request-3","gcs_uri":"gs://attacker/video.mp4"}`))
		Expect(err).To(HaveOccurred())
	})

	It("uses the same analysis-grade session video as debug re-analysis for browser playback", func() {
		objects := []string{
			"videos/1/s/chunk_001.mp4",
			"videos/1/s/analysis.mp4",
			"videos/1/s/merged.mp4",
		}
		Expect(selectSessionVideoObject(objects, true)).To(Equal("videos/1/s/analysis.mp4"))
	})

	It("never treats an arbitrary retained mobile chunk or hardsub as a session video", func() {
		objects := []string{
			"videos/1/s/A1B2C3D4.mp4",
			"videos/1/s/hardsubbed.mp4",
			"videos/1/s/hl_full_abcd.mp4",
		}
		Expect(selectSessionVideoObject(objects, true)).To(BeEmpty())
		Expect(selectSessionVideoObject([]string{
			"videos/WOD-2026-01-01-12-00_encoded.mp4",
		}, true)).To(Equal("videos/WOD-2026-01-01-12-00_encoded.mp4"))
	})

	It("renders only safe candidate and token fields", func() {
		candidate, err := json.Marshal(ChunkReanalysisCandidateResponse{
			ExerciseType:    "Pull-up",
			Output:          "Keep the ribs down.",
			ObservedSignals: map[string]any{"rep_count": float64(3)},
		})
		Expect(err).NotTo(HaveOccurred())

		response := chunkReanalysisRunResponse(db.ChunkReanalysisRun{
			ID:                  7,
			Status:              db.ChunkReanalysisStatusCompleted,
			StructuredCandidate: db.JSONDocument(candidate),
			SourceGCSURI:        "gs://private/session.mp4",
			GeminiFileURI:       "https://private/files/1",
			PromptTokens:        10,
			CandidateTokens:     5,
			TotalTokens:         15,
		})
		encoded, err := json.Marshal(response)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Candidate).NotTo(BeNil())
		Expect(response.Candidate.ExerciseType).To(Equal("Pull-up"))
		Expect(response.TokenUsage.TotalTokens).To(Equal(int32(15)))
		Expect(string(encoded)).NotTo(ContainSubstring("private"))
	})

	It("rejects capture-clock timestamps as a merged-video interval", func() {
		start, end := 15.0, 25.0
		target := &chunkReanalysisTarget{StartSecs: &start, EndSecs: &end}
		Expect(validMediaInterval(target.MediaStartSecs, target.MediaEndSecs)).To(BeFalse())
	})
})
