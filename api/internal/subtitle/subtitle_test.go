package subtitle

import (
	"testing"

	"github.com/wod-strategist/api/internal/db"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSubtitle(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Subtitle Suite")
}

func pf(v float64) *float64 { return &v }

var _ = Describe("FormatSRT", func() {
	It("formats completed chunks sorted by start_secs", func() {
		chunks := []db.ChunkAnalysisResult{
			{ID: 1, Status: "COMPLETED", Output: "Second", StartSecs: pf(10), EndSecs: pf(20)},
			{ID: 2, Status: "COMPLETED", Output: "First", StartSecs: pf(0), EndSecs: pf(10)},
		}

		srt := FormatSRT(chunks)
		Expect(srt).To(ContainSubstring("1\n00:00:00,000 --> 00:00:10,000\nFirst\n"))
		Expect(srt).To(ContainSubstring("2\n00:00:10,000 --> 00:00:20,000\nSecond\n"))
	})

	It("skips non-COMPLETED chunks", func() {
		chunks := []db.ChunkAnalysisResult{
			{ID: 1, Status: "FAILED", Output: "bad", StartSecs: pf(0), EndSecs: pf(10)},
			{ID: 2, Status: "COMPLETED", Output: "good", StartSecs: pf(10), EndSecs: pf(20)},
		}

		srt := FormatSRT(chunks)
		Expect(srt).NotTo(ContainSubstring("bad"))
		Expect(srt).To(ContainSubstring("good"))
	})

	It("skips chunks without timestamps", func() {
		chunks := []db.ChunkAnalysisResult{
			{ID: 1, Status: "COMPLETED", Output: "no times"},
			{ID: 2, Status: "COMPLETED", Output: "has times", StartSecs: pf(0), EndSecs: pf(5)},
		}

		srt := FormatSRT(chunks)
		Expect(srt).NotTo(ContainSubstring("no times"))
		Expect(srt).To(ContainSubstring("has times"))
	})

	It("returns empty string when no eligible chunks", func() {
		chunks := []db.ChunkAnalysisResult{
			{ID: 1, Status: "FAILED", Output: "bad"},
		}
		Expect(FormatSRT(chunks)).To(BeEmpty())
	})

	It("returns empty string for nil input", func() {
		Expect(FormatSRT(nil)).To(BeEmpty())
	})
})

var _ = Describe("FormatSRTTime", func() {
	It("formats zero", func() {
		Expect(FormatSRTTime(0)).To(Equal("00:00:00,000"))
	})

	It("formats fractional seconds", func() {
		Expect(FormatSRTTime(1.5)).To(Equal("00:00:01,500"))
	})

	It("formats minutes", func() {
		Expect(FormatSRTTime(65.123)).To(Equal("00:01:05,123"))
	})

	It("formats hours", func() {
		Expect(FormatSRTTime(3661.0)).To(Equal("01:01:01,000"))
	})

	It("handles negative as zero", func() {
		Expect(FormatSRTTime(-5)).To(Equal("00:00:00,000"))
	})
})
