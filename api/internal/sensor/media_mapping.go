package sensor

import (
	"errors"
	"math"
)

// ChunkTimeline defines a video chunk's mapping from capture clock to media clock.
// Capture clock interval is [CaptureStartMs, CaptureEndMs).
// Media clock interval is [MediaStartMs, MediaEndMs).
type ChunkTimeline struct {
	CaptureStartMs int64   `json:"capture_start_ms"`
	CaptureEndMs   int64   `json:"capture_end_ms"`
	MediaStartMs   float64 `json:"media_start_ms"`
	MediaEndMs     float64 `json:"media_end_ms"`
	IsFinal        bool    `json:"is_final"`
}

// CaptureToMedia maps a capture timestamp in ms to a media timestamp in seconds.
// Returns (mediaSeconds, ok).
// Rules:
// - Chunks are [start, end) intervals, except final chunk allows the exact end boundary.
// - Overlaps, reversals, non-finite values, and invalid boundaries return false.
// - If media_start + (capture - start) exceeds media_end by <= 50ms (0.05s), clamp to media_end.
// - If it exceeds media_end by > 50ms, return false.
// - Gaps between chunks return false.
func CaptureToMedia(captureMs int64, chunks []ChunkTimeline) (float64, bool) {
	if len(chunks) == 0 {
		return 0, false
	}

	// Validate chunk sequence
	for i := 0; i < len(chunks); i++ {
		c := chunks[i]
		if c.CaptureEndMs <= c.CaptureStartMs {
			return 0, false
		}
		if math.IsNaN(c.MediaStartMs) || math.IsNaN(c.MediaEndMs) || math.IsInf(c.MediaStartMs, 0) || math.IsInf(c.MediaEndMs, 0) {
			return 0, false
		}
		if c.MediaEndMs <= c.MediaStartMs {
			return 0, false
		}
		if i > 0 {
			prev := chunks[i-1]
			if c.CaptureStartMs < prev.CaptureEndMs {
				// Overlap
				return 0, false
			}
		}
	}

	for _, c := range chunks {
		inRange := false
		if c.IsFinal {
			inRange = (captureMs >= c.CaptureStartMs && captureMs <= c.CaptureEndMs)
		} else {
			inRange = (captureMs >= c.CaptureStartMs && captureMs < c.CaptureEndMs)
		}

		if inRange {
			offsetSec := float64(captureMs-c.CaptureStartMs) / 1000.0
			mappedMediaSec := c.MediaStartMs + offsetSec

			// Check exceed boundary
			diff := mappedMediaSec - c.MediaEndMs
			if diff > 0.050 { // > 50ms exceed
				return 0, false
			}
			if diff > 0 { // <= 50ms exceed: clamp
				mappedMediaSec = c.MediaEndMs
			}
			return mappedMediaSec, true
		}
	}

	// In gap or outside range
	return 0, false
}

// ValidateTimelineSequence validates that the chunk timelines form a valid monotonic sequence.
func ValidateTimelineSequence(chunks []ChunkTimeline) error {
	if len(chunks) == 0 {
		return errors.New("empty chunk timeline")
	}
	for i := 0; i < len(chunks); i++ {
		c := chunks[i]
		if c.CaptureEndMs <= c.CaptureStartMs {
			return errors.New("chunk capture end must be greater than start")
		}
		if c.MediaEndMs <= c.MediaStartMs {
			return errors.New("chunk media end must be greater than start")
		}
		if i > 0 {
			prev := chunks[i-1]
			if c.CaptureStartMs < prev.CaptureEndMs {
				return errors.New("chunks overlap in capture timeline")
			}
		}
	}
	return nil
}
