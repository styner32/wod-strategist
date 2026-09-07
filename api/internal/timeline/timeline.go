package timeline

import (
	"sort"

	"github.com/wod-strategist/api/internal/db"
)

// Chunk represents the time boundaries for a recorded video chunk in both capture and media clocks.
type Chunk struct {
	StartSecs      *float64
	EndSecs        *float64
	MediaStartSecs *float64
	MediaEndSecs   *float64
}

// SessionTimeline maps between human capture clock and video media clock for a session.
type SessionTimeline struct {
	SessionID string
	chunks    []Chunk
	valid     bool
}

// NewSessionTimeline creates a new SessionTimeline from a slice of Chunks.
//
// Each chunk is validated independently: it must have non-nil StartSecs,
// EndSecs, MediaStartSecs and MediaEndSecs with positive durations on both
// clocks. Chunks that fail are dropped, and the timeline is valid when at
// least one chunk survives.
//
// There is deliberately no global "does this session drift?" check. Migration
// 000038 backfilled media_* = start_* only for server-split rows
// (file_path LIKE '%/split_chunk_%'), whose two clocks legitimately coincide;
// unmapped mobile rows keep NULL and are dropped by the per-chunk validation
// above. Rejecting a session because media_end == end would therefore throw
// away correct identity mappings. This matches the per-interval rule used by
// filterObservationsWithChunks in the worker package.
func NewSessionTimeline(sessionID string, chunks []Chunk) *SessionTimeline {
	st := &SessionTimeline{
		SessionID: sessionID,
		chunks:    make([]Chunk, 0, len(chunks)),
		valid:     false,
	}

	if len(chunks) == 0 {
		return st
	}

	for _, c := range chunks {
		if c.StartSecs == nil || c.EndSecs == nil || c.MediaStartSecs == nil || c.MediaEndSecs == nil {
			continue
		}
		if *c.EndSecs <= *c.StartSecs || *c.MediaEndSecs <= *c.MediaStartSecs {
			continue
		}

		st.chunks = append(st.chunks, c)
	}

	if len(st.chunks) == 0 {
		return st
	}

	// Sort chunks by StartSecs ascending
	sort.Slice(st.chunks, func(i, j int) bool {
		return *st.chunks[i].StartSecs < *st.chunks[j].StartSecs
	})

	st.valid = true
	return st
}

// NewSessionTimelineFromAnalysisResults constructs a SessionTimeline from db.ChunkAnalysisResult slice.
func NewSessionTimelineFromAnalysisResults(sessionID string, results []db.ChunkAnalysisResult) *SessionTimeline {
	chunks := make([]Chunk, len(results))
	for i, r := range results {
		chunks[i] = Chunk{
			StartSecs:      r.StartSecs,
			EndSecs:        r.EndSecs,
			MediaStartSecs: r.MediaStartSecs,
			MediaEndSecs:   r.MediaEndSecs,
		}
	}
	return NewSessionTimeline(sessionID, chunks)
}

// IsValid returns whether this session timeline has a valid, non-backfilled media mapping.
func (st *SessionTimeline) IsValid() bool {
	return st != nil && st.valid
}

// CaptureToMedia converts a capture clock time in seconds to the corresponding merged media clock time.
// Returns (mediaSecs, true) if captureSecs falls within a recorded chunk.
// Returns (0, false) if the timeline is invalid, or if captureSecs falls into a chunk boundary gap or out of bounds.
//
// The two clocks are scaled within each chunk rather than offset by a constant.
// A chunk's capture window is wider than the video it produced: start_secs is
// stamped just before startRecording() and end_secs when onRecordingFinished
// arrives, so it also covers camera start-up and file finalisation, while
// media_* comes from the probed duration of the recorded file. Offsetting by
// (captureSecs - start) would therefore drift toward the end of every chunk and
// can return a time past MediaEndSecs, i.e. inside the next chunk's footage.
// Scaling is exact at both edges and always stays within the chunk's media
// interval; when the two durations happen to match it reduces to a plain offset.
func (st *SessionTimeline) CaptureToMedia(captureSecs float64) (float64, bool) {
	if !st.IsValid() {
		return 0, false
	}

	for _, c := range st.chunks {
		start := *c.StartSecs
		end := *c.EndSecs
		if captureSecs < start || captureSecs > end {
			continue
		}

		captureDur := end - start
		mediaStart := *c.MediaStartSecs
		mediaDur := *c.MediaEndSecs - mediaStart
		if captureDur <= 0 {
			continue
		}

		return mediaStart + (captureSecs-start)*(mediaDur/captureDur), true
	}

	// Not in any chunk (e.g. chunk boundary gap or outside recording range)
	return 0, false
}

// CaptureToMedia is a standalone package-level helper that calls st.CaptureToMedia(captureSecs).
func CaptureToMedia(st *SessionTimeline, captureSecs float64) (float64, bool) {
	if st == nil {
		return 0, false
	}
	return st.CaptureToMedia(captureSecs)
}
