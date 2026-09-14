package timeline_test

import (
	"testing"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/timeline"
)

func ptr(f float64) *float64 {
	return &f
}

func TestNewSessionTimeline_ValidDriftSession(t *testing.T) {
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
	if !st.IsValid() {
		t.Fatalf("expected timeline to be valid")
	}

	// 1. Inside Chunk 1
	media, ok := st.CaptureToMedia(5.0)
	if !ok || mathAbs(media-5.0) > 1e-6 {
		t.Errorf("expected 5.0, got %f (ok=%v)", media, ok)
	}

	// 2. Chunk boundary gap between Chunk 1 and Chunk 2 (e.g. 10.150s)
	media, ok = st.CaptureToMedia(10.150)
	if ok {
		t.Errorf("expected ok=false in chunk boundary gap, got media=%f", media)
	}

	// 3. Inside Chunk 2: capture 15.278s -> offset in chunk = 5.0s -> media 10.0 + 5.0 = 15.0s
	media, ok = st.CaptureToMedia(15.278)
	if !ok || mathAbs(media-15.0) > 1e-6 {
		t.Errorf("expected 15.0, got %f (ok=%v)", media, ok)
	}

	// 4. Chunk boundary gap between Chunk 2 and Chunk 3 (e.g. 20.400s)
	media, ok = st.CaptureToMedia(20.400)
	if ok {
		t.Errorf("expected ok=false in chunk boundary gap, got media=%f", media)
	}

	// 5. Inside Chunk 3: capture 25.556s -> offset in chunk = 5.0s -> media 20.0 + 5.0 = 25.0s
	media, ok = st.CaptureToMedia(25.556)
	if !ok || mathAbs(media-25.0) > 1e-6 {
		t.Errorf("expected 25.0, got %f (ok=%v)", media, ok)
	}

	// 6. Before session start (< 0)
	media, ok = st.CaptureToMedia(-1.0)
	if ok {
		t.Errorf("expected ok=false before session start, got media=%f", media)
	}

	// 7. After session end (> 30.556)
	media, ok = st.CaptureToMedia(31.0)
	if ok {
		t.Errorf("expected ok=false after session end, got media=%f", media)
	}

	// 8. Package-level helper CaptureToMedia
	mediaHelper, okHelper := timeline.CaptureToMedia(st, 5.0)
	if !okHelper || mathAbs(mediaHelper-5.0) > 1e-6 {
		t.Errorf("standalone helper failed: got %f (ok=%v)", mediaHelper, okHelper)
	}
}

func TestNewSessionTimeline_ServerSplitIdentityMapping(t *testing.T) {
	// Server-split rows carry offsets that are already relative to the uploaded
	// session video, so media_* == start_* is a correct identity mapping — not a
	// missing one. Migration 000038 backfilled exactly these rows; unmapped
	// mobile rows keep NULL and are rejected by TestNewSessionTimeline_MissingMediaOffsets.
	chunks := []timeline.Chunk{
		{
			StartSecs:      ptr(0.0),
			EndSecs:        ptr(100.0),
			MediaStartSecs: ptr(0.0),
			MediaEndSecs:   ptr(100.0),
		},
	}

	st := timeline.NewSessionTimeline("server-split-session", chunks)
	if !st.IsValid() {
		t.Fatalf("expected server-split session with media_end == end to be valid")
	}

	media, ok := st.CaptureToMedia(50.0)
	if !ok || mathAbs(media-50.0) > 1e-6 {
		t.Errorf("expected identity mapping 50.0, got %f (ok=%v)", media, ok)
	}
}

func TestCaptureToMedia_CaptureWindowWiderThanRecordedVideo(t *testing.T) {
	// The real shape produced by the mobile chunk recorder: start_secs is stamped
	// just before startRecording() and end_secs when onRecordingFinished arrives,
	// so each capture window is ~278ms wider than the video it produced, and
	// consecutive windows are contiguous (no gap between them).
	//
	// Chunk 1: capture [0.000, 10.278]  media [ 0.0, 10.0]
	// Chunk 2: capture [10.278, 20.556] media [10.0, 20.0]
	chunks := []timeline.Chunk{
		{StartSecs: ptr(0.0), EndSecs: ptr(10.278), MediaStartSecs: ptr(0.0), MediaEndSecs: ptr(10.0)},
		{StartSecs: ptr(10.278), EndSecs: ptr(20.556), MediaStartSecs: ptr(10.0), MediaEndSecs: ptr(20.0)},
	}

	st := timeline.NewSessionTimeline("wide-capture-session", chunks)
	if !st.IsValid() {
		t.Fatalf("expected timeline to be valid")
	}

	// A chunk's edges must land exactly on its media edges. A constant offset
	// would map the end of chunk 1 to 10.278 — past MediaEndSecs and into the
	// footage of chunk 2.
	if media, ok := st.CaptureToMedia(0.0); !ok || mathAbs(media-0.0) > 1e-6 {
		t.Errorf("chunk 1 start: expected 0.0, got %f (ok=%v)", media, ok)
	}
	if media, ok := st.CaptureToMedia(10.278); !ok || mathAbs(media-10.0) > 1e-6 {
		t.Errorf("chunk 1 end: expected 10.0, got %f (ok=%v)", media, ok)
	}
	if media, ok := st.CaptureToMedia(20.556); !ok || mathAbs(media-20.0) > 1e-6 {
		t.Errorf("chunk 2 end: expected 20.0, got %f (ok=%v)", media, ok)
	}

	// Midpoints scale proportionally.
	if media, ok := st.CaptureToMedia(5.139); !ok || mathAbs(media-5.0) > 1e-6 {
		t.Errorf("chunk 1 midpoint: expected 5.0, got %f (ok=%v)", media, ok)
	}

	// Every mapped time stays inside its chunk's media interval and increases
	// monotonically across the session.
	prev := -1.0
	for capture := 0.0; capture <= 20.556; capture += 0.101 {
		media, ok := st.CaptureToMedia(capture)
		if !ok {
			t.Fatalf("expected contiguous capture windows to be mapped, gap at %f", capture)
		}
		if media < 0 || media > 20.0+1e-9 {
			t.Errorf("capture %f mapped outside the media range: %f", capture, media)
		}
		if media < prev-1e-9 {
			t.Errorf("mapping is not monotonic at capture %f: %f after %f", capture, media, prev)
		}
		prev = media
	}
}

func TestNewSessionTimeline_MissingMediaOffsets(t *testing.T) {
	// Chunks with nil media_*
	chunks := []timeline.Chunk{
		{
			StartSecs:      ptr(0.0),
			EndSecs:        ptr(50.0),
			MediaStartSecs: nil,
			MediaEndSecs:   nil,
		},
	}

	st := timeline.NewSessionTimeline("nil-media-session", chunks)
	if st.IsValid() {
		t.Errorf("expected session with nil media_* to be invalid")
	}
}

func TestNewSessionTimeline_Empty(t *testing.T) {
	st := timeline.NewSessionTimeline("empty-session", nil)
	if st.IsValid() {
		t.Errorf("expected empty session to be invalid")
	}
	_, ok := st.CaptureToMedia(10.0)
	if ok {
		t.Errorf("expected ok=false for empty timeline")
	}
}

func TestNewSessionTimelineFromAnalysisResults(t *testing.T) {
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
	if !st.IsValid() {
		t.Fatalf("expected valid timeline from analysis results")
	}

	media, ok := st.CaptureToMedia(15.3)
	if !ok || mathAbs(media-15.0) > 1e-6 {
		t.Errorf("expected 15.0, got %f (ok=%v)", media, ok)
	}
}

func TestNewSessionTimeline_IgnoresNilChunksAndRetainsValid(t *testing.T) {
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
	if !st.IsValid() {
		t.Fatalf("expected valid timeline when valid chunks exhibit drift")
	}

	// In chunk 1
	media, ok := st.CaptureToMedia(5.0)
	if !ok || mathAbs(media-5.0) > 1e-6 {
		t.Errorf("expected 5.0, got %f (ok=%v)", media, ok)
	}

	// In unmapped chunk 2 -> ok=false
	_, ok = st.CaptureToMedia(15.0)
	if ok {
		t.Errorf("expected ok=false in unmapped chunk")
	}

	// In chunk 3
	media, ok = st.CaptureToMedia(25.6)
	if !ok || mathAbs(media-15.0) > 1e-6 {
		t.Errorf("expected 15.0, got %f (ok=%v)", media, ok)
	}
}

func mathAbs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
