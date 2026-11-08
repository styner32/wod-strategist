package activity

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("DecodeValidCachedSummary", func() {
	It("validates built summaries (available, zero-rep, unavailable archived, legacy unavailable) and strips Reviews", func() {
		row := source(1, 0, 10)
		sv := SourceVersion([]db.ChunkAnalysisResult{row})
		built := Build([]db.ChunkAnalysisResult{row}, &Summary{
			SourceVersion: sv,
			ReviewVersion: Version,
			ReviewState:   "completed",
			Reviews: []ReviewedChunk{
				{ChunkID: 1, State: "completed", Observations: Observations{Version: Version, DurationSecs: 10, TargetState: "identified", ActivityState: "exercise", Events: []Observation{cycle(1, 3)}, Unassessed: []Interval{}}},
			},
		})
		raw, err := json.Marshal(built)
		Expect(err).NotTo(HaveOccurred())

		decoded, ok := DecodeValidCachedSummary(raw)
		Expect(ok).To(BeTrue())
		Expect(decoded.Available).To(BeTrue())
		Expect(decoded.ReviewState).To(Equal("completed"))
		Expect(decoded.Movements).To(HaveLen(1))
		Expect(decoded.Movements[0].Count).To(Equal(1))
		Expect(decoded.Reviews).To(BeEmpty(), "Reviews must be stripped for GET /history reuse")

		// Legacy session (Available == false) built by activity.Build is also valid for cache reuse.
		legacyRow := source(2, 0, 10)
		legacyRow.MovementObservations = nil
		legacyBuilt := Build([]db.ChunkAnalysisResult{legacyRow}, nil)
		Expect(legacyBuilt.Available).To(BeFalse())
		legacyRaw, err := json.Marshal(legacyBuilt)
		Expect(err).NotTo(HaveOccurred())
		legacyDecoded, ok := DecodeValidCachedSummary(legacyRaw)
		Expect(ok).To(BeTrue())
		Expect(legacyDecoded.Available).To(BeFalse())
		Expect(legacyDecoded.ReviewState).To(Equal("unavailable"))
		Expect(legacyDecoded.Unassessed).To(BeEmpty())

		// Archived provisional session ("unavailable") remains valid when restored.
		unavailBuilt := Build([]db.ChunkAnalysisResult{row}, &Summary{
			SourceVersion: sv,
			ReviewVersion: Version,
			ReviewState:   "unavailable",
		})
		unavailRaw, err := json.Marshal(unavailBuilt)
		Expect(err).NotTo(HaveOccurred())
		unavailDecoded, ok := DecodeValidCachedSummary(unavailRaw)
		Expect(ok).To(BeTrue())
		Expect(unavailDecoded.ReviewState).To(Equal("unavailable"))
	})

	DescribeTable("rejects malformed or incomplete stored summaries",
		func(raw string) {
			_, ok := DecodeValidCachedSummary([]byte(raw))
			Expect(ok).To(BeFalse())
		},
		Entry("empty bytes", ``),
		Entry("partial stub with only review_state", `{"review_state":"provisional"}`),
		Entry("wrong version", `{"version":99,"review_version":1,"source_version":"abc","coverage_scope":"recorded_chunks","review_state":"completed","movements":[],"unassessed":[]}`),
		Entry("missing source_version", `{"version":1,"review_version":1,"source_version":"","coverage_scope":"recorded_chunks","review_state":"completed","movements":[],"unassessed":[]}`),
		Entry("unknown review_state", `{"version":1,"review_version":1,"source_version":"abc","coverage_scope":"recorded_chunks","review_state":"mystery","movements":[],"unassessed":[]}`),
		Entry("null movements array", `{"version":1,"review_version":1,"source_version":"abc","coverage_scope":"recorded_chunks","review_state":"completed","movements":null,"unassessed":[]}`),
	)
})
