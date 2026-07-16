package controllers

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("session re-analysis helpers", func() {
	It("whitelists only authoritative whole-session video objects", func() {
		objects := []string{
			"videos/1/session/A1B2C3D4.mp4",
			"videos/1/session/hardsubbed.mp4",
			"videos/1/session/hl_full_abcd.mp4",
			"videos/1/session/merged.mp4",
			"videos/1/session/analysis.mp4",
		}
		Expect(selectSessionReanalysisVideoObject(objects)).To(Equal("videos/1/session/analysis.mp4"))
		Expect(selectSessionReanalysisVideoObject(objects[:3])).To(BeEmpty())
	})

	It("supports known legacy whole-video filenames without accepting random chunks", func() {
		Expect(selectSessionReanalysisVideoObject([]string{
			"videos/P1-WOD-2026-01-01-12-00_random.mp4",
			"videos/P1-WOD-2026-01-01-12-00_merged_123.mp4",
		})).To(Equal("videos/P1-WOD-2026-01-01-12-00_merged_123.mp4"))
		Expect(selectSessionReanalysisVideoObject([]string{
			"videos/P1-WOD-2026-01-01-12-00_encoded.mp4",
		})).To(Equal("videos/P1-WOD-2026-01-01-12-00_encoded.mp4"))
	})

	It("returns safe candidate data without private storage metadata", func() {
		response := sessionReanalysisRunResponse(db.SessionReanalysisRun{
			ID: 1, SessionID: "session", Status: db.SessionReanalysisStatusCompleted,
			Output: "candidate", HighlightSegments: `[{"start":"0:00","end":"0:01"}]`,
			SessionScore: `{"overall":80}`, SourceGCSURI: "gs://private/video.mp4",
			GeminiFileURI: "https://private/files/1",
		})
		encoded, err := json.Marshal(response)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Candidate).NotTo(BeNil())
		Expect(string(encoded)).NotTo(ContainSubstring("private"))
	})

	It("accepts only a client request ID", func() {
		request, err := decodeCreateSessionReanalysisRequest(strings.NewReader(`{"client_request_id":"request-1"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(request.ClientRequestID).To(Equal("request-1"))
		_, err = decodeCreateSessionReanalysisRequest(strings.NewReader(`{"client_request_id":"request-2","gcs_uri":"gs://attacker/video.mp4"}`))
		Expect(err).To(HaveOccurred())
	})
})
