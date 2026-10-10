package timeline_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/timeline"
)

func ptr(f float64) *float64 {
	return &f
}

var _ = Describe("Session Timeline", func() {
	It("correctly maps capture to media in a valid drift session", func() {
		// Chunk 1: capture 0.0 ~ 10.0, media 0.0 ~ 10.0
		// 278ms gap between chunks
		// Chunk 2: capture 10.278 ~ 20.278, media 10.0 ~ 20.0
		// Chunk 3: capture 20.556 ~ 30.556, media 20.0 ~ 30.0
		// Total capture: 30.556, total media: 30.0 (drift = 0.556s > 0)
		chunks := []timeline.Chunk{
			{
				StartSecs:      ptr(0.0),
				EndSecs:        ptr(10.0),
				MediaStartSecs: ptr(0.0),
				MediaEndSecs:   ptr(10.0),
			},
			{
				StartSecs:      ptr(10.278),
				EndSecs:        ptr(20.278),
				MediaStartSecs: ptr(10.0),
				MediaEndSecs:   ptr(20.0),
			},
			{
				StartSecs:      ptr(20.556),
				EndSecs:        ptr(30.556),
				MediaStartSecs: ptr(20.0),
				MediaEndSecs:   ptr(30.0),
			},
		}

		st := timeline.NewSessionTimeline("session-1", chunks)
		Expect(st.IsValid()).To(BeTrue())

		// 1. Inside Chunk 1
		media, ok := st.CaptureToMedia(5.0)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 5.0, 1e-6))

		// 2. Chunk boundary gap between Chunk 1 and Chunk 2 (e.g. 10.150s)
		_, ok = st.CaptureToMedia(10.150)
		Expect(ok).To(BeFalse(), "expected ok=false in chunk boundary gap")

		// 3. Inside Chunk 2: capture 15.278s -> offset in chunk = 5.0s -> media 10.0 + 5.0 = 15.0s
		media, ok = st.CaptureToMedia(15.278)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 15.0, 1e-6))

		// 4. Chunk boundary gap between Chunk 2 and Chunk 3 (e.g. 20.400s)
		_, ok = st.CaptureToMedia(20.400)
		Expect(ok).To(BeFalse(), "expected ok=false in chunk boundary gap")

		// 5. Inside Chunk 3: capture 25.556s -> offset in chunk = 5.0s -> media 20.0 + 5.0 = 25.0s
		media, ok = st.CaptureToMedia(25.556)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 25.0, 1e-6))

		// 6. Before session start (< 0)
		_, ok = st.CaptureToMedia(-1.0)
		Expect(ok).To(BeFalse(), "expected ok=false before session start")

		// 7. After session end (> 30.556)
		_, ok = st.CaptureToMedia(31.0)
		Expect(ok).To(BeFalse(), "expected ok=false after session end")

		// 8. Package-level helper CaptureToMedia
		mediaHelper, okHelper := timeline.CaptureToMedia(st, 5.0)
		Expect(okHelper).To(BeTrue())
		Expect(mediaHelper).To(BeNumerically("~", 5.0, 1e-6))
	})

	It("supports server-split identity mapping", func() {
		chunks := []timeline.Chunk{
			{
				StartSecs:      ptr(0.0),
				EndSecs:        ptr(100.0),
				MediaStartSecs: ptr(0.0),
				MediaEndSecs:   ptr(100.0),
			},
		}

		st := timeline.NewSessionTimeline("server-split-session", chunks)
		Expect(st.IsValid()).To(BeTrue())

		media, ok := st.CaptureToMedia(50.0)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 50.0, 1e-6))
	})

	It("handles capture window wider than recorded video", func() {
		chunks := []timeline.Chunk{
			{StartSecs: ptr(0.0), EndSecs: ptr(10.278), MediaStartSecs: ptr(0.0), MediaEndSecs: ptr(10.0)},
			{StartSecs: ptr(10.278), EndSecs: ptr(20.556), MediaStartSecs: ptr(10.0), MediaEndSecs: ptr(20.0)},
		}

		st := timeline.NewSessionTimeline("wide-capture-session", chunks)
		Expect(st.IsValid()).To(BeTrue())

		media, ok := st.CaptureToMedia(0.0)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 0.0, 1e-6))

		media, ok = st.CaptureToMedia(10.278)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 10.0, 1e-6))

		media, ok = st.CaptureToMedia(20.556)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 20.0, 1e-6))

		// Midpoints scale proportionally.
		media, ok = st.CaptureToMedia(5.139)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 5.0, 1e-6))

		prev := -1.0
		for capture := 0.0; capture <= 20.556; capture += 0.101 {
			media, ok := st.CaptureToMedia(capture)
			Expect(ok).To(BeTrue(), "expected contiguous capture windows to be mapped, gap at %f", capture)
			Expect(media).To(BeNumerically(">=", 0))
			Expect(media).To(BeNumerically("<=", 20.0+1e-9))
			Expect(media).To(BeNumerically(">=", prev-1e-9), "mapping is not monotonic at capture %f: %f after %f", capture, media, prev)
			prev = media
		}
	})

	It("marks session invalid when media offsets are missing", func() {
		chunks := []timeline.Chunk{
			{
				StartSecs:      ptr(0.0),
				EndSecs:        ptr(50.0),
				MediaStartSecs: nil,
				MediaEndSecs:   nil,
			},
		}

		st := timeline.NewSessionTimeline("nil-media-session", chunks)
		Expect(st.IsValid()).To(BeFalse())
	})

	It("marks empty session invalid and returns ok=false", func() {
		st := timeline.NewSessionTimeline("empty-session", nil)
		Expect(st.IsValid()).To(BeFalse())
		_, ok := st.CaptureToMedia(10.0)
		Expect(ok).To(BeFalse())
	})

	It("builds timeline correctly from analysis results", func() {
		results := []db.ChunkAnalysisResult{
			{
				StartSecs:      ptr(0.0),
				EndSecs:        ptr(10.0),
				MediaStartSecs: ptr(0.0),
				MediaEndSecs:   ptr(10.0),
			},
			{
				StartSecs:      ptr(10.3),
				EndSecs:        ptr(20.3),
				MediaStartSecs: ptr(10.0),
				MediaEndSecs:   ptr(20.0),
			},
		}

		st := timeline.NewSessionTimelineFromAnalysisResults("session-results", results)
		Expect(st.IsValid()).To(BeTrue())

		media, ok := st.CaptureToMedia(15.3)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 15.0, 1e-6))
	})

	It("ignores nil chunks and retains valid ones", func() {
		chunks := []timeline.Chunk{
			{
				StartSecs:      ptr(0.0),
				EndSecs:        ptr(10.0),
				MediaStartSecs: ptr(0.0),
				MediaEndSecs:   ptr(10.0),
			},
			{
				StartSecs:      ptr(10.3),
				EndSecs:        ptr(20.3),
				MediaStartSecs: nil, // dangling/failed chunk
				MediaEndSecs:   nil,
			},
			{
				StartSecs:      ptr(20.6),
				EndSecs:        ptr(30.6),
				MediaStartSecs: ptr(10.0),
				MediaEndSecs:   ptr(20.0),
			},
		}

		st := timeline.NewSessionTimeline("mixed-session", chunks)
		Expect(st.IsValid()).To(BeTrue())

		// In chunk 1
		media, ok := st.CaptureToMedia(5.0)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 5.0, 1e-6))

		// In unmapped chunk 2 -> ok=false
		_, ok = st.CaptureToMedia(15.0)
		Expect(ok).To(BeFalse(), "expected ok=false in unmapped chunk")

		// In chunk 3
		media, ok = st.CaptureToMedia(25.6)
		Expect(ok).To(BeTrue())
		Expect(media).To(BeNumerically("~", 15.0, 1e-6))
	})
})
