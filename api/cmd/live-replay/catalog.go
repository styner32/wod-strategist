package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const defaultFixturesRoot = "tmp/live-replay/fixtures"

type CatalogEntry struct {
	Name     string
	Dir      string
	Manifest FixtureManifest
}

func normalizeFixtureName(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" || len(name) > 64 {
		return "", fmt.Errorf("invalid fixture name %q: must be 1..64 ASCII letters, digits, '-' or '_'", raw)
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		alnum := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
		if i == 0 && !alnum {
			return "", fmt.Errorf("invalid fixture name %q: must start with an ASCII letter or digit", raw)
		}
		if !alnum && ch != '-' && ch != '_' {
			return "", fmt.Errorf("invalid fixture name %q: only ASCII letters, digits, '-' and '_' are allowed", raw)
		}
	}
	return name, nil
}

func deriveFixtureName(wodDescription, sessionID string) string {
	for _, line := range strings.Split(wodDescription, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		candidate := trimmed
		if start := strings.IndexByte(trimmed, '"'); start >= 0 {
			if end := strings.IndexByte(trimmed[start+1:], '"'); end > 0 {
				candidate = trimmed[start+1 : start+1+end]
			}
		} else {
			for _, sep := range []string{":", "(", " - ", " — "} {
				if idx := strings.Index(candidate, sep); idx > 0 {
					candidate = candidate[:idx]
				}
			}
		}
		if slug := slugifyASCII(candidate); slug != "" && len(slug) <= 32 {
			if valid, err := normalizeFixtureName(slug); err == nil {
				return valid
			}
		}
		break
	}
	if slug := slugifyASCII(sessionID); slug != "" {
		if valid, err := normalizeFixtureName(slug); err == nil {
			return valid
		}
	}
	return "benchmark"
}

func slugifyASCII(input string) string {
	var out strings.Builder
	needSep := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			if needSep && out.Len() > 0 {
				out.WriteByte('-')
			}
			out.WriteByte(ch)
			needSep = false
		} else if out.Len() > 0 {
			needSep = true
		}
		if out.Len() >= 64 {
			break
		}
	}
	return strings.Trim(out.String(), "-")
}

// parseMediaTimestamp accepts MM:SS[.sss], HH:MM:SS[.sss], plain seconds, or Go durations (e.g. 4m50s).
func parseMediaTimestamp(raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("timestamp cannot be empty")
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) != 2 && len(parts) != 3 {
			return 0, fmt.Errorf("invalid timestamp %q; expected MM:SS or HH:MM:SS", raw)
		}
		var hours, minutes int
		secPart := parts[len(parts)-1]
		if len(parts) == 3 {
			h, err := strconv.Atoi(parts[0])
			if err != nil || h < 0 {
				return 0, fmt.Errorf("invalid hours in timestamp %q", raw)
			}
			m, err := strconv.Atoi(parts[1])
			if err != nil || m < 0 || m >= 60 {
				return 0, fmt.Errorf("invalid minutes in timestamp %q", raw)
			}
			hours, minutes = h, m
		} else {
			m, err := strconv.Atoi(parts[0])
			if err != nil || m < 0 {
				return 0, fmt.Errorf("invalid minutes in timestamp %q", raw)
			}
			minutes = m
		}
		secs, err := strconv.ParseFloat(secPart, 64)
		if err != nil || !finite(secs) || secs < 0 || secs >= 60 {
			return 0, fmt.Errorf("invalid seconds in timestamp %q", raw)
		}
		return float64(hours*3600+minutes*60) + secs, nil
	}
	if val, err := strconv.ParseFloat(s, 64); err == nil {
		if !finite(val) || val < 0 {
			return 0, fmt.Errorf("timestamp %q must be a non-negative finite number", raw)
		}
		return val, nil
	}
	if dur, err := time.ParseDuration(s); err == nil {
		if dur < 0 {
			return 0, fmt.Errorf("timestamp %q must be non-negative", raw)
		}
		return dur.Seconds(), nil
	}
	return 0, fmt.Errorf("invalid timestamp %q; use MM:SS (e.g. 4:50) or seconds (e.g. 290)", raw)
}

func parseEndTimestamp(raw string) (secs float64, auto bool, err error) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "auto") || strings.EqualFold(s, "end") {
		return 0, true, nil
	}
	secs, err = parseMediaTimestamp(s)
	return secs, false, err
}

func formatMediaTimestamp(secs float64, alwaysTenths bool) string {
	if !finite(secs) || secs < 0 {
		return "00:00"
	}
	tenths := int(math.Round(secs * 10))
	whole := tenths / 10
	frac := tenths % 10
	mins := whole / 60
	rem := whole % 60
	if mins >= 60 {
		hours := mins / 60
		mins %= 60
		if frac == 0 && !alwaysTenths {
			return fmt.Sprintf("%d:%02d:%02d", hours, mins, rem)
		}
		return fmt.Sprintf("%d:%02d:%02d.%d", hours, mins, rem, frac)
	}
	if frac == 0 && !alwaysTenths {
		return fmt.Sprintf("%02d:%02d", mins, rem)
	}
	return fmt.Sprintf("%02d:%02d.%d", mins, rem, frac)
}

func wodSummaryLine(m FixtureManifest) string {
	title := ""
	for _, line := range strings.Split(m.WODDescription, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			title = trimmed
			break
		}
	}
	movements := strings.Join(m.Movements, ", ")
	switch {
	case title != "" && movements != "":
		return fmt.Sprintf("%s — %s", title, movements)
	case title != "":
		return title
	case movements != "":
		return movements
	default:
		return "(no WOD description)"
	}
}

func discoverFixtures(root string) ([]CatalogEntry, error) {
	var entries []CatalogEntry
	seenDirs := map[string]bool{}
	addDir := func(dir, fallbackName string) {
		abs, err := filepath.Abs(dir)
		if err == nil {
			if seenDirs[abs] {
				return
			}
			seenDirs[abs] = true
		}
		data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			return
		}
		var m FixtureManifest
		if err := json.Unmarshal(data, &m); err != nil || m.Version != 1 || m.SourceSessionID == "" {
			return
		}
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = fallbackName
		}
		entries = append(entries, CatalogEntry{Name: name, Dir: dir, Manifest: m})
	}

	dirEntries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		candidate := filepath.Join(root, entry.Name())
		info, statErr := os.Stat(candidate)
		if statErr != nil || !info.IsDir() {
			continue
		}
		addDir(candidate, entry.Name())
	}
	if filepath.Clean(root) == filepath.Clean(defaultFixturesRoot) {
		if info, statErr := os.Stat(defaultFixtureDir); statErr == nil && info.IsDir() {
			addDir(defaultFixtureDir, deriveFixtureName("", "default"))
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
		if left != right {
			return left < right
		}
		return entries[i].Dir < entries[j].Dir
	})
	return entries, nil
}

func printFixtureCatalog(out io.Writer, root string, entries []CatalogEntry) {
	if out == nil {
		return
	}
	if len(entries) == 0 {
		fmt.Fprintf(out, "No prepared benchmark fixtures found in %s.\nPrepare one with:\n  make live-test-prepare WOD=<name> SESSION=<session_id> START=4:50\n", root)
		return
	}
	nameWidth := 4
	intervalWidth := 11
	summaryWidth := 10
	intervals := make([]string, len(entries))
	summaries := make([]string, len(entries))
	for i, entry := range entries {
		if len(entry.Name) > nameWidth {
			nameWidth = len(entry.Name)
		}
		m := entry.Manifest
		intervals[i] = fmt.Sprintf("%s..%s", formatMediaTimestamp(m.MediaStartSecs, false), formatMediaTimestamp(m.MediaEndSecs, false))
		if len(intervals[i]) > intervalWidth {
			intervalWidth = len(intervals[i])
		}
		summaries[i] = wodSummaryLine(m)
		if runes := utf8.RuneCountInString(summaries[i]); runes > summaryWidth {
			summaryWidth = runes
		}
	}
	if summaryWidth > 48 {
		summaryWidth = 48
	}
	fmt.Fprintf(out, "Prepared benchmark fixtures (%s):\n", root)
	for i, entry := range entries {
		m := entry.Manifest
		dur := m.MediaEndSecs - m.MediaStartSecs
		summary := summaries[i]
		runes := []rune(summary)
		if len(runes) > summaryWidth {
			summary = string(runes[:summaryWidth-1]) + "…"
			runes = []rune(summary)
		}
		pad := strings.Repeat(" ", max(0, summaryWidth-len(runes)))
		fmt.Fprintf(out, "  [%d] %-*s  %s (%2d chunks, %-*s)  %s%s  [%s]\n",
			i+1, nameWidth, entry.Name,
			formatMediaTimestamp(dur, true), len(m.Chunks), intervalWidth, intervals[i],
			summary, pad, m.SourceSessionID,
		)
	}
}

func availableFixtureNames(entries []CatalogEntry) string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return strings.Join(names, ", ")
}

func matchCatalogEntry(entries []CatalogEntry, query string) (CatalogEntry, error) {
	q := strings.TrimSpace(query)
	for _, entry := range entries {
		if strings.EqualFold(entry.Name, q) || strings.EqualFold(filepath.Base(entry.Dir), q) {
			return entry, nil
		}
	}
	var sessionMatches []CatalogEntry
	for _, entry := range entries {
		if strings.EqualFold(entry.Manifest.SourceSessionID, q) {
			sessionMatches = append(sessionMatches, entry)
		}
	}
	if len(sessionMatches) == 1 {
		return sessionMatches[0], nil
	}
	if len(sessionMatches) > 1 {
		return CatalogEntry{}, fmt.Errorf("multiple fixtures match session %q (%s); choose by fixture name", query, availableFixtureNames(sessionMatches))
	}
	return CatalogEntry{}, fmt.Errorf("unknown fixture %q; available fixtures: %s", query, availableFixtureNames(entries))
}

func isExplicitPath(value string) bool {
	return filepath.IsAbs(value) || strings.ContainsAny(value, `/\`) || value == "." || value == ".."
}

func resolvePrepareTarget(wod, fixture string) (name, outputDir string, err error) {
	wod, fixture = strings.TrimSpace(wod), strings.TrimSpace(fixture)
	if wod != "" && fixture != "" && !strings.EqualFold(wod, fixture) {
		return "", "", fmt.Errorf("specify either --wod (WOD=<name>) or --fixture (FIXTURE=<name|dir>), not both")
	}
	if fixture != "" {
		if isExplicitPath(fixture) {
			return "", fixture, nil
		}
		return fixture, "", nil
	}
	return wod, "", nil
}

func resolveFixtureSelection(root, wod, fixtureDir string, in io.Reader, out io.Writer) (string, error) {
	wod, fixtureDir = strings.TrimSpace(wod), strings.TrimSpace(fixtureDir)
	if wod != "" && fixtureDir != "" && !strings.EqualFold(wod, fixtureDir) {
		return "", fmt.Errorf("specify either --wod (WOD=<name>) or --fixture (FIXTURE=<name|dir>), not both")
	}
	selector := wod
	if selector == "" {
		selector = fixtureDir
	}
	if selector != "" {
		if _, err := os.Stat(filepath.Join(selector, "manifest.json")); err == nil {
			return selector, nil
		}
		if isExplicitPath(selector) {
			return "", fmt.Errorf("fixture directory %q has no manifest.json; run make live-test-list to see prepared fixtures", selector)
		}
	}
	entries, err := discoverFixtures(root)
	if err != nil {
		return "", fmt.Errorf("discover prepared fixtures: %w", err)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no prepared fixtures found in %s; run: make live-test-prepare WOD=<name> SESSION=<session_id> START=4:50", root)
	}
	if selector != "" {
		matched, matchErr := matchCatalogEntry(entries, selector)
		if matchErr == nil {
			return matched.Dir, nil
		}
		if idx, numErr := strconv.Atoi(selector); numErr == nil && idx >= 1 && idx <= len(entries) {
			return entries[idx-1].Dir, nil
		}
		return "", matchErr
	}
	printFixtureCatalog(out, root, entries)
	if out != nil {
		fmt.Fprintf(out, "\nSelect fixture [1-%d or name, q to cancel]: ", len(entries))
	}
	if in == nil {
		return "", fmt.Errorf("fixture selection required: pass WOD=<name> (available: %s)", availableFixtureNames(entries))
	}
	line, readErr := bufio.NewReader(in).ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", readErr
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		if errors.Is(readErr, io.EOF) {
			return "", fmt.Errorf("fixture selection required: pass WOD=<name> (available: %s)", availableFixtureNames(entries))
		}
		return "", fmt.Errorf("replay cancelled (no fixture selected)")
	}
	if strings.EqualFold(choice, "q") || strings.EqualFold(choice, "quit") || strings.EqualFold(choice, "cancel") {
		return "", fmt.Errorf("replay cancelled")
	}
	if idx, numErr := strconv.Atoi(choice); numErr == nil {
		if idx < 1 || idx > len(entries) {
			return "", fmt.Errorf("selection %d is out of range 1..%d", idx, len(entries))
		}
		return entries[idx-1].Dir, nil
	}
	matched, matchErr := matchCatalogEntry(entries, choice)
	if matchErr != nil {
		return "", matchErr
	}
	return matched.Dir, nil
}

func resolveFromFixtureDir(root, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(trimmed, "manifest.json")); err == nil {
		return trimmed
	}
	if slug, err := normalizeFixtureName(trimmed); err == nil {
		candidate := filepath.Join(root, slug)
		if _, statErr := os.Stat(filepath.Join(candidate, "manifest.json")); statErr == nil {
			return candidate
		}
	}
	return trimmed
}
