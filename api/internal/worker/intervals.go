package worker

import (
	"sort"
)

// FocusEntry is one injury focus range parsed from ```injury_timestamps output.
type FocusEntry struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	Reason string `json:"reason"`
}

const (
	focusGapToleranceSecs  = 5.0 // Merge if gap between intervals is within this duration
	maxInjuryFocusSegments = 5   // Limit of injury Pro segment calls
)

// mergedInterval is a FocusEntry resolved to seconds with combined reasons.
type mergedInterval struct {
	StartSecs float64
	EndSecs   float64
	Reasons   []string
}

// toIntervals converts FocusEntry slice to mergedInterval slice without merging.
func toIntervals(entries []FocusEntry) []mergedInterval {
	intervals := make([]mergedInterval, 0, len(entries))
	for _, e := range entries {
		start := convertToSeconds(e.Start).Seconds()
		end := convertToSeconds(e.End).Seconds()
		if end <= start {
			continue
		}
		intervals = append(intervals, mergedInterval{
			StartSecs: start,
			EndSecs:   end,
			Reasons:   []string{e.Reason},
		})
	}
	return intervals
}

// mergeFocusIntervals sorts entries and unions overlapping/near-adjacent ranges.
// Reasons of merged entries are concatenated so the prompt keeps every finding.
func mergeFocusIntervals(entries []FocusEntry) []mergedInterval {
	items := make([]mergedInterval, 0, len(entries))
	for _, e := range entries {
		start := convertToSeconds(e.Start).Seconds()
		end := convertToSeconds(e.End).Seconds()
		if end <= start {
			continue
		}
		items = append(items, mergedInterval{StartSecs: start, EndSecs: end, Reasons: []string{e.Reason}})
	}
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartSecs < items[j].StartSecs })

	var merged []mergedInterval
	for _, it := range items {
		if len(merged) > 0 && it.StartSecs <= merged[len(merged)-1].EndSecs+focusGapToleranceSecs {
			last := &merged[len(merged)-1]
			if it.EndSecs > last.EndSecs {
				last.EndSecs = it.EndSecs
			}
			last.Reasons = append(last.Reasons, it.Reasons...)
			continue
		}
		merged = append(merged, it)
	}
	return merged
}

// capFocusIntervals keeps at most max intervals (longest first), chronological order.
func capFocusIntervals(intervals []mergedInterval, max int) []mergedInterval {
	if len(intervals) <= max {
		return intervals
	}
	byLen := append([]mergedInterval(nil), intervals...)
	sort.Slice(byLen, func(i, j int) bool {
		return (byLen[i].EndSecs - byLen[i].StartSecs) > (byLen[j].EndSecs - byLen[j].StartSecs)
	})
	kept := byLen[:max]
	sort.Slice(kept, func(i, j int) bool { return kept[i].StartSecs < kept[j].StartSecs })
	return kept
}
