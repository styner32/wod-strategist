package subtitle

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/wod-strategist/api/internal/db"
)

// segmentHeaderRegex matches the segment header format from two-pass analysis:
// "## 세그먼트 N: ExerciseName (start ~ end)"
var segmentHeaderRegex = regexp.MustCompile(`(?m)^##\s*세그먼트\s+\d+:\s*(.+?)\s*\((\d+:\d{2})\s*~\s*(\d+:\d{2})\)`)

// bulletRegex matches Korean/English bullet points (-, *, •, numbered) with content.
var bulletRegex = regexp.MustCompile(`(?m)^\s*(?:[-*•]|\d+[.)]\s*)\s*(.+)$`)

// boldRegex matches **bold** text.
var boldRegex = regexp.MustCompile(`\*\*(.+?)\*\*`)

// sectionHeaderRegex matches markdown section headers (###, ####).
var sectionHeaderRegex = regexp.MustCompile(`(?m)^#{3,4}\s+(.+)$`)

// codeBlockRegex matches fenced code blocks (```...```) for stripping.
var codeBlockRegex = regexp.MustCompile("(?s)```.*?```")

// srtEntry is a generic subtitle entry with a time window and text.
type srtEntry struct {
	start float64
	end   float64
	text  string
}

// FormatFinalAnalysisSRT converts the final two-pass analysis output into SRT
// subtitle format. It parses segment headers to extract time ranges, then
// extracts key feedback bullet points from each segment and spreads them as
// individual subtitle entries across the segment's time window.
//
// Each feedback point gets its own subtitle entry, so if a segment has 5 points
// of feedback, the segment's time window is divided into 5 equal sub-windows.
func FormatFinalAnalysisSRT(analysisOutput string) string {
	entries := buildFinalAnalysisEntries(analysisOutput)
	return renderSRT(entries)
}

// FormatMixedSRT merges final analysis feedback with chunk analysis feedback.
//
// Final analysis entries (with ✅/⚠️ markers) take priority and cover their
// segment time ranges. Chunk analysis entries fill time gaps where no final
// analysis segment exists (rest periods, transitions, unparsed sections).
//
// This ensures the video always has subtitle coverage:
//   - Segments with deep analysis → pro/con feedback from the final result
//   - Gaps between segments → real-time chunk coaching cues
func FormatMixedSRT(analysisOutput string, chunks []db.ChunkAnalysisResult) string {
	finalEntries := buildFinalAnalysisEntries(analysisOutput)
	chunkEntries := buildChunkEntries(chunks)

	// If no final analysis entries, fall back to chunk-only SRT.
	if len(finalEntries) == 0 {
		return renderSRT(chunkEntries)
	}

	// Merge: keep all final analysis entries, add chunk entries only for gaps.
	merged := append([]srtEntry{}, finalEntries...)
	for _, ch := range chunkEntries {
		if !overlapsAnyFinalSegment(ch, finalEntries) {
			merged = append(merged, ch)
		}
	}

	// Sort by start time for correct SRT ordering.
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].start < merged[j].start
	})

	return renderSRT(merged)
}

// overlapsAnyFinalSegment returns true if the chunk entry's time range overlaps
// with any of the final analysis entries. We use a small tolerance (0.5s) to
// avoid edge-case gaps at segment boundaries.
func overlapsAnyFinalSegment(ch srtEntry, finalEntries []srtEntry) bool {
	if len(finalEntries) == 0 {
		return false
	}
	// Find the continuous coverage spans from final entries.
	// Final entries from the same segment are contiguous, so we check against
	// the min start and max end of the final entries in the overlapping range.
	const tolerance = 0.5
	for _, fe := range finalEntries {
		// Two intervals overlap if start1 < end2 AND start2 < end1
		if ch.start < fe.end-tolerance && fe.start < ch.end-tolerance {
			return true
		}
	}
	return false
}

// buildFinalAnalysisEntries parses the final analysis output and returns
// subtitle entries with ✅/⚠️ markers spread across each segment's time window.
func buildFinalAnalysisEntries(analysisOutput string) []srtEntry {
	segments := parseAnalysisSegments(analysisOutput)
	if len(segments) == 0 {
		return nil
	}

	var entries []srtEntry
	for _, seg := range segments {
		startSecs := parseMmSsToSeconds(seg.start)
		endSecs := parseMmSsToSeconds(seg.end)
		if endSecs <= startSecs {
			continue
		}

		points := extractFeedbackPoints(seg.body)
		if len(points) == 0 {
			continue
		}

		// Spread feedback points evenly across the segment's time window.
		segDuration := endSecs - startSecs
		pointDuration := segDuration / float64(len(points))
		// Minimum display time per subtitle: 3 seconds
		if pointDuration < 3.0 {
			pointDuration = 3.0
		}

		for i, pt := range points {
			ptStart := startSecs + float64(i)*pointDuration
			ptEnd := ptStart + pointDuration
			if ptEnd > endSecs {
				ptEnd = endSecs
			}
			if ptStart >= endSecs {
				break
			}

			text := truncateSubtitle(pt, 80)
			entries = append(entries, srtEntry{start: ptStart, end: ptEnd, text: text})
		}
	}
	return entries
}

// buildChunkEntries converts chunk analysis results into subtitle entries.
//
// Long chunk outputs are split into multiple shorter entries at sentence
// boundaries and spread across the chunk's time window, avoiding truncation.
func buildChunkEntries(chunks []db.ChunkAnalysisResult) []srtEntry {
	var entries []srtEntry
	for _, ch := range chunks {
		if ch.Status != "COMPLETED" {
			continue
		}
		if ch.StartSecs == nil || ch.EndSecs == nil {
			continue
		}
		output := strings.TrimSpace(ch.Output)
		// Defense-in-depth: strip any residual code blocks (```...```)
		// in case the chunk was saved before observed_signals stripping
		// was deployed, or the model emits unexpected fenced blocks.
		output = codeBlockRegex.ReplaceAllString(output, "")
		output = strings.TrimSpace(output)
		if output == "" {
			continue
		}

		// Prepend BPM badge for hardsub overlay when heart rate data is available
		if ch.HeartRateBPM > 0 {
			output = fmt.Sprintf("[%d bpm] %s", ch.HeartRateBPM, output)
		}

		start := *ch.StartSecs
		end := *ch.EndSecs
		if end <= start {
			continue
		}

		// Split long text into sentences/clauses for separate subtitle entries.
		sentences := splitIntoSentences(output)
		if len(sentences) == 0 {
			continue
		}

		duration := end - start
		slotDuration := duration / float64(len(sentences))
		if slotDuration < 3.0 {
			slotDuration = 3.0
		}

		for i, sent := range sentences {
			sentStart := start + float64(i)*slotDuration
			sentEnd := sentStart + slotDuration
			if sentEnd > end {
				sentEnd = end
			}
			if sentStart >= end {
				break
			}
			entries = append(entries, srtEntry{
				start: sentStart,
				end:   sentEnd,
				text:  truncateSubtitle(sent, 80),
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].start < entries[j].start
	})
	return entries
}

// splitIntoSentences splits text into sentence-level chunks suitable for subtitles.
// Splits on Korean sentence endings (다, 요, 음), periods, and parenthetical blocks.
// Each resulting sentence is trimmed and non-empty.
func splitIntoSentences(text string) []string {
	// Split on common sentence delimiters:
	// - Period followed by space/end
	// - Korean sentence-ending patterns: "다." "요." "음)" "다,"
	// - Semicolons, newlines
	var sentences []string
	var current strings.Builder

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		current.WriteRune(runes[i])

		isSplit := false
		switch runes[i] {
		case '.', '。':
			// Period: split if followed by space, end, or Korean char
			if i+1 >= len(runes) || runes[i+1] == ' ' || runes[i+1] == '\n' {
				isSplit = true
			}
		case ';', '\n':
			isSplit = true
		case ')':
			// Close paren: split if current segment is long enough
			if current.Len() > 20 {
				isSplit = true
			}
		case ',':
			// Comma: only split if current segment is already long (>30 chars)
			if len([]rune(current.String())) > 30 {
				isSplit = true
			}
		}

		if isSplit {
			sent := trimLeadingPunctuation(strings.TrimSpace(current.String()))
			if len([]rune(sent)) >= 5 {
				sentences = append(sentences, sent)
			}
			current.Reset()
		}
	}

	// Remaining text
	if remaining := trimLeadingPunctuation(strings.TrimSpace(current.String())); len([]rune(remaining)) >= 5 {
		sentences = append(sentences, remaining)
	}

	// If splitting produced nothing useful, return the original as one entry
	if len(sentences) == 0 && len([]rune(text)) >= 5 {
		sentences = []string{text}
	}

	return sentences
}

// trimLeadingPunctuation strips leading commas, semicolons, and spaces from text.
func trimLeadingPunctuation(s string) string {
	return strings.TrimLeft(s, ", ;\t")
}

// renderSRT formats a sorted list of subtitle entries into SRT format.
func renderSRT(entries []srtEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("%d\n", i+1))
		b.WriteString(fmt.Sprintf("%s --> %s\n", FormatSRTTime(e.start), FormatSRTTime(e.end)))
		b.WriteString(e.text)
		b.WriteString("\n")
	}
	return b.String()
}

// analysisSegment holds parsed data from a single segment of the final analysis.
type analysisSegment struct {
	exercise string
	start    string // MM:SS
	end      string // MM:SS
	body     string // full text of the segment analysis
}

// parseAnalysisSegments splits the final analysis output into individual segments
// based on the "## 세그먼트" headers.
func parseAnalysisSegments(output string) []analysisSegment {
	matches := segmentHeaderRegex.FindAllStringSubmatchIndex(output, -1)
	if len(matches) == 0 {
		return nil
	}

	var segments []analysisSegment
	for i, match := range matches {
		exercise := output[match[2]:match[3]]
		start := output[match[4]:match[5]]
		end := output[match[6]:match[7]]

		// Body extends from end of this header to start of next header (or EOF).
		bodyStart := match[1]
		bodyEnd := len(output)
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0]
		}
		body := strings.TrimSpace(output[bodyStart:bodyEnd])

		segments = append(segments, analysisSegment{
			exercise: exercise,
			start:    start,
			end:      end,
			body:     body,
		})
	}
	return segments
}

// boldLabelRegex matches inline bold labels that act as sub-section markers.
// Examples: "**강점:**", "**약점:**", "- **강점:**"
// These switch the section context but are NOT displayed as subtitle text.
var boldLabelRegex = regexp.MustCompile(`^\s*(?:[-*•]\s*)?\*\*(.+?)[:]\*\*\s*$`)

// extractFeedbackPoints extracts key feedback items from a segment's analysis body.
// It looks for bullet points, section headers, and filters out noise (JSON blocks,
// highlight blocks, etc.). Returns a list of concise feedback strings.
func extractFeedbackPoints(body string) []string {
	// Strip code blocks (```...```) — these contain JSON highlights/injury_timestamps
	cleaned := codeBlockRegex.ReplaceAllString(body, "")

	var points []string
	seen := make(map[string]bool)

	// Track section context for pro/con decoration.
	// Updated by both ### headers and bold sub-labels (**강점:**).
	var currentSection string
	lines := strings.Split(cleaned, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Check for section headers (### or ####)
		if headerMatch := sectionHeaderRegex.FindStringSubmatch(trimmed); len(headerMatch) > 1 {
			currentSection = strings.TrimSpace(headerMatch[1])
			// Strip bold markers from section headers
			currentSection = boldRegex.ReplaceAllString(currentSection, "$1")
			continue
		}

		// Check for bold sub-labels like "**강점:**" or "- **약점:**"
		// These act as context switches, not content — skip them as subtitle text.
		if labelMatch := boldLabelRegex.FindStringSubmatch(trimmed); len(labelMatch) > 1 {
			currentSection = strings.TrimSpace(labelMatch[1])
			continue
		}

		// Check for bullet points
		if bulletMatch := bulletRegex.FindStringSubmatch(trimmed); len(bulletMatch) > 1 {
			point := strings.TrimSpace(bulletMatch[1])
			// Strip markdown bold
			point = boldRegex.ReplaceAllString(point, "$1")
			// Skip very short or noise points
			if len([]rune(point)) < 5 {
				continue
			}

			// Add pro/con markers based on section context
			decorated := decoratePoint(point, currentSection)

			if !seen[decorated] {
				seen[decorated] = true
				points = append(points, decorated)
			}
		}
	}

	return points
}

// decoratePoint adds a text-safe marker prefix based on the section context.
//
// Uses Korean text markers instead of emoji (✅/⚠️) because FFmpeg's subtitle
// fonts (Arial, etc.) don't support emoji glyphs — they render as broken boxes (☒).
func decoratePoint(point, section string) string {
	sectionLower := strings.ToLower(section)

	// Positive indicators
	if strings.Contains(sectionLower, "강점") ||
		strings.Contains(sectionLower, "strength") ||
		strings.Contains(sectionLower, "잘") {
		return "[강점] " + point
	}

	// Negative/improvement indicators
	if strings.Contains(sectionLower, "약점") ||
		strings.Contains(sectionLower, "weakness") ||
		strings.Contains(sectionLower, "개선") ||
		strings.Contains(sectionLower, "피로") ||
		strings.Contains(sectionLower, "fatigue") ||
		strings.Contains(sectionLower, "feedback") ||
		strings.Contains(sectionLower, "솔루션") {
		return "[개선] " + point
	}

	// Default: no marker
	return point
}

// parseMmSsToSeconds converts "MM:SS" to seconds as float64.
func parseMmSsToSeconds(mmss string) float64 {
	var m, s int
	_, err := fmt.Sscanf(mmss, "%d:%d", &m, &s)
	if err != nil {
		return 0
	}
	return float64(m*60 + s)
}

// truncateSubtitle wraps subtitle text into multiple lines for readability.
// If the text exceeds maxChars, it is split at natural break points across
// up to 3 lines. Korean text has fewer spaces, so we also break on commas,
// periods, and parentheses.
func truncateSubtitle(text string, maxChars int) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}

	// Try to wrap into 2-3 lines
	lines := wrapText(runes, maxChars)
	result := strings.Join(lines, "\n")

	return result
}

// wrapText splits runes into lines of approximately maxChars each (up to 3 lines).
func wrapText(runes []rune, maxChars int) []string {
	total := len(runes)
	if total <= maxChars {
		return []string{string(runes)}
	}

	// 2 lines
	if total <= maxChars*2 {
		breakAt := findBreakPoint(runes, total/2, 15)
		if breakAt > 0 && breakAt < total {
			return []string{
				strings.TrimSpace(string(runes[:breakAt])),
				strings.TrimSpace(string(runes[breakAt:])),
			}
		}
	}

	// 3 lines
	if total <= maxChars*3 {
		third := total / 3
		bp1 := findBreakPoint(runes, third, 15)
		if bp1 <= 0 {
			bp1 = third
		}
		bp2 := findBreakPoint(runes, third*2, 15)
		if bp2 <= bp1 {
			bp2 = third * 2
		}
		if bp1 > 0 && bp2 > bp1 && bp2 < total {
			return []string{
				strings.TrimSpace(string(runes[:bp1])),
				strings.TrimSpace(string(runes[bp1:bp2])),
				strings.TrimSpace(string(runes[bp2:])),
			}
		}
	}

	// Too long for 3 lines — truncate the last line
	third := total / 3
	bp1 := findBreakPoint(runes, third, 15)
	if bp1 <= 0 {
		bp1 = third
	}
	bp2 := findBreakPoint(runes, third*2, 15)
	if bp2 <= bp1 {
		bp2 = third * 2
	}
	lastLine := runes[bp2:]
	if len(lastLine) > maxChars {
		lastLine = append(lastLine[:maxChars-3], []rune("...")...)
	}

	return []string{
		strings.TrimSpace(string(runes[:bp1])),
		strings.TrimSpace(string(runes[bp1:bp2])),
		strings.TrimSpace(string(lastLine)),
	}
}

// findBreakPoint searches for a natural break point near targetIdx.
// Returns the index after the break character, or -1 if not found.
func findBreakPoint(runes []rune, targetIdx, searchRange int) int {
	// Search backwards first, then forwards
	for _, dir := range []int{-1, 1} {
		for offset := 0; offset <= searchRange; offset++ {
			i := targetIdx + dir*offset
			if i < 1 || i >= len(runes) {
				continue
			}
			switch runes[i] {
			case ' ', ',', '.', ')', '、', '。', ';':
				return i + 1
			}
		}
	}
	return -1
}

