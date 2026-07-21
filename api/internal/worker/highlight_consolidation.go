package worker

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"unicode"
)

const (
	highlightSchemaVersion = 2

	HighlightObservationPositiveForm  = "positive_form"
	HighlightObservationFormIssue     = "form_issue"
	HighlightObservationFatigueOnset  = "fatigue_onset"
	HighlightObservationTechnique     = "technique_event"
	HighlightTagKeyMoment             = "key_moment"
	defaultHighlightMergeGapSeconds   = 1.5
	defaultHighlightMinClipSeconds    = 5.0
	defaultHighlightMaxClipSeconds    = 20.0
	defaultHighlightEventsPerMovement = 3
)

// HighlightNormalizeOptions controls deterministic playback-event creation.
// Zero values use the production defaults documented above.
type HighlightNormalizeOptions struct {
	MergeGapSeconds             float64
	MinClipSeconds              float64
	MaxClipSeconds              float64
	VideoStartSeconds           float64
	VideoEndSeconds             float64
	MaxEventsPerMovement        int
	ConservativeUnknownVideoEnd bool
}

type highlightSource struct {
	Index           int
	Start           float64
	End             float64
	Movement        string
	HasBounds       bool
	HardGapBoundary bool
}

type highlightCandidate struct {
	Observation HighlightObservation
	Movement    string
	Tags        []string
	Start       float64
	End         float64
	Source      highlightSource
}

type highlightGroup struct {
	Candidates   []highlightCandidate
	Movement     string
	Tags         []string
	Start        float64
	End          float64
	AllowedStart float64
	AllowedEnd   float64
	HasEndBound  bool
}

func (o HighlightNormalizeOptions) withDefaults() HighlightNormalizeOptions {
	if o.MergeGapSeconds <= 0 {
		o.MergeGapSeconds = defaultHighlightMergeGapSeconds
	}
	if o.MinClipSeconds <= 0 {
		o.MinClipSeconds = defaultHighlightMinClipSeconds
	}
	if o.MaxClipSeconds <= 0 {
		o.MaxClipSeconds = defaultHighlightMaxClipSeconds
	}
	if o.MaxClipSeconds < o.MinClipSeconds {
		o.MaxClipSeconds = o.MinClipSeconds
	}
	if o.MaxEventsPerMovement <= 0 {
		o.MaxEventsPerMovement = defaultHighlightEventsPerMovement
	}
	if o.VideoStartSeconds < 0 {
		o.VideoStartSeconds = 0
	}
	return o
}

// NormalizeHighlightSegmentsJSON accepts both legacy flat arrays and v2 event
// arrays. It is intentionally idempotent for already-normalized v2 data.
func NormalizeHighlightSegmentsJSON(raw string, options HighlightNormalizeOptions) ([]HighlightSegment, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	input, err := decodeHighlightSegmentArray(raw)
	if err != nil {
		return nil, err
	}

	options = options.withDefaults()
	var candidates []highlightCandidate
	for index, segment := range input {
		if segment.Version >= highlightSchemaVersion {
			// A v2 parent without observations has no exact evidence. Never turn
			// its padded playback range back into a fabricated observation.
			if len(segment.Observations) == 0 ||
				(strings.TrimSpace(segment.Movement) != "" && isNonExerciseMovement(segment.Movement)) {
				continue
			}
			source := highlightSource{Index: index, Movement: segment.Movement}
			parentStart, startErr := parseTimestampToSeconds(segment.Start)
			parentEnd, endErr := parseTimestampToSeconds(segment.End)
			if startErr == nil && endErr == nil && parentEnd > parentStart {
				source.Start = parentStart
				source.End = parentEnd
				source.HasBounds = true
			}
			for _, observation := range segment.Observations {
				candidates = append(candidates, candidatesFromObservation(observation, segment.Movement, segment.Tags, source)...)
			}
			continue
		}
		candidates = append(candidates, candidatesFromLegacySegment(segment, highlightSource{Index: -1})...)
	}
	return consolidateHighlightCandidates(candidates, options), nil
}

// MarshalHighlightSegments keeps storage as the existing JSON-encoded array.
func MarshalHighlightSegments(segments []HighlightSegment) string {
	if segments == nil {
		segments = []HighlightSegment{}
	}
	data, err := json.Marshal(segments)
	if err != nil {
		return ""
	}
	return string(data)
}

func HighlightSegmentHasTag(segment HighlightSegment, tag string) bool {
	for _, value := range segment.Tags {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(tag)) {
			return true
		}
	}
	return false
}

func HighlightSegmentHasObservationType(segment HighlightSegment, kinds ...string) bool {
	for _, observation := range segment.Observations {
		for _, kind := range kinds {
			if observation.Type == kind {
				return true
			}
		}
	}
	return false
}

// RebuildHighlightSegment validates exact observations and recomputes the
// parent projection and playback range from only the retained evidence.
func RebuildHighlightSegment(segment HighlightSegment, options HighlightNormalizeOptions) (HighlightSegment, bool) {
	options = options.withDefaults()
	hasTechniqueObservation := HighlightSegmentHasObservationType(segment, HighlightObservationTechnique)
	preserveEvaluationKeyTag := HighlightSegmentHasTag(segment, HighlightTagKeyMoment) && !hasTechniqueObservation
	var candidates []highlightCandidate
	for _, observation := range segment.Observations {
		start, err := parseTimestampToSeconds(observation.Start)
		if err != nil {
			continue
		}
		end, err := parseTimestampToSeconds(observation.End)
		if err != nil || end <= start {
			continue
		}
		if !validObservationType(observation.Type) || !validConfidence(observation.Confidence) {
			continue
		}
		if options.VideoEndSeconds > options.VideoStartSeconds &&
			(start < options.VideoStartSeconds || end > options.VideoEndSeconds) {
			continue
		}
		tags := []string(nil)
		if observation.Type == HighlightObservationTechnique || preserveEvaluationKeyTag {
			tags = append(tags, HighlightTagKeyMoment)
		}
		candidates = append(candidates, highlightCandidate{
			Observation: observation,
			Movement:    segment.Movement,
			Tags:        tags,
			Start:       start,
			End:         end,
			Source:      highlightSource{Index: -1},
		})
	}
	if len(candidates) == 0 {
		return HighlightSegment{}, false
	}

	group := newHighlightGroup(candidates[0], options)
	for _, candidate := range candidates[1:] {
		addCandidateToGroup(&group, candidate)
	}
	return buildHighlightEvent(group, options), true
}

func parseHighlightCandidates(output string, source highlightSource) []highlightCandidate {
	matches := highlightBlockRegex.FindAllStringSubmatch(output, -1)
	var candidates []highlightCandidate
	for _, match := range matches {
		jsonText := firstNonEmpty(match[1:])
		if jsonText == "" {
			continue
		}
		parsed, err := decodeHighlightSegmentArray(jsonText)
		if err != nil {
			continue
		}
		for _, segment := range parsed {
			if segment.Version >= highlightSchemaVersion && len(segment.Observations) > 0 {
				for _, observation := range segment.Observations {
					candidates = append(candidates, candidatesFromObservation(observation, segment.Movement, segment.Tags, source)...)
				}
				continue
			}
			candidates = append(candidates, candidatesFromLegacySegment(segment, source)...)
		}
	}
	return candidates
}

func decodeHighlightSegmentArray(raw string) ([]HighlightSegment, error) {
	var values []map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	segments := make([]HighlightSegment, 0, len(values))
	for _, value := range values {
		segment := HighlightSegment{
			Version:  jsonInt(value["version"]),
			Start:    firstTimestamp(value, "start", "start_time", "start_secs"),
			End:      firstTimestamp(value, "end", "end_time", "end_secs"),
			Type:     jsonString(value["type"]),
			Movement: jsonString(value["movement"]),
			Reason:   jsonString(value["reason"]),
			Tags:     jsonStrings(value["tags"]),
		}
		if segment.Reason == "" {
			segment.Reason = jsonString(value["description"])
		}
		if confidence, ok := jsonFloat(value["confidence"]); ok {
			segment.Confidence = &confidence
		}
		if segment.Type == "" && segment.Start != "" && segment.End != "" && segment.Reason != "" {
			// The oldest mobile contract had only start_time/end_time/description.
			// Treat it as an independent technique moment rather than discarding it.
			segment.Type = "key_moment"
		}
		if observations, ok := value["observations"].([]any); ok {
			for _, rawObservation := range observations {
				observationValue, ok := rawObservation.(map[string]any)
				if !ok {
					continue
				}
				observation := HighlightObservation{
					Start:  firstTimestamp(observationValue, "start", "start_time", "start_secs"),
					End:    firstTimestamp(observationValue, "end", "end_time", "end_secs"),
					Type:   jsonString(observationValue["type"]),
					Reason: jsonString(observationValue["reason"]),
				}
				if observation.Reason == "" {
					observation.Reason = jsonString(observationValue["description"])
				}
				if confidence, ok := jsonFloat(observationValue["confidence"]); ok {
					observation.Confidence = &confidence
				}
				if verified, ok := observationValue["verified"].(bool); ok {
					observation.Verified = &verified
				}
				segment.Observations = append(segment.Observations, observation)
			}
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

func firstTimestamp(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if timestamp, ok := jsonTimestamp(value[key]); ok {
			return timestamp
		}
	}
	return ""
}

func jsonTimestamp(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return "", false
		}
		return typed, true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 0 {
			return "", false
		}
		return formatSegmentTimestamp(typed), true
	default:
		return "", false
	}
}

func jsonString(value any) string {
	result, _ := value.(string)
	return strings.TrimSpace(result)
}

func jsonStrings(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		if text := jsonString(item); text != "" {
			result = append(result, text)
		}
	}
	return result
}

func jsonInt(value any) int {
	number, ok := value.(float64)
	if !ok || number < 0 || math.Trunc(number) != number {
		return 0
	}
	return int(number)
}

func jsonFloat(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok
}

func candidatesFromObservation(observation HighlightObservation, movement string, tags []string, source highlightSource) []highlightCandidate {
	start, err := parseTimestampToSeconds(observation.Start)
	if err != nil {
		return nil
	}
	end, err := parseTimestampToSeconds(observation.End)
	if err != nil || end <= start || !validObservationType(observation.Type) || !validConfidence(observation.Confidence) {
		return nil
	}
	if source.HasBounds && (start < source.Start || end > source.End) {
		return nil
	}

	if strings.TrimSpace(movement) == "" {
		movement = source.Movement
	}
	if strings.TrimSpace(movement) != "" && isNonExerciseMovement(movement) {
		return nil
	}
	observation.Start = formatSegmentTimestamp(start)
	observation.End = formatSegmentTimestamp(end)
	observation.Reason = strings.TrimSpace(observation.Reason)
	tags = supportedHighlightTags(tags)
	if observation.Type == HighlightObservationTechnique {
		tags = appendUniqueFold(tags, HighlightTagKeyMoment)
	}
	return splitLongHighlightCandidate(highlightCandidate{
		Observation: observation,
		Movement:    strings.TrimSpace(movement),
		Tags:        tags,
		Start:       start,
		End:         end,
		Source:      source,
	}, defaultHighlightMaxClipSeconds)
}

func candidatesFromLegacySegment(segment HighlightSegment, source highlightSource) []highlightCandidate {
	if isHeartRateOnlyFatigueHighlight(segment) {
		return nil
	}
	if !validConfidence(segment.Confidence) {
		return nil
	}
	start, err := parseTimestampToSeconds(segment.Start)
	if err != nil {
		return nil
	}
	end, err := parseTimestampToSeconds(segment.End)
	if err != nil || end <= start {
		return nil
	}
	if source.HasBounds && (start < source.Start || end > source.End) {
		return nil
	}

	kind, keyMoment := normalizeObservationType(segment.Type)
	if kind == "" {
		return nil
	}
	tags := supportedHighlightTags(segment.Tags)
	if keyMoment {
		tags = appendUniqueFold(tags, HighlightTagKeyMoment)
	}
	movement := strings.TrimSpace(segment.Movement)
	if movement == "" {
		movement = strings.TrimSpace(source.Movement)
	}
	if strings.TrimSpace(movement) != "" && isNonExerciseMovement(movement) {
		return nil
	}
	observation := HighlightObservation{
		Start:      formatSegmentTimestamp(start),
		End:        formatSegmentTimestamp(end),
		Type:       kind,
		Reason:     strings.TrimSpace(segment.Reason),
		Confidence: segment.Confidence,
	}
	return splitLongHighlightCandidate(highlightCandidate{
		Observation: observation,
		Movement:    movement,
		Tags:        tags,
		Start:       start,
		End:         end,
		Source:      source,
	}, defaultHighlightMaxClipSeconds)
}

func splitLongHighlightCandidate(candidate highlightCandidate, maxDuration float64) []highlightCandidate {
	if maxDuration <= 0 || candidate.End-candidate.Start <= maxDuration {
		return []highlightCandidate{candidate}
	}
	var result []highlightCandidate
	for start := candidate.Start; start < candidate.End; start += maxDuration {
		piece := candidate
		piece.Start = start
		piece.End = math.Min(candidate.End, start+maxDuration)
		piece.Observation.Start = formatSegmentTimestamp(piece.Start)
		piece.Observation.End = formatSegmentTimestamp(piece.End)
		result = append(result, piece)
	}
	return result
}

func consolidateHighlightCandidates(candidates []highlightCandidate, options HighlightNormalizeOptions) []HighlightSegment {
	options = options.withDefaults()
	filtered := make([]highlightCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.End <= candidate.Start || !validObservationType(candidate.Observation.Type) ||
			!validConfidence(candidate.Observation.Confidence) {
			continue
		}
		if options.VideoEndSeconds > options.VideoStartSeconds &&
			(candidate.Start < options.VideoStartSeconds || candidate.End > options.VideoEndSeconds) {
			continue
		}
		filtered = append(filtered, splitLongHighlightCandidate(candidate, options.MaxClipSeconds)...)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Start == filtered[j].Start {
			return filtered[i].End < filtered[j].End
		}
		return filtered[i].Start < filtered[j].Start
	})

	groups := groupHighlightCandidates(filtered, options)
	groups = splitOversizedHighlightGroups(groups, options)

	var events []HighlightSegment
	for _, group := range groups {
		events = append(events, buildHighlightEvent(group, options))
	}
	sortHighlightSegments(events)
	return selectHighlightEvents(events, options.MaxEventsPerMovement)
}

func groupHighlightCandidates(candidates []highlightCandidate, options HighlightNormalizeOptions) []highlightGroup {
	var groups []highlightGroup
	for _, candidate := range candidates {
		bestIndex := -1
		bestDistance := math.MaxFloat64
		for i := range groups {
			if !sameHighlightMovement(candidate, groups[i]) || !sourceBoundaryCompatible(candidate, groups[i], options) {
				continue
			}
			distance := intervalDistance(candidate.Start, candidate.End, groups[i].Start, groups[i].End)
			if distance <= options.MergeGapSeconds && distance < bestDistance {
				bestIndex = i
				bestDistance = distance
			}
		}
		if bestIndex < 0 {
			groups = append(groups, newHighlightGroup(candidate, options))
			continue
		}
		addCandidateToGroup(&groups[bestIndex], candidate)
	}
	return groups
}

func splitOversizedHighlightGroups(groups []highlightGroup, options HighlightNormalizeOptions) []highlightGroup {
	var result []highlightGroup
	for _, group := range groups {
		result = append(result, splitOversizedHighlightGroup(group, options)...)
	}
	return result
}

func splitOversizedHighlightGroup(group highlightGroup, options HighlightNormalizeOptions) []highlightGroup {
	if group.End-group.Start <= options.MaxClipSeconds || len(group.Candidates) < 2 {
		return []highlightGroup{group}
	}

	sort.SliceStable(group.Candidates, func(i, j int) bool {
		if group.Candidates[i].Start == group.Candidates[j].Start {
			return group.Candidates[i].End < group.Candidates[j].End
		}
		return group.Candidates[i].Start < group.Candidates[j].Start
	})
	cut := 1
	largestGap := group.Candidates[1].Start - group.Candidates[0].End
	for i := 2; i < len(group.Candidates); i++ {
		gap := group.Candidates[i].Start - group.Candidates[i-1].End
		if gap > largestGap {
			largestGap = gap
			cut = i
		}
	}

	left := highlightGroupFromCandidates(group.Candidates[:cut], options)
	right := highlightGroupFromCandidates(group.Candidates[cut:], options)
	result := splitOversizedHighlightGroup(left, options)
	return append(result, splitOversizedHighlightGroup(right, options)...)
}

func highlightGroupFromCandidates(candidates []highlightCandidate, options HighlightNormalizeOptions) highlightGroup {
	group := newHighlightGroup(candidates[0], options)
	for _, candidate := range candidates[1:] {
		addCandidateToGroup(&group, candidate)
	}
	return group
}

func newHighlightGroup(candidate highlightCandidate, options HighlightNormalizeOptions) highlightGroup {
	allowedStart := options.VideoStartSeconds
	allowedEnd := options.VideoEndSeconds
	hasEndBound := allowedEnd > allowedStart
	if candidate.Source.HasBounds {
		allowedStart = math.Max(allowedStart, candidate.Source.Start)
		if !hasEndBound || candidate.Source.End < allowedEnd {
			allowedEnd = candidate.Source.End
			hasEndBound = true
		}
	}
	movement := candidate.Movement
	if isCompoundMovement(candidate.Source.Movement) {
		movement = candidate.Source.Movement
	}
	return highlightGroup{
		Candidates:   []highlightCandidate{candidate},
		Movement:     movement,
		Tags:         append([]string(nil), candidate.Tags...),
		Start:        candidate.Start,
		End:          candidate.End,
		AllowedStart: allowedStart,
		AllowedEnd:   allowedEnd,
		HasEndBound:  hasEndBound,
	}
}

func addCandidateToGroup(group *highlightGroup, candidate highlightCandidate) {
	group.Candidates = append(group.Candidates, candidate)
	group.Start = math.Min(group.Start, candidate.Start)
	group.End = math.Max(group.End, candidate.End)
	for _, tag := range candidate.Tags {
		group.Tags = appendUniqueFold(group.Tags, tag)
	}
	if isCompoundMovement(candidate.Source.Movement) && candidate.Source.Movement != "" {
		group.Movement = candidate.Source.Movement
	}
	if candidate.Source.HasBounds {
		group.AllowedStart = math.Min(group.AllowedStart, candidate.Source.Start)
		if !group.HasEndBound || candidate.Source.End > group.AllowedEnd {
			group.AllowedEnd = candidate.Source.End
			group.HasEndBound = true
		}
	}
}

func buildHighlightEvent(group highlightGroup, options HighlightNormalizeOptions) HighlightSegment {
	options = options.withDefaults()
	sort.SliceStable(group.Candidates, func(i, j int) bool {
		if group.Candidates[i].Start == group.Candidates[j].Start {
			return group.Candidates[i].End < group.Candidates[j].End
		}
		return group.Candidates[i].Start < group.Candidates[j].Start
	})
	observations := make([]HighlightObservation, 0, len(group.Candidates))
	for _, candidate := range group.Candidates {
		observations = append(observations, candidate.Observation)
	}

	start, end := paddedHighlightBounds(group.Start, group.End, group.AllowedStart, group.AllowedEnd, group.HasEndBound, options)
	typeName := deriveHighlightType(observations)
	tags := uniqueFold(group.Tags)
	if typeName == "key_moment" {
		tags = appendUniqueFold(tags, HighlightTagKeyMoment)
	}
	return HighlightSegment{
		Version:      highlightSchemaVersion,
		Start:        formatSegmentTimestamp(start),
		End:          formatSegmentTimestamp(end),
		Type:         typeName,
		Movement:     strings.TrimSpace(group.Movement),
		Reason:       summarizeHighlightObservations(observations),
		Tags:         tags,
		Observations: observations,
	}
}

func paddedHighlightBounds(start, end, allowedStart, allowedEnd float64, hasEndBound bool, options HighlightNormalizeOptions) (float64, float64) {
	if !hasEndBound && options.ConservativeUnknownVideoEnd {
		// API read-time normalization has no stored video duration for some old
		// rows. Treat the last exact observation as the safe end and use preceding
		// context rather than fabricating time beyond known media.
		allowedEnd = end
		hasEndBound = true
	}
	start = math.Max(start, allowedStart)
	if hasEndBound {
		end = math.Min(end, allowedEnd)
	}
	if end-start > options.MaxClipSeconds {
		end = start + options.MaxClipSeconds
	}
	if end-start >= options.MinClipSeconds {
		return start, end
	}

	needed := options.MinClipSeconds - (end - start)
	start -= needed / 2
	end += needed / 2
	if start < allowedStart {
		end += allowedStart - start
		start = allowedStart
	}
	if hasEndBound && end > allowedEnd {
		start -= end - allowedEnd
		end = allowedEnd
		if start < allowedStart {
			start = allowedStart
		}
	}
	if end-start > options.MaxClipSeconds {
		end = start + options.MaxClipSeconds
	}
	return math.Max(0, start), math.Max(math.Max(0, start), end)
}

func deriveHighlightType(observations []HighlightObservation) string {
	var positive, issue, fatigue, technique bool
	for _, observation := range observations {
		switch observation.Type {
		case HighlightObservationPositiveForm:
			positive = true
		case HighlightObservationFormIssue:
			issue = true
		case HighlightObservationFatigueOnset:
			fatigue = true
		case HighlightObservationTechnique:
			technique = true
		}
	}
	if positive && (issue || fatigue) {
		return "mixed_form"
	}
	if issue {
		return "worst_form"
	}
	if fatigue {
		return "fatigue_point"
	}
	if positive {
		return "best_form"
	}
	if technique {
		return "key_moment"
	}
	return "key_moment"
}

func summarizeHighlightObservations(observations []HighlightObservation) string {
	var reasons []string
	seen := map[string]bool{}
	for _, observation := range observations {
		reason := strings.TrimSpace(observation.Reason)
		key := strings.ToLower(reason)
		if reason == "" || seen[key] {
			continue
		}
		seen[key] = true
		reasons = append(reasons, reason)
	}
	summary := strings.Join(reasons, " · ")
	const maxRunes = 360
	runes := []rune(summary)
	if len(runes) > maxRunes {
		summary = string(runes[:maxRunes-1]) + "…"
	}
	return summary
}

func selectHighlightEvents(events []HighlightSegment, maxPerMovement int) []HighlightSegment {
	if maxPerMovement <= 0 {
		return events
	}
	type slots struct {
		positive    []HighlightSegment
		improvement []HighlightSegment
		technique   []HighlightSegment
	}
	byMovement := map[string]*slots{}
	order := []string{}
	for _, event := range events {
		key := normalizeHighlightMovementKey(event.Movement)
		if _, ok := byMovement[key]; !ok {
			byMovement[key] = &slots{}
			order = append(order, key)
		}
		slot := byMovement[key]
		switch event.Type {
		case "best_form":
			slot.positive = append(slot.positive, event)
		case "key_moment":
			slot.technique = append(slot.technique, event)
		default:
			slot.improvement = append(slot.improvement, event)
		}
	}

	var selected []HighlightSegment
	for _, key := range order {
		slot := byMovement[key]
		for _, candidates := range [][]HighlightSegment{slot.positive, slot.improvement, slot.technique} {
			if len(candidates) == 0 || countMovementEvents(selected, key) >= maxPerMovement {
				continue
			}
			sort.SliceStable(candidates, func(i, j int) bool { return highlightEventBetter(candidates[i], candidates[j]) })
			selected = append(selected, candidates[0])
		}
	}
	sortHighlightSegments(selected)
	return selected
}

func highlightEventBetter(left, right HighlightSegment) bool {
	leftFeatured := HighlightSegmentHasTag(left, HighlightTagKeyMoment)
	rightFeatured := HighlightSegmentHasTag(right, HighlightTagKeyMoment)
	if leftFeatured != rightFeatured {
		return leftFeatured
	}
	leftConfidence := maxObservationConfidence(left)
	rightConfidence := maxObservationConfidence(right)
	if leftConfidence != rightConfidence {
		return leftConfidence > rightConfidence
	}
	if len(left.Observations) != len(right.Observations) {
		return len(left.Observations) > len(right.Observations)
	}
	leftStart, _ := parseTimestampToSeconds(left.Start)
	rightStart, _ := parseTimestampToSeconds(right.Start)
	return leftStart < rightStart
}

func maxObservationConfidence(segment HighlightSegment) float64 {
	var result float64
	for _, observation := range segment.Observations {
		if observation.Confidence != nil && *observation.Confidence > result {
			result = *observation.Confidence
		}
	}
	return result
}

func countMovementEvents(events []HighlightSegment, key string) int {
	count := 0
	for _, event := range events {
		if normalizeHighlightMovementKey(event.Movement) == key {
			count++
		}
	}
	return count
}

func sortHighlightSegments(segments []HighlightSegment) {
	sort.SliceStable(segments, func(i, j int) bool {
		left, leftErr := parseTimestampToSeconds(segments[i].Start)
		right, rightErr := parseTimestampToSeconds(segments[j].Start)
		if leftErr != nil {
			return false
		}
		if rightErr != nil {
			return true
		}
		return left < right
	})
}

func sameHighlightMovement(candidate highlightCandidate, group highlightGroup) bool {
	if candidate.Source.Index >= 0 && len(group.Candidates) > 0 &&
		candidate.Source.Index == group.Candidates[0].Source.Index && isCompoundMovement(candidate.Source.Movement) {
		return true
	}
	return normalizeHighlightMovementKey(candidate.Movement) == normalizeHighlightMovementKey(group.Movement)
}

func sourceBoundaryCompatible(candidate highlightCandidate, group highlightGroup, options HighlightNormalizeOptions) bool {
	if candidate.Source.Index < 0 || len(group.Candidates) == 0 || group.Candidates[0].Source.Index < 0 {
		return true
	}
	for _, existing := range group.Candidates {
		if candidate.Source.Index == existing.Source.Index {
			return true
		}
		if candidate.Source.Index-existing.Source.Index > 1 || existing.Source.Index-candidate.Source.Index > 1 {
			continue
		}
		allowedSourceGap := options.MergeGapSeconds
		if candidate.Source.HardGapBoundary || existing.Source.HardGapBoundary {
			allowedSourceGap = 0.001
		}
		if candidate.Source.HasBounds && existing.Source.HasBounds &&
			intervalDistance(candidate.Source.Start, candidate.Source.End, existing.Source.Start, existing.Source.End) <= allowedSourceGap &&
			normalizeHighlightMovementKey(candidate.Source.Movement) == normalizeHighlightMovementKey(existing.Source.Movement) {
			return true
		}
	}
	return false
}

func intervalDistance(startA, endA, startB, endB float64) float64 {
	if endA >= startB && endB >= startA {
		return 0
	}
	if endA < startB {
		return startB - endA
	}
	return startA - endB
}

func normalizeObservationType(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "best_form", HighlightObservationPositiveForm:
		return HighlightObservationPositiveForm, false
	case "worst_form", HighlightObservationFormIssue:
		return HighlightObservationFormIssue, false
	case "fatigue_point", HighlightObservationFatigueOnset:
		return HighlightObservationFatigueOnset, false
	case "key_moment", HighlightObservationTechnique:
		return HighlightObservationTechnique, true
	default:
		return "", false
	}
}

func validObservationType(value string) bool {
	switch value {
	case HighlightObservationPositiveForm, HighlightObservationFormIssue,
		HighlightObservationFatigueOnset, HighlightObservationTechnique:
		return true
	default:
		return false
	}
}

func validConfidence(value *float64) bool {
	return value == nil || (!math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 1)
}

func normalizeHighlightMovementKey(value string) string {
	var builder strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			space = false
		} else if !space && builder.Len() > 0 {
			builder.WriteByte(' ')
			space = true
		}
	}
	key := strings.TrimSpace(builder.String())
	switch key {
	case "s2oh":
		return "shoulder to overhead"
	case "ohs":
		return "overhead squat"
	case "rdl":
		return "romanian deadlift"
	case "t2b":
		return "toes to bar"
	}
	return key
}

func isCompoundMovement(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, " and ") || strings.Contains(value, " & ") || strings.Contains(value, "+")
}

func uniqueFold(values []string) []string {
	var result []string
	for _, value := range values {
		result = appendUniqueFold(result, value)
	}
	return result
}

func supportedHighlightTags(values []string) []string {
	if containsFold(values, HighlightTagKeyMoment) {
		return []string{HighlightTagKeyMoment}
	}
	return nil
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func appendUniqueFold(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}
