package db

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
)

// IsSplitChunkFilePath reports whether filePath points to a server-generated
// split_chunk_* object rather than an original client-uploaded chunk.
func IsSplitChunkFilePath(filePath string) bool {
	if filePath == "" {
		return false
	}
	return strings.Contains(filePath, "/split_chunk_") || strings.HasPrefix(filepath.Base(filePath), "split_chunk_")
}

// ValidChunkInterval validates a [start, end) timestamp pair as finite, non-negative,
// and strictly positive in duration.
func ValidChunkInterval(start, end *float64) (float64, float64, bool) {
	if start == nil || end == nil {
		return 0, 0, false
	}
	s, e := *start, *end
	if math.IsNaN(s) || math.IsNaN(e) || math.IsInf(s, 0) || math.IsInf(e, 0) {
		return 0, 0, false
	}
	if s < 0 || e <= s {
		return 0, 0, false
	}
	return s, e, true
}

// ChunkMediaInterval returns a chunk's verified interval on the merged media clock.
// Capture-clock StartSecs/EndSecs are never used for original client chunks because
// recording pauses shift capture timestamps relative to merged.mp4. Server-generated
// split_chunk_* rows are already measured on the merged media clock.
func ChunkMediaInterval(chunk ChunkAnalysisResult) (float64, float64, bool) {
	if s, e, ok := ValidChunkInterval(chunk.MediaStartSecs, chunk.MediaEndSecs); ok {
		return s, e, true
	}
	if IsSplitChunkFilePath(chunk.FilePath) {
		return ValidChunkInterval(chunk.StartSecs, chunk.EndSecs)
	}
	return 0, 0, false
}

// MaxCompletedChunkMediaEndSecs returns the furthest verified media-clock end
// timestamp among COMPLETED chunks in the slice.
func MaxCompletedChunkMediaEndSecs(chunks []ChunkAnalysisResult) float64 {
	maxEnd := 0.0
	for _, chunk := range chunks {
		if !strings.EqualFold(chunk.Status, "COMPLETED") {
			continue
		}
		if _, end, ok := ChunkMediaInterval(chunk); ok && end > maxEnd {
			maxEnd = end
		}
	}
	return maxEnd
}

type floatSpan struct {
	start float64
	end   float64
}

// IntervalCoveredByChunks reports whether at least half of [startSecs, endSecs)
// is already covered by COMPLETED chunks on the merged media timeline.
func IntervalCoveredByChunks(startSecs, endSecs float64, chunks []ChunkAnalysisResult) bool {
	if math.IsNaN(startSecs) || math.IsNaN(endSecs) || math.IsInf(startSecs, 0) || math.IsInf(endSecs, 0) {
		return false
	}
	if startSecs < 0 || endSecs < startSecs {
		return false
	}

	duration := endSecs - startSecs
	if duration == 0 {
		for _, chunk := range chunks {
			if !strings.EqualFold(chunk.Status, "COMPLETED") {
				continue
			}
			if s, e, ok := ChunkMediaInterval(chunk); ok && startSecs >= s && startSecs < e {
				return true
			}
			if IsSplitChunkFilePath(chunk.FilePath) && chunk.StartSecs != nil && math.Abs(*chunk.StartSecs-startSecs) < 1e-6 {
				return true
			}
		}
		return false
	}

	var spans []floatSpan
	for _, chunk := range chunks {
		if !strings.EqualFold(chunk.Status, "COMPLETED") {
			continue
		}
		cStart, cEnd, ok := ChunkMediaInterval(chunk)
		if !ok {
			if IsSplitChunkFilePath(chunk.FilePath) && chunk.StartSecs != nil && math.Abs(*chunk.StartSecs-startSecs) < 1e-6 {
				return true
			}
			continue
		}
		lo := math.Max(startSecs, cStart)
		hi := math.Min(endSecs, cEnd)
		if hi > lo {
			spans = append(spans, floatSpan{start: lo, end: hi})
		}
	}
	if len(spans) == 0 {
		return false
	}

	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].end < spans[j].end
		}
		return spans[i].start < spans[j].start
	})

	covered := 0.0
	cur := spans[0]
	for _, sp := range spans[1:] {
		if sp.start <= cur.end {
			if sp.end > cur.end {
				cur.end = sp.end
			}
			continue
		}
		covered += cur.end - cur.start
		cur = sp
	}
	covered += cur.end - cur.start

	return covered >= 0.5*duration
}

// FilterRedundantSplitChunks removes duplicate per-file rows (keeping COMPLETED
// first, then lowest ID) and filters out server-generated split_chunk_* rows
// whose media interval is already covered by COMPLETED original mobile chunks
// (or lies past a fully mapped original concat timeline). Input ordering of
// retained rows is preserved.
func FilterRedundantSplitChunks(chunks []ChunkAnalysisResult) []ChunkAnalysisResult {
	if len(chunks) == 0 {
		return chunks
	}

	winnerByKey := make(map[string]int, len(chunks))
	for i, row := range chunks {
		trimmedPath := strings.TrimSpace(row.FilePath)
		if trimmedPath == "" {
			continue
		}
		key := fmt.Sprintf("%d\x00%s\x00%s", row.ProfileID, row.SessionID, trimmedPath)
		prevIdx, exists := winnerByKey[key]
		if !exists || preferChunkRow(row, chunks[prevIdx]) {
			winnerByKey[key] = i
		}
	}

	var completedOriginals []ChunkAnalysisResult
	originalCount := 0
	mappedOriginalCount := 0
	maxMappedOriginalEnd := 0.0

	for i, row := range chunks {
		if trimmedPath := strings.TrimSpace(row.FilePath); trimmedPath != "" {
			key := fmt.Sprintf("%d\x00%s\x00%s", row.ProfileID, row.SessionID, trimmedPath)
			if winnerByKey[key] != i {
				continue
			}
		}
		if IsSplitChunkFilePath(row.FilePath) {
			continue
		}
		originalCount++
		if _, mEnd, ok := ValidChunkInterval(row.MediaStartSecs, row.MediaEndSecs); ok {
			mappedOriginalCount++
			if mEnd > maxMappedOriginalEnd {
				maxMappedOriginalEnd = mEnd
			}
		}
		if strings.EqualFold(row.Status, "COMPLETED") {
			completedOriginals = append(completedOriginals, row)
		}
	}

	filtered := make([]ChunkAnalysisResult, 0, len(chunks))
	for i, row := range chunks {
		if trimmedPath := strings.TrimSpace(row.FilePath); trimmedPath != "" {
			key := fmt.Sprintf("%d\x00%s\x00%s", row.ProfileID, row.SessionID, trimmedPath)
			if winnerByKey[key] != i {
				continue
			}
		}

		if IsSplitChunkFilePath(row.FilePath) && originalCount > 0 {
			if sStart, sEnd, ok := ChunkMediaInterval(row); ok {
				if IntervalCoveredByChunks(sStart, sEnd, completedOriginals) {
					continue
				}
				if mappedOriginalCount == originalCount && maxMappedOriginalEnd > 0 && sStart >= maxMappedOriginalEnd {
					continue
				}
			}
		}

		filtered = append(filtered, row)
	}

	return filtered
}

func preferChunkRow(candidate, existing ChunkAnalysisResult) bool {
	candCompleted := strings.EqualFold(candidate.Status, "COMPLETED")
	existCompleted := strings.EqualFold(existing.Status, "COMPLETED")
	if candCompleted != existCompleted {
		return candCompleted
	}
	if candidate.ID > 0 && existing.ID > 0 && candidate.ID != existing.ID {
		return candidate.ID < existing.ID
	}
	return false
}
