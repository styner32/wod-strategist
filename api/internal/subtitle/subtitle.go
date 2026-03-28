package subtitle

import (
	"fmt"
	"sort"
	"strings"

	"github.com/wod-strategist/api/internal/db"
)

// FormatSRT converts completed chunk analysis results into SRT subtitle format.
// Only chunks with status "COMPLETED" and valid start/end seconds are included.
// Results are sorted by StartSecs ascending.
func FormatSRT(chunks []db.ChunkAnalysisResult) string {
	type entry struct {
		start  float64
		end    float64
		output string
	}
	var entries []entry
	for _, ch := range chunks {
		if ch.Status != "COMPLETED" {
			continue
		}
		if ch.StartSecs == nil || ch.EndSecs == nil {
			continue
		}
		entries = append(entries, entry{
			start:  *ch.StartSecs,
			end:    *ch.EndSecs,
			output: ch.Output,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].start < entries[j].start
	})

	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("%d\n", i+1))
		b.WriteString(fmt.Sprintf("%s --> %s\n", FormatSRTTime(e.start), FormatSRTTime(e.end)))
		b.WriteString(e.output)
		b.WriteString("\n")
	}
	return b.String()
}

// FormatSRTTime converts seconds (float64) to SRT timecode format: HH:MM:SS,mmm
func FormatSRTTime(seconds float64) string {
	totalMs := int(seconds * 1000)
	if totalMs < 0 {
		totalMs = 0
	}
	h := totalMs / 3600000
	totalMs %= 3600000
	m := totalMs / 60000
	totalMs %= 60000
	s := totalMs / 1000
	ms := totalMs % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
