package timeline

import (
	"math"
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
// A timeline is considered valid only if:
// 1. There is at least one chunk.
// 2. Every chunk has non-nil StartSecs, EndSecs, MediaStartSecs, and MediaEndSecs with EndSecs > StartSecs and MediaEndSecs > MediaStartSecs.
// 3. The session exhibits genuine media clock drift: max(end_secs) > max(media_end_secs) + 0.001.
// If media_end == end, the session has no drift mapping (e.g. backfilled historical session), so the timeline is invalid.
func NewSessionTimeline(sessionID string, chunks []Chunk) *SessionTimeline {
	st := &SessionTimeline{
		SessionID: sessionID,
		chunks:    make([]Chunk, 0, len(chunks)),
		valid:     false,
	}

	if len(chunks) == 0 {
		return st
	}

	var maxEnd float64
	var maxMediaEnd float64
	hasEnd := false
	hasMediaEnd := false

	for _, c := range chunks {
		if c.StartSecs == nil || c.EndSecs == nil || c.MediaStartSecs == nil || c.MediaEndSecs == nil {
			continue
		}
		if *c.EndSecs <= *c.StartSecs || *c.MediaEndSecs <= *c.MediaStartSecs {
			continue
		}

		if !hasEnd || *c.EndSecs > maxEnd {
			maxEnd = *c.EndSecs
			hasEnd = true
		}
		if !hasMediaEnd || *c.MediaEndSecs > maxMediaEnd {
			maxMediaEnd = *c.MediaEndSecs
			hasMediaEnd = true
		}

		st.chunks = append(st.chunks, c)
	}

	// If no valid chunks, or if max media_end == max end (within 1ms tolerance) or maxMediaEnd >= maxEnd,
	// the session has no drift information (e.g. backfilled by migration 000038).
	if len(st.chunks) == 0 || !hasEnd || !hasMediaEnd || math.Abs(maxEnd-maxMediaEnd) < 0.001 || maxMediaEnd >= maxEnd {
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
func (st *SessionTimeline) CaptureToMedia(captureSecs float64) (float64, bool) {
	if !st.IsValid() {
		return 0, false
	}

	for _, c := range st.chunks {
		start := *c.StartSecs
		end := *c.EndSecs
		if captureSecs >= start && captureSecs <= end {
			offsetInChunk := captureSecs - start
			mediaSecs := *c.MediaStartSecs + offsetInChunk
			return mediaSecs, true
		}
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
