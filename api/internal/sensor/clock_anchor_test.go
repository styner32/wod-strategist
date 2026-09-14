package sensor_test

import (
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/sensor"
)

var _ = Describe("Sensor stream clock anchor", func() {
	DescribeTable("decodes integer nanoseconds without floating-point precision loss", func(raw string, expected int64) {
		var event sensor.StreamStartEvent
		content := fmt.Sprintf(`{"k":"stream_start","clock_anchor":{"device_timestamp_ns":%s,"capture_offset_ms":4585,"method":"first_packet_received"}}`, raw)
		Expect(json.Unmarshal([]byte(content), &event)).To(Succeed())
		Expect(int64(event.ClockAnchor.DeviceTimestampNs)).To(Equal(expected))
		Expect(event.ClockAnchor.CaptureOffsetMs).To(Equal(4585.0))
		Expect(event.ClockAnchor.Method).To(Equal("first_packet_received"))
	},
		Entry("mobile string above JavaScript's safe integer range", `"599617123172966688"`, int64(599617123172966688)),
		Entry("legacy number above JavaScript's safe integer range", `599617123172966688`, int64(599617123172966688)),
		Entry("legacy small number", `100000`, int64(100000)),
		Entry("maximum int64 string", `"9223372036854775807"`, int64(9223372036854775807)),
		Entry("null retains the existing zero-value behavior", `null`, int64(0)),
	)

	DescribeTable("rejects malformed or out-of-range nanoseconds", func(raw string) {
		var event sensor.StreamStartEvent
		content := fmt.Sprintf(`{"clock_anchor":{"device_timestamp_ns":%s}}`, raw)
		Expect(json.Unmarshal([]byte(content), &event)).NotTo(Succeed())
	},
		Entry("non-numeric string", `"invalid"`),
		Entry("fractional string", `"1.5"`),
		Entry("fractional number", `1.5`),
		Entry("overflowing string", `"9223372036854775808"`),
		Entry("overflowing number", `9223372036854775808`),
	)

	DescribeTable("applies the negotiated ACC rate for either timestamp encoding", func(raw string) {
		// A complete 25 Hz window would be classified unknown if stream_start
		// failed to decode and the parser silently fell back to 50 Hz.
		samples := strings.TrimSuffix(strings.Repeat("[0,0,1],", 25), ",")
		content := fmt.Sprintf(`{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-clock-anchor","profile_id":1}
{"k":"stream_start","t":0,"stream_id":1,"sampling":{"acc_hz":25},"clock_anchor":{"device_timestamp_ns":%s,"capture_offset_ms":0,"method":"first_packet_received"}}
{"k":"hr","t":0,"bpm":150}
{"k":"acc","t":0,"stream_id":1,"dt":40,"v":[%s]}
{"k":"acc","t":1000,"stream_id":1,"dt":40,"v":[[0,0,1]]}
{"k":"hr","t":1000,"bpm":150}
{"k":"end","t":2000}
`, raw, samples)
		for _, version := range []int{1, 2} {
			result, err := sensor.ParseAndProcess(strings.NewReader(content), sensor.ParseOptions{CalculationVersion: version})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Quality.IsComplete).To(BeTrue())
			Expect(result.Quality.Warnings).To(BeEmpty())
			Expect(result.Metrics.ACC.LowMovementSeconds).To(Equal(1.0))
			Expect(result.Metrics.ACC.UnknownMovementSeconds).To(Equal(1.0))
			Expect(result.Metrics.HR.WeightedMeanBPM).To(HaveValue(Equal(150.0)))
		}
	},
		Entry("mobile string", `"599617123172966688"`),
		Entry("legacy number", `599617123172966688`),
	)
})
