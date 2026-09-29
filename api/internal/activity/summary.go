package activity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/wod-strategist/api/internal/db"
)

type Total struct {
	Movement string  `json:"movement"`
	Unit     string  `json:"unit"`
	Count    int     `json:"count"`
	Seconds  float64 `json:"seconds"`
}
type Gap struct {
	ChunkID uint     `json:"chunk_id,omitempty"`
	Clock   string   `json:"clock"`
	Start   *float64 `json:"start_secs"`
	End     *float64 `json:"end_secs"`
	Reason  string   `json:"reason"`
}
type ReviewedChunk struct {
	ChunkID      uint         `json:"chunk_id"`
	State        string       `json:"state"`
	Observations Observations `json:"observations"`
}
type Summary struct {
	MediaGeneration string          `json:"media_generation,omitempty"`
	Version         int             `json:"version"`
	Available       bool            `json:"available"`
	SourceVersion   string          `json:"source_version"`
	ReviewVersion   int             `json:"review_version"`
	ReviewState     string          `json:"review_state"`
	CoverageScope   string          `json:"coverage_scope"`
	Movements       []Total         `json:"movements"`
	Unassessed      []Gap           `json:"unassessed"`
	Reviews         []ReviewedChunk `json:"reviews"`
}

// CanonicalChunks agrees with original-result persistence: completed wins, then
// lowest ID. Different files with overlapping capture intervals remain conflicts.
func CanonicalChunks(rows []db.ChunkAnalysisResult) []db.ChunkAnalysisResult {
	rows = append([]db.ChunkAnalysisResult{}, rows...)
	sort.Slice(rows, func(i, j int) bool {
		if (rows[i].Status == "COMPLETED") != (rows[j].Status == "COMPLETED") {
			return rows[i].Status == "COMPLETED"
		}
		return rows[i].ID < rows[j].ID
	})
	seen := map[string]bool{}
	result := []db.ChunkAnalysisResult{}
	for _, row := range rows {
		key := fmt.Sprintf("%d/%s/%s", row.ProfileID, row.SessionID, row.FilePath)
		if row.FilePath == "" {
			key = fmt.Sprintf("row/%d", row.ID)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i].StartSecs, result[j].StartSecs
		if a == nil {
			return b == nil && result[i].ID < result[j].ID
		}
		if b == nil {
			return true
		}
		if *a == *b {
			return result[i].ID < result[j].ID
		}
		return *a < *b
	})
	return result
}

func SourceVersion(rows []db.ChunkAnalysisResult) string {
	type source struct {
		ID                               uint
		File, Status                     string
		Start, End, MediaStart, MediaEnd *float64
		Observations                     any
	}
	list := []source{}
	for _, row := range CanonicalChunks(rows) {
		var observations any
		_ = json.Unmarshal(row.MovementObservations, &observations)
		list = append(list, source{row.ID, row.FilePath, row.Status, row.StartSecs, row.EndSecs, row.MediaStartSecs, row.MediaEndSecs, observations})
	}
	raw, _ := json.Marshal(list)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func Timed(row db.ChunkAnalysisResult) bool {
	return row.StartSecs != nil && row.EndSecs != nil && ValidInterval(*row.StartSecs, *row.EndSecs)
}
func MediaTimed(row db.ChunkAnalysisResult) bool {
	return row.MediaStartSecs != nil && row.MediaEndSecs != nil && ValidInterval(*row.MediaStartSecs, *row.MediaEndSecs)
}

// Adjacent requires both clocks to be continuous. A capture pause must never
// disappear merely because ffmpeg concatenated the files. 1 ms covers rounding.
func Adjacent(a, b db.ChunkAnalysisResult) bool {
	return Timed(a) && Timed(b) && MediaTimed(a) && MediaTimed(b) &&
		abs(*a.EndSecs-*b.StartSecs) <= 0.001 && abs(*a.MediaEndSecs-*b.MediaStartSecs) <= 0.001
}
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func Build(rows []db.ChunkAnalysisResult, stored *Summary) Summary {
	rows = CanonicalChunks(rows)
	result := Summary{Version: Version, SourceVersion: SourceVersion(rows), ReviewVersion: Version, ReviewState: "provisional", CoverageScope: "recorded_chunks", Movements: []Total{}, Unassessed: []Gap{}, Reviews: []ReviewedChunk{}}
	reviewed := map[uint]ReviewedChunk{}
	if stored != nil && stored.SourceVersion == result.SourceVersion && stored.ReviewVersion == Version {
		result.MediaGeneration = stored.MediaGeneration
		result.ReviewState = stored.ReviewState
		result.Reviews = stored.Reviews
		for _, r := range stored.Reviews {
			reviewed[r.ChunkID] = r
		}
	}
	totals := map[string]Total{}
	previousEnd := 0.0
	for i, row := range rows {
		if len(row.MovementObservations) > 0 {
			result.Available = true
		}
		gap := func(reason string) {
			result.Unassessed = append(result.Unassessed, Gap{row.ID, "capture", row.StartSecs, row.EndSecs, reason})
		}
		if !Timed(row) {
			gap("unknown_capture_interval")
			continue
		}
		if *row.StartSecs > previousEnd+0.001 {
			s, e := previousEnd, *row.StartSecs
			result.Unassessed = append(result.Unassessed, Gap{Clock: "capture", Start: &s, End: &e, Reason: "capture_gap"})
		}
		if *row.EndSecs > previousEnd {
			previousEnd = *row.EndSecs
		}
		overlap := false
		for j, other := range rows {
			if i != j && Timed(other) && *row.StartSecs < *other.EndSecs && *row.EndSecs > *other.StartSecs {
				overlap = true
				break
			}
		}
		if overlap {
			gap("overlapping_sources")
			continue
		}
		var local Observations
		_ = json.Unmarshal(row.MovementObservations, &local)
		doc, err := Decode(row.MovementObservations, 0, local.DurationSecs)
		clock, offset := "chunk", 0.0
		if row.Status != "COMPLETED" {
			doc = Unknown(0, *row.EndSecs-*row.StartSecs, "analysis_incomplete")
			clock, offset = "capture", *row.StartSecs
			err = nil
		}
		if r, ok := reviewed[row.ID]; ok {
			if r.State == "completed" && MediaTimed(row) {
				doc = r.Observations
				clock = "media"
				offset = 0
				err = nil
			} else {
				gap("review_failed")
			}
		}
		if err != nil {
			gap("missing_or_invalid_observations")
			continue
		}
		for _, event := range doc.Events {
			key := event.Movement + "/" + event.Unit
			total := totals[key]
			total.Movement = event.Movement
			total.Unit = event.Unit
			if event.Unit == "reps" {
				total.Count++
			} else {
				total.Seconds += event.End - event.Start
			}
			totals[key] = total
		}
		for _, g := range doc.Unassessed {
			s, e := g.Start+offset, g.End+offset
			result.Unassessed = append(result.Unassessed, Gap{row.ID, clock, &s, &e, g.Reason})
		}
	}
	for _, total := range totals {
		result.Movements = append(result.Movements, total)
	}
	sort.Slice(result.Movements, func(i, j int) bool { return result.Movements[i].Movement < result.Movements[j].Movement })
	if !result.Available {
		result.ReviewState = "unavailable"
		result.Unassessed = []Gap{}
	}
	return result
}
