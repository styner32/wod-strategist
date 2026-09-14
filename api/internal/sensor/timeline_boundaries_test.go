package sensor_test

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/sensor"
)

var _ = Describe("Sensor timeline bounds and lifecycle", func() {
	var source sensor.TimelineSource
	var stream sensor.StreamStartEvent
	samples := func(count int, magnitude float64) [][]float64 {
		result := make([][]float64, count)
		for i := range result {
			result[i] = []float64{0, 0, magnitude}
		}
		return result
	}
	BeforeEach(func() {
		source = sensor.TimelineSource{HRCalculationVersion: 2}
		stream = sensor.StreamStartEvent{StreamID: 1}
		stream.Sampling.ACCHz = 50
		stream.Sampling.ACCRangeG = 8
	})

	DescribeTable("rejects unsafe duration without allocating or panicking", func(duration float64) {
		tb := sensor.NewTimelineBuilder()
		Expect(tb.Build(source, duration, nil, nil)).To(BeNil())
		Expect(tb.Err()).To(MatchError("invalid_timeline_duration"))
	}, Entry("out of integer range", 1e25), Entry("negative", -1.0), Entry("NaN", math.NaN()), Entry("infinity", math.Inf(1)))

	It("represents a years-long empty span with one gap", func() {
		tb := sensor.NewTimelineBuilder()
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Dt: 20, V: samples(50, 1)})
		timeline := tb.Build(source, 1e12, nil, nil)
		Expect(tb.Err()).NotTo(HaveOccurred())
		Expect(timeline.Points).To(HaveLen(1))
		Expect(timeline.Gaps).To(Equal([]sensor.TimelineGap{{StartMs: 1000, EndMs: 1e12, Channel: "both", Reason: "missing_sensor_data"}}))
	})

	It("bounds observed HR output independently of elapsed duration", func() {
		tb := sensor.NewTimelineBuilder()
		events := make([]sensor.HREvent, sensor.MaxTimelineBuckets+2)
		for i := range events {
			events[i] = sensor.HREvent{Time: float64(i) * 1000, BPM: 150}
		}
		Expect(tb.BuildWithHREvents(source, float64(len(events))*1000, nil, events)).To(BeNil())
		Expect(tb.Err()).To(MatchError("timeline_bucket_limit_exceeded"))
	})

	It("clips samples and sample errors after the true footer end, even in earlier buckets", func() {
		tb := sensor.NewTimelineBuilder()
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Dt: 20, V: samples(75, 1)})
		// This tail extends beyond the final partial bucket and contains saturation.
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Time: 1500, Dt: 20, V: samples(125, 9)})
		timeline := tb.Build(source, 1500, nil, nil)
		Expect(tb.Err()).NotTo(HaveOccurred())
		Expect(timeline.Points).To(HaveLen(2))
		Expect(timeline.Points[1].EndMs).To(Equal(int64(1500)))
		Expect(timeline.Points[1].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
		Expect(timeline.Points[1].AccMagnitudeStdG.Value).To(HaveValue(Equal(0.0)))
	})

	It("does not let post-end samples satisfy coverage in the final bucket", func() {
		tb := sensor.NewTimelineBuilder()
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Time: 1400, Dt: 20, V: samples(30, 1)})
		timeline := tb.Build(source, 1500, nil, nil)
		Expect(timeline.Points[1].AccMagnitudeStdG.Status).To(Equal(sensor.StatusInsufficient))
		Expect(timeline.Points[1].AccMagnitudeStdG.Value).To(BeNil())
	})

	It("preserves completed data when a stream reconnects after a gap", func() {
		tb := sensor.NewTimelineBuilder()
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Dt: 20, V: samples(50, 1)})
		stream.StreamID, stream.Time = 2, 5000
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 2, Time: 5000, Dt: 20, V: samples(50, 1)})
		timeline := tb.Build(source, 6000, nil, nil)
		Expect(timeline.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
		Expect(timeline.Points[len(timeline.Points)-1].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
	})

	It("invalidates only the bucket crossing a stream configuration change", func() {
		tb := sensor.NewTimelineBuilder()
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Dt: 20, V: samples(75, 1)})
		stream.Time, stream.Sampling.ACCHz = 1500, 25
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Time: 1520, Dt: 40, V: samples(37, 1)})
		timeline := tb.Build(source, 3000, nil, nil)
		Expect(timeline.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
		Expect(timeline.Points[1].AccMagnitudeStdG.Reason).To(Equal("stream_changed"))
		Expect(timeline.Points[2].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
	})

	It("remembers an in-second reconnect before its first packet arrives", func() {
		tb := sensor.NewTimelineBuilder()
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Dt: 20, V: samples(50, 1)})
		stream.StreamID, stream.Time = 2, 5020
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 2, Time: 5020, Dt: 20, V: samples(49, 1)})
		timeline := tb.Build(source, 6000, nil, nil)
		Expect(timeline.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
		Expect(timeline.Points[len(timeline.Points)-1].AccMagnitudeStdG.Reason).To(Equal("stream_changed"))
	})

	It("discards pre-start samples in a packet that crosses the capture start", func() {
		tb := sensor.NewTimelineBuilder()
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Time: -500, Dt: 20, V: append(samples(25, 9), samples(50, 1)...)})
		timeline := tb.Build(source, 1000, nil, nil)
		Expect(tb.Err()).NotTo(HaveOccurred())
		Expect(timeline.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
		Expect(timeline.Points[0].AccMagnitudeStdG.Value).To(HaveValue(Equal(0.0)))
	})

	It("detects a complete packet reversing across a bucket boundary", func() {
		tb := sensor.NewTimelineBuilder()
		tb.HandleStreamStart(stream)
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Time: 1000, Dt: 20, V: samples(50, 1)})
		tb.HandleAcc(sensor.AccEvent{StreamID: 1, Time: 0, Dt: 20, V: samples(50, 1)})
		timeline := tb.Build(source, 2000, nil, nil)
		Expect(timeline.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusInvalid))
		Expect(timeline.Points[0].AccMagnitudeStdG.Reason).To(Equal("timestamp_reversed"))
		Expect(timeline.Points[1].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
	})

	Context("parser failure isolation and spool cleanup", func() {
		content := func(end string) string {
			return fmt.Sprintf(`{"k":"meta","schema_version":"2.0.0"}
{"k":"stream_start","t":0,"stream_id":1,"sampling":{"acc_hz":50,"acc_range_g":8}}
{"k":"hr","t":0,"bpm":150}
{"k":"acc","t":0,"stream_id":1,"dt":20,"v":[[0,0,1],[0,0,1]]}
{"k":"hr","t":1000,"bpm":150}
%s`, end)
		}
		var directory string
		BeforeEach(func() {
			directory = GinkgoT().TempDir()
			GinkgoT().Setenv("TMPDIR", directory)
		})
		AfterEach(func() {
			entries, err := os.ReadDir(directory)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
		})
		It("retains summary on unsafe duration and removes the spool", func() {
			result, err := sensor.ParseAndProcessWithTimeline(strings.NewReader(content(`{"k":"end","t":1e25}`)), sensor.ParseOptions{CalculationVersion: 2}, source)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Timeline).To(BeNil())
			Expect(result.TimelineError).To(MatchError("invalid_timeline_duration"))
			Expect(result.Summary.Quality.IsComplete).To(BeTrue())
			Expect(result.Summary.Metrics.HR.WeightedMeanBPM).To(HaveValue(Equal(150.0)))
		})
		It("retains the complete HR summary when a spool cannot be created", func() {
			GinkgoT().Setenv("TMPDIR", filepath.Join(directory, "missing-directory"))
			result, err := sensor.ParseAndProcessWithTimeline(strings.NewReader(content(`{"k":"end","t":1000}`)), sensor.ParseOptions{CalculationVersion: 2}, source)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.TimelineError).To(MatchError("timeline_spool_create_failed"))
			Expect(result.Summary.Quality.ValidHR).To(BeTrue())
			Expect(result.Summary.Metrics.HR.WeightedMeanBPM).To(HaveValue(Equal(150.0)))
		})
		It("does not require a spool for the summary-only parser", func() {
			GinkgoT().Setenv("TMPDIR", filepath.Join(directory, "missing-directory"))
			result, err := sensor.ParseAndProcess(strings.NewReader(content(`{"k":"end","t":1000}`)), sensor.ParseOptions{CalculationVersion: 2})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Quality.ValidHR).To(BeTrue())
		})
		It("removes the spool when parsing exits without a footer", func() {
			result, err := sensor.ParseAndProcessWithTimeline(strings.NewReader(content("")), sensor.ParseOptions{CalculationVersion: 2}, source)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Summary.Quality.IsComplete).To(BeFalse())
			Expect(result.Timeline).To(BeNil())
		})
		It("removes the spool after a successful build", func() {
			result, err := sensor.ParseAndProcessWithTimeline(strings.NewReader(content(`{"k":"end","t":1000}`)), sensor.ParseOptions{CalculationVersion: 2}, source)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.TimelineError).NotTo(HaveOccurred())
			Expect(result.Timeline).NotTo(BeNil())
		})
	})
})
