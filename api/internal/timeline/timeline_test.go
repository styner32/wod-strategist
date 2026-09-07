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

func TestNewSessionTimeline_BackfilledSession(t *testing.T) {
	// Session where media_end == end (e.g. backfilled historical session)
	chunks := []timeline.Chunk{
		{
			StartSecs:      ptr(0.0),
			EndSecs:        ptr(100.0),
			MediaStartSecs: ptr(0.0),
			MediaEndSecs:   ptr(100.0),
		},
	}

	st := timeline.NewSessionTimeline("backfilled-session", chunks)
	if st.IsValid() {
		t.Errorf("expected backfilled session with media_end == end to be invalid")
	}

	_, ok := st.CaptureToMedia(50.0)
	if ok {
		t.Errorf("expected ok=false for invalid timeline")
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
