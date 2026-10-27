package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/wod-strategist/api/internal/sensor"
)

// Raw fields keep device nanosecond timestamps and future schema fields intact.
type telemetryEvent map[string]json.RawMessage

func telemetryNumber(event telemetryEvent, key string) (float64, error) {
	var value float64
	if err := json.Unmarshal(event[key], &value); err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("invalid telemetry %s", key)
	}
	return value, nil
}

func telemetryKind(event telemetryEvent) string {
	var kind string
	_ = json.Unmarshal(event["k"], &kind)
	return kind
}

func telemetrySet(event telemetryEvent, key string, value any) {
	// Callers only pass finite numbers and JSON-compatible values.
	event[key], _ = json.Marshal(value)
}

func readTelemetry(data []byte) ([]telemetryEvent, error) {
	if len(data) == 0 || len(data) > sensor.MaxFileSize {
		return nil, fmt.Errorf("telemetry must contain 1..%d bytes", sensor.MaxFileSize)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), sensor.MaxLineSize)
	var events []telemetryEvent
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event telemetryEvent
		if err := json.Unmarshal(line, &event); err != nil || event == nil || telemetryKind(event) == "" {
			return nil, fmt.Errorf("invalid telemetry event at line %d", len(events)+1)
		}
		if len(events) > 0 && telemetryKind(events[len(events)-1]) == "end" {
			return nil, fmt.Errorf("telemetry contains an event after its footer")
		}
		if len(events) > 0 && telemetryKind(event) == "meta" {
			return nil, fmt.Errorf("telemetry contains duplicate metadata")
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read telemetry: %w", err)
	}
	if len(events) < 2 || telemetryKind(events[0]) != "meta" || telemetryKind(events[len(events)-1]) != "end" {
		return nil, fmt.Errorf("telemetry requires a first metadata event and final footer")
	}
	var schema, clock string
	_ = json.Unmarshal(events[0]["schema_version"], &schema)
	_ = json.Unmarshal(events[0]["clock_source"], &clock)
	if schema != "2.0.0" || clock != "capture_clock" {
		return nil, fmt.Errorf("telemetry must use schema 2.0.0 and capture_clock")
	}
	return events, nil
}

func writeTelemetry(events []telemetryEvent) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return nil, fmt.Errorf("encode telemetry: %w", err)
		}
	}
	if output.Len() > sensor.MaxFileSize {
		return nil, fmt.Errorf("cropped telemetry exceeds the sensor upload size limit")
	}
	return output.Bytes(), nil
}

func identifyTelemetry(meta telemetryEvent, sessionID string, profileID uint, baseEpochMs int64) error {
	if sessionID == "" || profileID == 0 || baseEpochMs <= 0 {
		return fmt.Errorf("telemetry requires session ID, profile ID, and positive base epoch")
	}
	telemetrySet(meta, "workout_session_id", sessionID)
	telemetrySet(meta, "profile_id", profileID)
	telemetrySet(meta, "base_epoch_ms", baseEpochMs)
	return nil
}

func reidentifyTelemetry(data []byte, sessionID string, profileID uint, baseEpochMs int64) ([]byte, error) {
	events, err := readTelemetry(data)
	if err != nil {
		return nil, err
	}
	if err := identifyTelemetry(events[0], sessionID, profileID, baseEpochMs); err != nil {
		return nil, err
	}
	return writeTelemetry(events)
}

func telemetryAcc(event telemetryEvent) (float64, float64, []json.RawMessage, error) {
	t, err := telemetryNumber(event, "t")
	if err != nil {
		return 0, 0, nil, err
	}
	dt, err := telemetryNumber(event, "dt")
	if err != nil || dt <= 0 {
		return 0, 0, nil, fmt.Errorf("ACC packet requires a positive sample interval")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(event["v"], &values); err != nil {
		return 0, 0, nil, fmt.Errorf("invalid ACC samples: %w", err)
	}
	return t, dt, values, nil
}

func rebaseTelemetryEvent(event telemetryEvent, t, startMs float64) error {
	telemetrySet(event, "t", t-startMs)
	if telemetryKind(event) == "stream_start" && len(event["clock_anchor"]) > 0 && string(event["clock_anchor"]) != "null" {
		var anchor telemetryEvent
		if err := json.Unmarshal(event["clock_anchor"], &anchor); err != nil || anchor == nil {
			return fmt.Errorf("invalid stream clock anchor")
		}
		if len(anchor["capture_offset_ms"]) > 0 {
			offset, err := telemetryNumber(anchor, "capture_offset_ms")
			if err != nil {
				return err
			}
			// A carried-in anchor predates the crop. Its offset can be negative;
			// the original device clock and its relation to capture time are retained.
			telemetrySet(anchor, "capture_offset_ms", offset-startMs)
		}
		telemetrySet(event, "clock_anchor", anchor)
	}
	return nil
}

func cropTelemetry(data []byte, startMs, endMs float64, sessionID string, profileID uint, baseEpochMs int64) ([]byte, SensorStats, error) {
	var empty SensorStats
	if !finiteReplayInterval(startMs, endMs) {
		return nil, empty, fmt.Errorf("invalid telemetry crop interval")
	}
	events, err := readTelemetry(data)
	if err != nil {
		return nil, empty, err
	}
	footer := events[len(events)-1]
	duration, err := telemetryNumber(footer, "t")
	if err != nil || endMs > duration {
		return nil, empty, fmt.Errorf("telemetry crop extends beyond the sensor footer")
	}
	if err := identifyTelemetry(events[0], sessionID, profileID, baseEpochMs); err != nil {
		return nil, empty, err
	}
	var summary telemetryEvent
	if err := json.Unmarshal(footer["summary"], &summary); err != nil || summary == nil {
		return nil, empty, fmt.Errorf("telemetry footer requires a summary")
	}
	for _, key := range []string{"write_failures", "dropped_lines"} {
		if raw := summary[key]; len(raw) > 0 && string(raw) != "null" {
			value, err := telemetryNumber(summary, key)
			if err != nil || value != 0 {
				return nil, empty, fmt.Errorf("source telemetry has %s; cannot create a complete replay", key)
			}
		}
	}

	var ready, stream telemetryEvent
	openGaps := make(map[string]telemetryEvent)
	var body []telemetryEvent
	var battery json.RawMessage
	for _, event := range events[1 : len(events)-1] {
		t, err := telemetryNumber(event, "t")
		kind := telemetryKind(event)
		if err != nil || (t < 0 && kind != "acc") {
			return nil, empty, fmt.Errorf("telemetry contains invalid event time")
		}
		if t < endMs {
			if kind == "battery" {
				battery = event["percent"]
			} else if kind == "device_ready" {
				var device telemetryEvent
				if json.Unmarshal(event["device"], &device) == nil && len(device["battery_percent_start"]) > 0 {
					battery = device["battery_percent_start"]
				}
			}
		}
		if t < startMs {
			switch kind {
			case "device_ready":
				ready = event
			case "stream_start":
				stream = event
			case "gap_start":
				openGaps[string(event["gap_id"])] = event
			case "gap_end":
				delete(openGaps, string(event["gap_id"]))
			}
		}
		if kind == "acc" {
			packetT, dt, values, err := telemetryAcc(event)
			if err != nil {
				return nil, empty, err
			}
			first, last := 0, len(values)
			for first < last && packetT+float64(first)*dt < startMs {
				first++
			}
			for last > first && packetT+float64(last-1)*dt >= endMs {
				last--
			}
			if first == last {
				continue
			}
			telemetrySet(event, "t", packetT+float64(first)*dt-startMs)
			telemetrySet(event, "v", values[first:last])
			body = append(body, event)
		} else if t >= startMs && t < endMs {
			if err := rebaseTelemetryEvent(event, t, startMs); err != nil {
				return nil, empty, err
			}
			body = append(body, event)
		}
	}
	output := []telemetryEvent{events[0]}
	for _, carried := range []telemetryEvent{ready, stream} {
		if carried != nil {
			t, _ := telemetryNumber(carried, "t")
			if err := rebaseTelemetryEvent(carried, t, startMs); err != nil {
				return nil, empty, err
			}
			telemetrySet(carried, "t", 0)
			output = append(output, carried)
		}
	}
	keys := make([]string, 0, len(openGaps))
	for key := range openGaps {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		event := openGaps[key]
		telemetrySet(event, "t", 0)
		output = append(output, event)
	}
	output = append(output, body...)

	var pauses []telemetryEvent
	if raw := footer["pause_intervals"]; len(raw) > 0 && string(raw) != "null" {
		var original []telemetryEvent
		if err := json.Unmarshal(raw, &original); err != nil {
			return nil, empty, fmt.Errorf("invalid sensor pause intervals")
		}
		for _, pause := range original {
			start, errStart := telemetryNumber(pause, "start_offset_ms")
			end, errEnd := telemetryNumber(pause, "end_offset_ms")
			if errStart != nil || errEnd != nil || end < start {
				return nil, empty, fmt.Errorf("invalid sensor pause interval")
			}
			start, end = math.Max(start, startMs), math.Min(end, endMs)
			if end > start {
				telemetrySet(pause, "start_offset_ms", start-startMs)
				telemetrySet(pause, "end_offset_ms", end-startMs)
				pauses = append(pauses, pause)
			}
		}
	}
	if pauses == nil {
		pauses = []telemetryEvent{}
	}
	telemetrySet(footer, "pause_intervals", pauses)
	telemetrySet(footer, "t", endMs-startMs)
	var footerDevice telemetryEvent
	_ = json.Unmarshal(footer["device"], &footerDevice)
	if footerDevice == nil {
		footerDevice = make(telemetryEvent)
	}
	if len(battery) == 0 {
		battery = json.RawMessage("null")
	}
	footerDevice["battery_percent_end"] = battery
	telemetrySet(footer, "device", footerDevice)
	output = append(output, footer)
	encoded, err := writeTelemetry(output)
	if err != nil {
		return nil, empty, err
	}
	stats, err := telemetryStats(encoded)
	if err != nil {
		return nil, empty, err
	}
	gapCount := 0
	for _, event := range output {
		if telemetryKind(event) == "gap_start" {
			gapCount++
		}
	}
	telemetrySet(summary, "hr_samples", stats.HRSamples)
	telemetrySet(summary, "acc_samples", stats.ACCSamples)
	telemetrySet(summary, "gaps", gapCount)
	// A whole-record nonzero packet-loss count cannot be attributed to this crop.
	if raw := summary["dropped_packets"]; len(raw) == 0 || string(raw) != "0" {
		telemetrySet(summary, "dropped_packets", nil)
	}
	telemetrySet(summary, "write_failures", 0)
	telemetrySet(summary, "dropped_lines", 0)
	telemetrySet(footer, "summary", summary)
	encoded, err = writeTelemetry(output)
	return encoded, stats, err
}

func telemetryStats(data []byte) (SensorStats, error) {
	var stats SensorStats
	events, err := readTelemetry(data)
	if err != nil {
		return stats, err
	}
	duration, err := telemetryNumber(events[len(events)-1], "t")
	if err != nil || duration <= 0 {
		return stats, fmt.Errorf("telemetry requires a positive footer duration")
	}
	stats.DurationMs = duration
	observe := func(t float64, first, last **float64) {
		if *first == nil || t < **first {
			v := t
			*first = &v
		}
		if *last == nil || t > **last {
			v := t
			*last = &v
		}
	}
	for _, event := range events[1 : len(events)-1] {
		switch telemetryKind(event) {
		case "hr":
			t, err := telemetryNumber(event, "t")
			if err != nil || t < 0 {
				return stats, fmt.Errorf("invalid HR timestamp")
			}
			if t >= duration {
				continue
			}
			stats.HRSamples++
			observe(t, &stats.FirstHRMs, &stats.LastHRMs)
		case "acc":
			t, dt, samples, err := telemetryAcc(event)
			if err != nil {
				return stats, err
			}
			for i := range samples {
				sampleT := t + float64(i)*dt
				if math.IsNaN(sampleT) || math.IsInf(sampleT, 0) {
					return stats, fmt.Errorf("invalid ACC sample timestamp")
				}
				if sampleT >= 0 && sampleT < duration {
					stats.ACCSamples++
					observe(sampleT, &stats.FirstACCMs, &stats.LastACCMs)
				}
			}
		}
	}
	return stats, nil
}

func peakHeartRate(data []byte, startMs, endMs float64) (int, error) {
	if !finiteReplayInterval(startMs, endMs) {
		return 0, fmt.Errorf("invalid heart rate interval")
	}
	events, err := readTelemetry(data)
	if err != nil {
		return 0, err
	}
	var quality sensor.HRQuality
	hasHistory := false
	peak := 0
	for _, event := range events[1 : len(events)-1] {
		kind := telemetryKind(event)
		if kind != "hr" && kind != "device_ready" && kind != "gap_start" && kind != "gap_end" {
			continue
		}
		t, err := telemetryNumber(event, "t")
		if err != nil || t < 0 {
			return 0, fmt.Errorf("invalid heart rate event time")
		}
		if t >= endMs {
			continue
		}
		if kind != "hr" {
			quality.Reset(hasHistory)
			hasHistory = true
			continue
		}
		var hr sensor.HREvent
		encoded, _ := json.Marshal(event)
		if err := json.Unmarshal(encoded, &hr); err != nil {
			return 0, fmt.Errorf("invalid heart rate sample: %w", err)
		}
		reason := quality.Evaluate(hr)
		hasHistory = true
		if t >= startMs && (reason == "valid" || reason == "low") && hr.BPM > peak {
			peak = hr.BPM
		}
	}
	return peak, nil
}

func finiteReplayInterval(start, end float64) bool {
	return !math.IsNaN(start) && !math.IsNaN(end) && !math.IsInf(start, 0) && !math.IsInf(end, 0) && start >= 0 && end > start
}

func sortedReplayMapping(segments []MappingSegment) ([]MappingSegment, error) {
	ordered := append([]MappingSegment(nil), segments...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].MediaStartMs < ordered[j].MediaStartMs })
	for i, segment := range ordered {
		if !finiteReplayInterval(segment.CaptureStartMs, segment.CaptureEndMs) || !finiteReplayInterval(segment.MediaStartMs, segment.MediaEndMs) {
			return nil, fmt.Errorf("invalid verified video mapping segment")
		}
		if i > 0 && (segment.MediaStartMs < ordered[i-1].MediaEndMs || segment.CaptureStartMs < ordered[i-1].CaptureEndMs) {
			return nil, fmt.Errorf("verified video mapping overlaps or reverses")
		}
	}
	return ordered, nil
}

func captureAtMedia(segments []MappingSegment, mediaMs float64) (float64, error) {
	if math.IsNaN(mediaMs) || math.IsInf(mediaMs, 0) || mediaMs < 0 {
		return 0, fmt.Errorf("invalid media timestamp")
	}
	ordered, err := sortedReplayMapping(segments)
	if err != nil {
		return 0, err
	}
	for _, segment := range ordered {
		if mediaMs >= segment.MediaStartMs && mediaMs < segment.MediaEndMs {
			return segment.CaptureStartMs + (mediaMs-segment.MediaStartMs)/(segment.MediaEndMs-segment.MediaStartMs)*(segment.CaptureEndMs-segment.CaptureStartMs), nil
		}
	}
	// Only the final endpoint is included; internal boundaries belong to the
	// following segment so capture gaps are not silently bridged.
	if len(ordered) > 0 && mediaMs == ordered[len(ordered)-1].MediaEndMs {
		return ordered[len(ordered)-1].CaptureEndMs, nil
	}
	return 0, fmt.Errorf("media timestamp %.6fms has no verified capture mapping", mediaMs)
}

func validateMapping(segments []MappingSegment, startMs, endMs float64) error {
	if !finiteReplayInterval(startMs, endMs) {
		return fmt.Errorf("invalid requested media interval")
	}
	ordered, err := sortedReplayMapping(segments)
	if err != nil {
		return err
	}
	cursor := startMs
	for _, segment := range ordered {
		if segment.MediaEndMs <= cursor {
			continue
		}
		if segment.MediaStartMs > cursor {
			return fmt.Errorf("media interval has no verified mapping at %.6fms", cursor)
		}
		cursor = segment.MediaEndMs
		if cursor >= endMs {
			return nil
		}
	}
	return fmt.Errorf("verified mapping ends before requested media interval")
}
