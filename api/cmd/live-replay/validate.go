package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
)

func finite(v float64) bool             { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func near(a, b, tolerance float64) bool { return finite(a) && finite(b) && math.Abs(a-b) <= tolerance }

func validateFixture(dir string, m FixtureManifest) ([]byte, error) {
	if m.Version != 1 || m.SourceSessionID == "" || m.SourceProfileID == 0 || m.Bucket == "" || !finite(m.FPS) || m.FPS <= 0 {
		return nil, fmt.Errorf("invalid fixture manifest identity, version or FPS; run prepare again")
	}
	for _, source := range []ObjectReference{m.Video, m.Sensor} {
		generation, err := strconv.ParseInt(source.Generation, 10, 64)
		if err != nil || generation <= 0 || source.Object == "" || source.SizeBytes <= 0 || len(source.SHA256) != 64 {
			return nil, fmt.Errorf("fixture source provenance is missing")
		}
	}
	if err := validateMapping(m.Mapping, m.MediaStartSecs*1000, m.MediaEndSecs*1000); err != nil {
		return nil, err
	}
	start, err := captureAtMedia(m.Mapping, m.MediaStartSecs*1000)
	if err != nil {
		return nil, err
	}
	end, err := prepareCaptureEnd(m.Mapping, m.MediaEndSecs*1000)
	if err != nil {
		return nil, err
	}
	if !near(start, m.CaptureStartMs, 0.001) || !near(end, m.CaptureEndMs, 0.001) || !near(m.Stats.DurationMs, end-start, 0.001) || math.Abs((end-start)/1000-(m.MediaEndSecs-m.MediaStartSecs)) > 1 {
		return nil, fmt.Errorf("fixture capture/media boundaries do not agree with source mapping")
	}
	raw, err := checkedFile(dir, m.SensorFile, m.SensorSHA256, m.SensorSizeBytes)
	if err != nil {
		return nil, err
	}
	stats, err := telemetryStats(raw)
	if err != nil {
		return nil, err
	}
	if err = sameSensorStats(stats, m.Stats); err != nil {
		return nil, err
	}
	if stats.HRSamples < 2 || stats.ACCSamples == 0 {
		return nil, fmt.Errorf("fixture needs measured HR and ACC samples")
	}
	if len(m.Chunks) == 0 {
		return nil, fmt.Errorf("fixture contains no video chunks")
	}
	chunkSeconds := m.ChunkSeconds
	if chunkSeconds == 0 {
		chunkSeconds = 10
	} // Original manifests predate this field.
	if err := validateChunkCadence(m.Chunks, chunkSeconds, m.MediaEndSecs-m.MediaStartSecs, m.FPS); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	previousMedia, previousCapture := 0.0, 0.0
	for _, chunk := range m.Chunks {
		name := filepath.Base(chunk.File)
		if seen[name] || !strings.EqualFold(filepath.Ext(name), ".mp4") {
			return nil, fmt.Errorf("duplicate or invalid fixture chunk %q", name)
		}
		seen[name] = true
		if !near(chunk.MediaStartSecs, previousMedia, 0.001) || !finite(chunk.CaptureStartSecs) || chunk.CaptureStartSecs < previousCapture-0.001 || !finite(chunk.MediaEndSecs) || !finite(chunk.CaptureEndSecs) || chunk.MediaEndSecs <= chunk.MediaStartSecs || chunk.CaptureEndSecs <= chunk.CaptureStartSecs {
			return nil, fmt.Errorf("fixture chunk %s has discontinuous or invalid times", name)
		}
		mappedStart, err := captureAtMedia(m.Mapping, (m.MediaStartSecs+chunk.MediaStartSecs)*1000)
		if err != nil {
			return nil, err
		}
		// Container rounding can place the final video endpoint within one frame of the source cut.
		mediaEnd := math.Min(m.MediaEndSecs, m.MediaStartSecs+chunk.MediaEndSecs)
		mappedEnd, err := prepareCaptureEnd(m.Mapping, mediaEnd*1000)
		if err != nil {
			return nil, err
		}
		if !near(chunk.CaptureStartSecs, (mappedStart-start)/1000, 0.001) || !near(chunk.CaptureEndSecs, (mappedEnd-start)/1000, 0.001) {
			return nil, fmt.Errorf("fixture chunk %s does not preserve capture/media mapping", name)
		}
		peak, err := peakHeartRate(raw, chunk.CaptureStartSecs*1000, chunk.CaptureEndSecs*1000)
		if err != nil {
			return nil, err
		}
		if peak != chunk.HeartRateBPM {
			return nil, fmt.Errorf("fixture chunk %s peak HR differs from raw telemetry", name)
		}
		if _, err := checkedFile(dir, chunk.File, chunk.SHA256, chunk.SizeBytes); err != nil {
			return nil, err
		}
		previousMedia, previousCapture = chunk.MediaEndSecs, chunk.CaptureEndSecs
	}
	if !near(previousMedia, m.MediaEndSecs-m.MediaStartSecs, 1/m.FPS+1e-6) || !near(previousCapture*1000, stats.DurationMs, 1) {
		return nil, fmt.Errorf("fixture video or capture coverage is incomplete")
	}
	return raw, nil
}

func validateChunkCadence(chunks []ReplayChunk, chunkSeconds, duration, fps float64) error {
	if !finite(chunkSeconds) || chunkSeconds <= 0 || !finite(duration) || duration <= 0 || !finite(fps) || fps <= 0 {
		return fmt.Errorf("invalid fixture chunk cadence")
	}
	expected := int(math.Ceil(duration/chunkSeconds - 1e-9))
	if len(chunks) != expected {
		return fmt.Errorf("fixture chunk cadence: expected %d chunks, got %d", expected, len(chunks))
	}
	tolerance := 1/fps + 0.001
	for i, chunk := range chunks {
		start := float64(i) * chunkSeconds
		end := math.Min(start+chunkSeconds, duration)
		if !near(chunk.MediaStartSecs, start, tolerance) || !near(chunk.MediaEndSecs, end, tolerance) || !near(chunk.MediaEndSecs-chunk.MediaStartSecs, end-start, tolerance) {
			return fmt.Errorf("fixture chunk cadence: %s must cover %.6f..%.6fs (one-frame tolerance)", chunk.File, start, end)
		}
	}
	return nil
}

func sameSensorStats(actual, expected SensorStats) error {
	if !near(actual.DurationMs, expected.DurationMs, 0.001) || actual.HRSamples != expected.HRSamples || actual.ACCSamples != expected.ACCSamples {
		return fmt.Errorf("sensor sample counts or duration differ from the prepared fixture")
	}
	for _, pair := range [][2]*float64{{actual.FirstHRMs, expected.FirstHRMs}, {actual.LastHRMs, expected.LastHRMs}, {actual.FirstACCMs, expected.FirstACCMs}, {actual.LastACCMs, expected.LastACCMs}} {
		if !sameOptionalNumber(pair[0], pair[1], 0.001) {
			return fmt.Errorf("sensor sample bounds differ from the prepared fixture")
		}
	}
	return nil
}

func validateTimelineMapping(rows []db.ChunkAnalysisResult, segments []MappingSegment) error {
	if len(rows) == 0 || len(segments) != len(rows) {
		return fmt.Errorf("sensor/video mapping is incomplete")
	}
	matched := map[int]bool{}
	for _, segment := range segments {
		found := false
		for i, row := range rows {
			if matched[i] || row.StartSecs == nil || row.EndSecs == nil || row.MediaStartSecs == nil || row.MediaEndSecs == nil {
				continue
			}
			if near(segment.CaptureStartMs, *row.StartSecs*1000, 1) && near(segment.CaptureEndMs, *row.EndSecs*1000, 1) && near(segment.MediaStartMs, *row.MediaStartSecs*1000, 1) && near(segment.MediaEndMs, *row.MediaEndSecs*1000, 1) {
				matched[i], found = true, true
				break
			}
		}
		if !found {
			return fmt.Errorf("sensor/video mapping disagrees with verified chunk intervals")
		}
	}
	return nil
}

func validateChunkResults(m FixtureManifest, rows []db.ChunkAnalysisResult, sessionID string, requireMedia bool) (bool, error) {
	expected := map[string]ReplayChunk{}
	for _, chunk := range m.Chunks {
		expected[filepath.Base(chunk.File)] = chunk
	}
	seen := map[string]db.ChunkAnalysisResult{}
	ready := true
	for _, row := range rows {
		name := filepath.Base(row.FilePath)
		chunk, exists := expected[name]
		if !exists || row.ProfileID != targetProfileID || row.SessionID != sessionID || row.FilePath != fmt.Sprintf("gs://%s/videos/%d/%s/%s", m.Bucket, targetProfileID, sessionID, name) {
			return false, fmt.Errorf("unexpected chunk source or ownership: %s", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return false, fmt.Errorf("duplicate chunk result: %s", name)
		}
		seen[name] = row
		if row.Status == "FAILED" {
			return false, fmt.Errorf("chunk analysis failed: %s", name)
		}
		if row.Status != "COMPLETED" {
			ready = false
			continue
		}
		if row.HeartRateBPM != chunk.HeartRateBPM {
			return false, fmt.Errorf("chunk peak HR changed: %s", name)
		}
		if row.StartSecs == nil || row.EndSecs == nil || !near(*row.StartSecs, chunk.CaptureStartSecs, 0.001) || !near(*row.EndSecs, chunk.CaptureEndSecs, 0.001) {
			return false, fmt.Errorf("chunk capture interval changed: %s", name)
		}
		var observations activity.Observations
		if json.Unmarshal(row.MovementObservations, &observations) != nil || !finite(observations.DurationSecs) || observations.DurationSecs <= 0 {
			return false, fmt.Errorf("live movement observations missing or invalid: %s", name)
		}
		if _, err := activity.Decode(row.MovementObservations, 0, observations.DurationSecs); err != nil {
			return false, fmt.Errorf("live movement observations missing or invalid: %s: %w", name, err)
		}
	}
	if len(seen) != len(expected) {
		ready = false
	}
	if !ready || !requireMedia {
		return ready, nil
	}
	previous := 0.0
	for _, chunk := range m.Chunks {
		row := seen[filepath.Base(chunk.File)]
		if row.MediaStartSecs == nil || row.MediaEndSecs == nil {
			return false, nil
		}
		if !finite(*row.MediaEndSecs) || *row.MediaEndSecs <= *row.MediaStartSecs || !near(*row.MediaStartSecs, previous, 0.001) || !near(*row.MediaStartSecs, chunk.MediaStartSecs, 1/m.FPS+1e-6) || !near(*row.MediaEndSecs, chunk.MediaEndSecs, 1/m.FPS+1e-6) {
			return false, fmt.Errorf("merged media mapping is invalid for %s", filepath.Base(chunk.File))
		}
		previous = *row.MediaEndSecs
	}
	if !near(previous, m.MediaEndSecs-m.MediaStartSecs, 1/m.FPS+1e-6) {
		return false, fmt.Errorf("merged media duration differs from fixture")
	}
	return true, nil
}

func validateSensorResult(m FixtureManifest, raw []byte, summary sensor.SensorSummaryResult, tl *sensor.SensorTimelineData, requestID, version string) error {
	v, err := strconv.ParseInt(version, 10, 64)
	if err != nil || v <= 0 || requestID == "" || summary.Version != v || summary.RequestID != requestID || summary.SourceGeneration == "" || summary.CalculationVersion != 3 || summary.CalculationInputs.CalculationVersion != 3 {
		return fmt.Errorf("sensor summary identity or calculation version is stale")
	}
	if !summary.Quality.IsComplete || len(summary.Quality.Errors) != 0 {
		return fmt.Errorf("sensor summary is incomplete or corrupt")
	}
	if tl == nil || tl.SchemaVersion != 1 || tl.Clock != "capture_clock" || tl.BucketMs != 1000 || !near(float64(tl.DurationMs), m.Stats.DurationMs, 1) || tl.Source.SensorVersion != version || tl.Source.RequestID != requestID || tl.Source.SourceGeneration != summary.SourceGeneration || tl.Source.HRCalculationVersion != 3 {
		return fmt.Errorf("sensor timeline identity, clock or duration is invalid")
	}
	stats, err := telemetryStats(raw)
	if err != nil {
		return err
	}
	if err = sameSensorStats(stats, m.Stats); err != nil {
		return err
	}
	var meta struct {
		SessionID string `json:"workout_session_id"`
		ProfileID uint   `json:"profile_id"`
	}
	first, _, _ := bytes.Cut(raw, []byte{'\n'})
	if err := json.Unmarshal(first, &meta); err != nil || meta.ProfileID != targetProfileID || meta.SessionID == "" {
		return fmt.Errorf("replayed sensor meta identity is invalid")
	}
	inputs := summary.CalculationInputs
	result, err := sensor.ParseAndProcessWithTimeline(bytes.NewReader(raw), sensor.ParseOptions{
		CalculationVersion: 3, ExpectedProfileID: targetProfileID, ExpectedSessionID: meta.SessionID,
		ExpectedSHA256: digest(raw), ExpectedSizeBytes: int64(len(raw)), Age: inputs.Age, EstimatedMaxHR: inputs.MaxHR,
		RestingHR: inputs.RestingHR, ENMORestG: inputs.ENMORestG, ENMORefG: inputs.ENMORefG, CalibrationVersion: inputs.CalibrationVersion,
	}, tl.Source)
	if err != nil {
		return fmt.Errorf("replayed sensor parser validation: %w", err)
	}
	if result.TimelineError != nil || result.Timeline == nil {
		return fmt.Errorf("replayed sensor timeline could not be recomputed: %v", result.TimelineError)
	}
	expected := result.Summary.Metrics
	actual := summary.Metrics
	if !near(actual.DurationSeconds, expected.DurationSeconds, 0.001) || !near(actual.PauseSeconds, expected.PauseSeconds, 0.001) {
		return fmt.Errorf("sensor effective duration or pause time differs from the uploaded samples")
	}
	if !sameHRMetrics(actual.HR, expected.HR) || summary.Quality.Status != result.Summary.Quality.Status || summary.Quality.ValidHR != result.Summary.Quality.ValidHR || !near(summary.Quality.HRCoverage, result.Summary.Quality.HRCoverage, 1e-6) || actual.ACC != expected.ACC {
		return fmt.Errorf("sensor HR/ACC metrics differ from the uploaded measured samples")
	}
	if !sameHybridMetrics(actual.Hybrid, expected.Hybrid) || !near(summary.HRBonus, result.Summary.HRBonus, 1e-6) {
		return fmt.Errorf("sensor hybrid intensity, load or quality differs from the uploaded measured samples")
	}
	if len(tl.Points) != len(result.Timeline.Points) || len(tl.Gaps) != len(result.Timeline.Gaps) || len(tl.Pauses) != len(result.Timeline.Pauses) {
		return fmt.Errorf("sensor timeline lost points, gaps or pauses")
	}
	for i, point := range tl.Points {
		want := result.Timeline.Points[i]
		if point.StartMs != want.StartMs || point.EndMs != want.EndMs || !sameTimelineValue(point.HeartRateBPM, want.HeartRateBPM) || !sameTimelineValue(point.AccMagnitudeStdG, want.AccMagnitudeStdG) || !sameOptionalTimelineValue(point.EnmoG, want.EnmoG) || !sameOptionalTimelineValue(point.HybridIntensity, want.HybridIntensity) {
			return fmt.Errorf("sensor timeline differs at bucket %d", i)
		}
	}
	for i, gap := range tl.Gaps {
		if gap != result.Timeline.Gaps[i] {
			return fmt.Errorf("sensor timeline gap differs at %d", i)
		}
	}
	for i, pause := range tl.Pauses {
		if pause != result.Timeline.Pauses[i] {
			return fmt.Errorf("sensor timeline pause differs at %d", i)
		}
	}
	return nil
}

func sameOptionalNumber(a, b *float64, tolerance float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return near(*a, *b, tolerance)
}

func sameOptionalInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameHRMetrics(a, b sensor.HRMetrics) bool {
	if a.ValidHR != b.ValidHR || a.HasHRZones != b.HasHRZones || !sameOptionalInt(a.MinBPM, b.MinBPM) || !sameOptionalInt(a.PeakBPM, b.PeakBPM) || !sameOptionalInt(a.EstimatedMaxHR, b.EstimatedMaxHR) || !sameOptionalNumber(a.WeightedMeanBPM, b.WeightedMeanBPM, 1e-6) || !sameOptionalNumber(a.ContactCoverage, b.ContactCoverage, 1e-6) || len(a.ExcludedByReason) != len(b.ExcludedByReason) {
		return false
	}
	for _, pair := range [][2]float64{{a.ExcludedSeconds, b.ExcludedSeconds}, {a.LowBPMSeconds, b.LowBPMSeconds}, {a.ValidSeconds, b.ValidSeconds}, {a.UnknownSeconds, b.UnknownSeconds}, {a.Coverage, b.Coverage}} {
		if !near(pair[0], pair[1], 1e-6) {
			return false
		}
	}
	for reason, value := range a.ExcludedByReason {
		other, exists := b.ExcludedByReason[reason]
		if !exists || !near(value, other, 1e-6) {
			return false
		}
	}
	if a.Zones == nil || b.Zones == nil {
		return a.Zones == nil && b.Zones == nil
	}
	x, y := a.Zones, b.Zones
	for _, pair := range [][2]float64{{x.Zone1Seconds, y.Zone1Seconds}, {x.Zone2Seconds, y.Zone2Seconds}, {x.Zone3Seconds, y.Zone3Seconds}, {x.Zone4Seconds, y.Zone4Seconds}, {x.Zone5Seconds, y.Zone5Seconds}, {x.Zone1Ratio, y.Zone1Ratio}, {x.Zone2Ratio, y.Zone2Ratio}, {x.Zone3Ratio, y.Zone3Ratio}, {x.Zone4Ratio, y.Zone4Ratio}, {x.Zone5Ratio, y.Zone5Ratio}} {
		if !near(pair[0], pair[1], 1e-6) {
			return false
		}
	}
	return true
}

func sameHybridMetrics(a, b *sensor.HybridMetrics) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.AlgorithmVersion != b.AlgorithmVersion || a.CalibrationVersion != b.CalibrationVersion || a.PeakExceededMaxHR != b.PeakExceededMaxHR || len(a.QualityFlags) != len(b.QualityFlags) {
		return false
	}
	for _, pair := range [][2]*float64{{a.HeartIntensity, b.HeartIntensity}, {a.MotionIntensity, b.MotionIntensity}, {a.HybridIntensity, b.HybridIntensity}, {a.HybridLoadAU, b.HybridLoadAU}, {a.ENMOP05G, b.ENMOP05G}, {a.ENMOP95G, b.ENMOP95G}} {
		if !sameOptionalNumber(pair[0], pair[1], 1e-6) {
			return false
		}
	}
	for _, pair := range [][2]float64{{a.ValidHybridSeconds, b.ValidHybridSeconds}, {a.ValidACCSeconds, b.ValidACCSeconds}, {a.DataCoverage, b.DataCoverage}} {
		if !near(pair[0], pair[1], 1e-6) {
			return false
		}
	}
	for i, flag := range a.QualityFlags {
		if flag != b.QualityFlags[i] {
			return false
		}
	}
	return true
}

func sameTimelineValue(a, b sensor.TimelineValue) bool {
	return a.Status == b.Status && a.Reason == b.Reason && sameOptionalNumber(a.Value, b.Value, 1e-6)
}
func sameOptionalTimelineValue(a, b *sensor.TimelineValue) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return sameTimelineValue(*a, *b)
}

func validateActivityResult(rows []db.ChunkAnalysisResult, summary activity.Summary) (bool, error) {
	switch summary.ReviewState {
	case "provisional", "queued", "running", "":
		return false, nil
	case "completed", "partial":
	default:
		return false, fmt.Errorf("activity review was not verified: %s", summary.ReviewState)
	}
	if !summary.Available || summary.Version != activity.Version || summary.ReviewVersion != activity.Version || summary.CoverageScope != "recorded_chunks" || summary.MediaGeneration == "" || summary.SourceVersion != activity.SourceVersion(rows) {
		return false, fmt.Errorf("activity summary identity or source version is stale")
	}
	reviews := map[uint]string{}
	for _, review := range summary.Reviews {
		if _, exists := reviews[review.ChunkID]; exists {
			return false, fmt.Errorf("duplicate activity review for chunk %d", review.ChunkID)
		}
		if review.State != "completed" {
			return false, fmt.Errorf("activity review processing failed for chunk %d: %s", review.ChunkID, review.State)
		}
		reviews[review.ChunkID] = review.State
	}
	if len(reviews) != len(rows) {
		return false, fmt.Errorf("activity review processing omitted recorded chunks")
	}
	for _, row := range rows {
		if reviews[row.ID] != "completed" {
			return false, fmt.Errorf("activity review missing for chunk %d", row.ID)
		}
	}
	for _, gap := range summary.Unassessed {
		if gap.Reason == "review_failed" || gap.Reason == "analysis_incomplete" || gap.Reason == "missing_or_invalid_observations" || gap.Reason == "overlapping_sources" {
			return false, fmt.Errorf("activity processing gap: %s", gap.Reason)
		}
	}
	return true, nil
}
