package sensor

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// TimelineBuilder spools only ACC and stream events until the authoritative
// footer duration is known. Replaying a bounded private file clips every sample
// before aggregation, without retaining the high-frequency signal in memory or
// reading the source object a second time. Build and Close both remove the file.
// Callers that may exit before Build must defer Close.
type TimelineBuilder struct {
	file   *os.File
	writer *bufio.Writer
	size   int
	err    error
}

func NewTimelineBuilder() *TimelineBuilder { return &TimelineBuilder{} }
func (tb *TimelineBuilder) Err() error     { return tb.err }

func (tb *TimelineBuilder) Close() {
	if tb.file != nil {
		name := tb.file.Name()
		_ = tb.file.Close()
		_ = os.Remove(name)
		tb.file = nil
		tb.writer = nil
	}
}

func (tb *TimelineBuilder) appendEvent(event any) {
	if tb.err != nil {
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		tb.err = errors.New("timeline_event_serialization_failed")
		return
	}
	// Allow encoding overhead above the raw source limit, but never an unbounded
	// spool when the builder is used independently of the parser.
	if len(data) >= MaxLineSize || tb.size+len(data)+1 > 2*MaxFileSize {
		tb.err = errors.New("timeline_spool_limit_exceeded")
		return
	}
	if tb.file == nil {
		tb.file, err = os.CreateTemp("", "sensor-timeline-*.ndjson")
		if err != nil {
			tb.err = errors.New("timeline_spool_create_failed")
			return
		}
		tb.writer = bufio.NewWriter(tb.file)
	}
	if _, err = tb.writer.Write(data); err == nil {
		err = tb.writer.WriteByte('\n')
	}
	if err != nil {
		tb.err = errors.New("timeline_spool_write_failed")
		return
	}
	tb.size += len(data) + 1
}

func (tb *TimelineBuilder) HandleStreamStart(event StreamStartEvent) {
	event.Kind = "stream_start"
	tb.appendEvent(event)
}

func (tb *TimelineBuilder) HandleAcc(event AccEvent) {
	event.Kind = "acc"
	tb.appendEvent(event)
}

func (tb *TimelineBuilder) Build(source TimelineSource, durationMs float64, pauses []PauseInterval, hrEvents []hrObservation) *SensorTimelineData {
	defer tb.Close()
	if tb.err != nil {
		return nil
	}
	if !validTimelineTime(durationMs) {
		tb.err = errors.New("invalid_timeline_duration")
		return nil
	}
	accumulator := newTimelineAccumulator(durationMs)
	if tb.file != nil {
		if err := tb.writer.Flush(); err != nil {
			tb.err = errors.New("timeline_spool_write_failed")
			return nil
		}
		if _, err := tb.file.Seek(0, io.SeekStart); err != nil {
			tb.err = errors.New("timeline_spool_read_failed")
			return nil
		}
		scanner := bufio.NewScanner(tb.file)
		scanner.Buffer(make([]byte, 64*1024), MaxLineSize)
		for scanner.Scan() {
			var header EventHeader
			if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
				tb.err = err
				return nil
			}
			switch header.Kind {
			case "stream_start":
				var event StreamStartEvent
				if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
					tb.err = err
					return nil
				}
				accumulator.HandleStreamStart(event)
			case "acc":
				var event AccEvent
				if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
					tb.err = err
					return nil
				}
				accumulator.HandleAcc(event)
			}
			if accumulator.err != nil {
				tb.err = accumulator.err
				return nil
			}
		}
		if err := scanner.Err(); err != nil {
			tb.err = errors.New("timeline_spool_read_failed")
			return nil
		}
	}
	var timeline *SensorTimelineData
	timeline, tb.err = accumulator.Build(source, durationMs, pauses, hrEvents)
	return timeline
}
