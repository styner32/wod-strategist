package main

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/sensor"
)

const telemetrySourceFixture = `{"k":"meta","schema_version":"2.0.0","clock_source":"capture_clock","workout_session_id":"source","profile_id":1,"base_epoch_ms":1700000000000,"future":{"exact":9007199254740993}}
{"k":"device_ready","t":0,"stream_id":1,"device":{"name":"Polar H10","battery_percent_start":90,"future":"keep"}}
{"k":"stream_start","t":0,"stream_id":1,"sampling":{"acc_hz":50,"acc_range_g":8,"acc_resolution_bits":16},"clock_anchor":{"device_timestamp_ns":987654321012345678,"capture_offset_ms":0,"method":"first_packet_received"}}
{"k":"hr","t":950,"bpm":140,"rr":[428.57],"contact":true}
{"k":"acc","t":980,"stream_id":1,"dt":20,"v":[[0,0,1],[0.1,0,1],[0.2,0,1]],"packet_extra":9007199254740993}
{"k":"hr","t":1000,"bpm":150,"rr":[400],"contact":true,"future_hr":"retained"}
{"k":"hr","t":2000,"bpm":160,"rr":[375],"contact":true}
{"k":"future_event","t":2500,"payload":{"big":9007199254740993}}
{"k":"acc","t":2980,"stream_id":1,"dt":20,"v":[[0.3,0,1],[0.4,0,1],[0.5,0,1]]}
{"k":"hr","t":3000,"bpm":170}
{"k":"end","t":6000,"pause_intervals":[{"start_offset_ms":500,"end_offset_ms":1100,"reason":"kept"},{"start_offset_ms":2000,"end_offset_ms":6000}],"device":{"battery_percent_end":55,"future_device":"kept"},"summary":{"hr_samples":99,"acc_samples":999,"dropped_packets":2,"gaps":0,"write_failures":0,"dropped_lines":0,"future_counter":9007199254740993}}
`

var _ = Describe("Live replay telemetry", func() {
	It("cuts ACC arrays at sample boundaries, rebases capture time, and restores stream context", func() {
		cropped, stats, err := cropTelemetry([]byte(telemetrySourceFixture), 1000, 3000, "WOD-20261026-01M3QZH8TPXYMBGP9ER5W6SMBB", 10, 1793000000000)
		Expect(err).NotTo(HaveOccurred())
		Expect(stats.DurationMs).To(Equal(2000.0))
		Expect(stats.HRSamples).To(Equal(2))
		Expect(stats.ACCSamples).To(Equal(3))
		Expect(*stats.FirstHRMs).To(Equal(0.0))
		Expect(*stats.LastHRMs).To(Equal(1000.0))
		Expect(*stats.FirstACCMs).To(Equal(0.0))
		Expect(*stats.LastACCMs).To(Equal(1980.0))
		events, err := readTelemetry(cropped)
		Expect(err).NotTo(HaveOccurred())
		Expect(telemetryKind(events[1])).To(Equal("device_ready"))
		Expect(telemetryKind(events[2])).To(Equal("stream_start"))
		Expect(string(events[0]["profile_id"])).To(Equal("10"))
		Expect(string(events[0]["base_epoch_ms"])).To(Equal("1793000000000"))
		Expect(string(cropped)).To(ContainSubstring(`"device_timestamp_ns":987654321012345678`))
		Expect(string(cropped)).To(ContainSubstring(`"capture_offset_ms":-1000`))
		Expect(string(cropped)).To(ContainSubstring(`"packet_extra":9007199254740993`))
		Expect(string(cropped)).To(ContainSubstring(`"future_hr":"retained"`))
		Expect(string(cropped)).To(ContainSubstring(`"future_counter":9007199254740993`))
		Expect(string(cropped)).To(ContainSubstring(`"payload":{"big":9007199254740993}`))
		var footer struct {
			PauseIntervals []struct {
				Start float64 `json:"start_offset_ms"`
				End   float64 `json:"end_offset_ms"`
			} `json:"pause_intervals"`
			Summary struct {
				HR      int  `json:"hr_samples"`
				ACC     int  `json:"acc_samples"`
				Dropped *int `json:"dropped_packets"`
			} `json:"summary"`
			Device struct {
				Battery int `json:"battery_percent_end"`
			} `json:"device"`
		}
		last, err := json.Marshal(events[len(events)-1])
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(last, &footer)).To(Succeed())
		Expect(footer.Summary.HR).To(Equal(2))
		Expect(footer.Summary.ACC).To(Equal(3))
		Expect(footer.Summary.Dropped).To(BeNil())
		Expect(footer.Device.Battery).To(Equal(90))
		Expect(footer.PauseIntervals).To(HaveLen(2))
		Expect(footer.PauseIntervals[0].Start).To(Equal(0.0))
		Expect(footer.PauseIntervals[0].End).To(Equal(100.0))
		Expect(footer.PauseIntervals[1].Start).To(Equal(1000.0))
		Expect(footer.PauseIntervals[1].End).To(Equal(2000.0))
		result, err := sensor.ParseAndProcessWithTimeline(bytes.NewReader(cropped), sensor.ParseOptions{
			CalculationVersion: 3, ExpectedProfileID: 10,
			ExpectedSessionID: "WOD-20261026-01M3QZH8TPXYMBGP9ER5W6SMBB", ExpectedSHA256: digest(cropped), ExpectedSizeBytes: int64(len(cropped)),
		}, sensor.TimelineSource{SensorVersion: "1", RequestID: "fixture", HRCalculationVersion: 3})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Summary.Quality.IsComplete).To(BeTrue())
		Expect(result.Summary.Metrics.DurationSeconds).To(Equal(0.9))
		Expect(result.Summary.Metrics.PauseSeconds).To(Equal(1.1))
		Expect(result.TimelineError).NotTo(HaveOccurred())
	})

	It("preserves carried gaps and clipped pauses without adding synthetic HR samples", func() {
		source := strings.Replace(telemetrySourceFixture,
			`{"k":"hr","t":950`,
			"{\"k\":\"gap_start\",\"t\":900,\"gap_id\":7,\"reason\":\"disconnected\"}\n{\"k\":\"hr\",\"t\":950", 1)
		source = strings.Replace(source, `{"k":"hr","t":2000`, "{\"k\":\"gap_end\",\"t\":1900,\"gap_id\":7}\n{\"k\":\"hr\",\"t\":2000", 1)
		cropped, stats, err := cropTelemetry([]byte(source), 1000, 3000, "new", 10, 1700000000000)
		Expect(err).NotTo(HaveOccurred())
		Expect(stats.HRSamples).To(Equal(2))
		Expect(string(cropped)).To(ContainSubstring(`"gap_id":7,"k":"gap_start","reason":"disconnected","t":0`))
		Expect(string(cropped)).To(ContainSubstring(`"gaps":1`))
		Expect(string(cropped)).To(ContainSubstring(`"gap_id":7,"k":"gap_end","t":900`))
	})

	It("changes only recording identity when reusing a prepared fixture", func() {
		output, err := reidentifyTelemetry([]byte(telemetrySourceFixture), "next", 10, 1800000000000)
		Expect(err).NotTo(HaveOccurred())
		before, err := readTelemetry([]byte(telemetrySourceFixture))
		Expect(err).NotTo(HaveOccurred())
		after, err := readTelemetry(output)
		Expect(err).NotTo(HaveOccurred())
		Expect(after[1:]).To(Equal(before[1:]))
		Expect(string(after[0]["workout_session_id"])).To(Equal(`"next"`))
		Expect(string(after[0]["future"])).To(Equal(string(before[0]["future"])))
	})

	DescribeTable("rejects unusable source data",
		func(source string, start, end float64) {
			_, _, err := cropTelemetry([]byte(source), start, end, "next", 10, 1700000000000)
			Expect(err).To(HaveOccurred())
		},
		Entry("missing footer", "{\"k\":\"meta\"}\n", 0.0, 1000.0),
		Entry("past footer", telemetrySourceFixture, 1000.0, 7000.0),
		Entry("reversed interval", telemetrySourceFixture, 3000.0, 1000.0),
		Entry("nonfinite interval", telemetrySourceFixture, 0.0, math.Inf(1)),
		Entry("failed original writes", strings.Replace(telemetrySourceFixture, `"write_failures":0`, `"write_failures":1`, 1), 1000.0, 3000.0),
		Entry("content after footer", telemetrySourceFixture+"{\"k\":\"hr\",\"t\":6001,\"bpm\":150}\n", 1000.0, 3000.0),
	)

	It("uses accepted HR observations for chunk peaks and excludes the next chunk boundary", func() {
		data := `{"k":"meta","schema_version":"2.0.0","clock_source":"capture_clock"}
{"k":"device_ready","t":0}
{"k":"hr","t":0,"bpm":150,"contact":true}
{"k":"hr","t":1000,"bpm":155,"contact":true}
{"k":"hr","t":2000,"bpm":230,"contact":false}
{"k":"hr","t":3000,"bpm":400}
{"k":"hr","t":4000,"bpm":160,"contact":true}
{"k":"hr","t":9000,"bpm":175,"contact":true}
{"k":"hr","t":10000,"bpm":180,"contact":true}
{"k":"end","t":11000,"pause_intervals":[],"summary":{}}
`
		peak, err := peakHeartRate([]byte(data), 0, 2000)
		Expect(err).NotTo(HaveOccurred())
		Expect(peak).To(Equal(155))
		peak, err = peakHeartRate([]byte(data), 2000, 9000)
		Expect(err).NotTo(HaveOccurred())
		Expect(peak).To(Equal(0))
	})

	It("retains accepted low BPM observations rather than replacing them with missing HR", func() {
		data := `{"k":"meta","schema_version":"2.0.0","clock_source":"capture_clock"}
{"k":"device_ready","t":0}
{"k":"hr","t":0,"bpm":50,"contact":true}
{"k":"hr","t":1000,"bpm":52,"contact":true}
{"k":"end","t":2000,"pause_intervals":[],"summary":{}}
`
		peak, err := peakHeartRate([]byte(data), 0, 2000)
		Expect(err).NotTo(HaveOccurred())
		Expect(peak).To(Equal(52))
	})
})

var _ = Describe("Live replay capture mapping", func() {
	It("maps media proportionally while keeping source capture gaps", func() {
		segments := []MappingSegment{
			{CaptureStartMs: 100.0, CaptureEndMs: 10100, MediaStartMs: 0, MediaEndMs: 10033.333},
			{CaptureStartMs: 10101.68, CaptureEndMs: 20101.68, MediaStartMs: 10033.333, MediaEndMs: 20033.333},
		}
		Expect(validateMapping(segments, 1000, 19000)).To(Succeed())
		capture, err := captureAtMedia(segments, 5016.6665)
		Expect(err).NotTo(HaveOccurred())
		Expect(capture).To(BeNumerically("~", 5100.0, 0.000001))
		capture, err = captureAtMedia(segments, 10033.333)
		Expect(err).NotTo(HaveOccurred())
		Expect(capture).To(Equal(10101.68))
		capture, err = captureAtMedia(segments, 20033.333)
		Expect(err).NotTo(HaveOccurred())
		Expect(capture).To(Equal(20101.68))
	})

	DescribeTable("rejects missing, overlapping, and unverified mappings",
		func(segments []MappingSegment) {
			Expect(validateMapping(segments, 0, 2000)).NotTo(Succeed())
		},
		Entry("missing", []MappingSegment{}),
		Entry("media gap", []MappingSegment{{0, 900, 0, 900}, {1000, 2000, 1000, 2000}}),
		Entry("overlapping media", []MappingSegment{{0, 1000, 0, 1000}, {1000, 2100, 900, 2000}}),
		Entry("overlapping capture", []MappingSegment{{0, 1000, 0, 1000}, {900, 1900, 1000, 2000}}),
		Entry("invalid endpoint", []MappingSegment{{0, math.NaN(), 0, 2000}}),
	)
})
