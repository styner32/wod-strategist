package sensor_test

import (
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/sensor"
)

var _ = Describe("TimelineMetrics", func() {
	var (
		source sensor.TimelineSource
	)

	BeforeEach(func() {
		source = sensor.TimelineSource{
			SensorVersion:        "1",
			RequestID:            "req-1",
			SourceGeneration:     "gen-100",
			HRCalculationVersion: 2,
		}
	})

	Context("acc_magnitude_std_g calculation", func() {
		It("yields 0.0 for constant stationary signal", func() {
			tb := sensor.NewTimelineBuilder()
			tb.HandleStreamStart(sensor.StreamStartEvent{
				StreamID: 1,
				Sampling: struct {
					ACCHz             float64 `json:"acc_hz"`
					ACCRangeG         int     `json:"acc_range_g"`
					ACCResolutionBits int     `json:"acc_resolution_bits"`
					FrameType         int     `json:"frame_type"`
					DeltaCompressed   bool    `json:"delta_compressed"`
				}{
					ACCHz:     50.0,
					ACCRangeG: 8,
				},
			})

			// 50 samples at 20ms intervals spanning [0, 1000ms) with constant (0, 0, 1) -> m = 1.0
			v := make([][]float64, 50)
			for i := range v {
				v[i] = []float64{0.0, 0.0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     0,
				Dt:       20.0,
				V:        v,
			})

			tl := tb.Build(source, 1000.0, nil, nil)
			Expect(tl.Points).To(HaveLen(1))
			Expect(tl.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
			Expect(tl.Points[0].AccMagnitudeStdG.Value).NotTo(BeNil())
			Expect(*tl.Points[0].AccMagnitudeStdG.Value).To(Equal(0.0))
		})

		It("matches expected population standard deviation for varying magnitude", func() {
			tb := sensor.NewTimelineBuilder()
			tb.HandleStreamStart(sensor.StreamStartEvent{
				StreamID: 1,
				Sampling: struct {
					ACCHz             float64 `json:"acc_hz"`
					ACCRangeG         int     `json:"acc_range_g"`
					ACCResolutionBits int     `json:"acc_resolution_bits"`
					FrameType         int     `json:"frame_type"`
					DeltaCompressed   bool    `json:"delta_compressed"`
				}{
					ACCHz:     3.0,
					ACCRangeG: 8,
				},
			})

			// Samples: m = 0.8, 1.0, 1.2
			// Mean = 1.0
			// Variance = ((0.8-1)^2 + (1-1)^2 + (1.2-1)^2) / 3 = 0.08 / 3 ≈ 0.0266667
			// StdDev = sqrt(0.0266667) ≈ 0.163299
			v := [][]float64{
				{0.8, 0, 0},
				{0, 1.0, 0},
				{0, 0, 1.2},
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     50, // first sample at 50ms, dt ~ 300ms
				Dt:       300.0,
				V:        v,
			})

			tl := tb.Build(source, 1000.0, nil, nil)
			Expect(tl.Points).To(HaveLen(1))
			Expect(tl.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
			Expect(tl.Points[0].AccMagnitudeStdG.Value).NotTo(BeNil())
			Expect(math.Abs(*tl.Points[0].AccMagnitudeStdG.Value - 0.1633)).To(BeNumerically("<=", 0.001))
		})

		It("is invariant to axis swap / orientation rotation", func() {
			buildWithSamples := func(v [][]float64) *sensor.SensorTimelineData {
				tb := sensor.NewTimelineBuilder()
				tb.HandleStreamStart(sensor.StreamStartEvent{
					StreamID: 1,
					Sampling: struct {
						ACCHz             float64 `json:"acc_hz"`
						ACCRangeG         int     `json:"acc_range_g"`
						ACCResolutionBits int     `json:"acc_resolution_bits"`
						FrameType         int     `json:"frame_type"`
						DeltaCompressed   bool    `json:"delta_compressed"`
					}{
						ACCHz:     50.0,
						ACCRangeG: 8,
					},
				})
				tb.HandleAcc(sensor.AccEvent{
					StreamID: 1,
					Time:     10,
					Dt:       20.0,
					V:        v,
				})
				return tb.Build(source, 1000.0, nil, nil)
			}

			// Generate 48 samples with different coordinate orientations but identical magnitudes
			vX := make([][]float64, 48)
			vY := make([][]float64, 48)
			for i := 0; i < 48; i++ {
				mag := 1.0 + float64(i%5)*0.2
				vX[i] = []float64{mag, 0, 0}
				vY[i] = []float64{0, mag, 0}
			}

			tlX := buildWithSamples(vX)
			tlY := buildWithSamples(vY)

			Expect(tlX.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
			Expect(tlY.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
			Expect(*tlX.Points[0].AccMagnitudeStdG.Value).To(Equal(*tlY.Points[0].AccMagnitudeStdG.Value))
		})

		It("aggregates multiple packets in the same second and splits packet spanning boundary", func() {
			tb := sensor.NewTimelineBuilder()
			tb.HandleStreamStart(sensor.StreamStartEvent{
				StreamID: 1,
				Sampling: struct {
					ACCHz             float64 `json:"acc_hz"`
					ACCRangeG         int     `json:"acc_range_g"`
					ACCResolutionBits int     `json:"acc_resolution_bits"`
					FrameType         int     `json:"frame_type"`
					DeltaCompressed   bool    `json:"delta_compressed"`
				}{
					ACCHz:     50.0,
					ACCRangeG: 8,
				},
			})

			// Packet 1: 25 samples from 0ms to 480ms
			v1 := make([][]float64, 25)
			for i := range v1 {
				v1[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     10,
				Dt:       20.0,
				V:        v1,
			})

			// Packet 2: 50 samples from 510ms to 1490ms (crosses 1000ms boundary!)
			v2 := make([][]float64, 50)
			for i := range v2 {
				v2[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     510,
				Dt:       20.0,
				V:        v2,
			})

			// Packet 3: 25 samples in second 2 [1000, 2000) from 1510ms to 1990ms
			v3 := make([][]float64, 25)
			for i := range v3 {
				v3[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     1510,
				Dt:       20.0,
				V:        v3,
			})

			tl := tb.Build(source, 2000.0, nil, nil)
			Expect(tl.Points).To(HaveLen(2))
			// Both 1-second buckets should be valid
			Expect(tl.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
			Expect(tl.Points[1].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
		})
	})

	Context("Quality and error conditions", func() {
		It("marks bucket insufficient when sample count is less than 80%", func() {
			tb := sensor.NewTimelineBuilder()
			tb.HandleStreamStart(sensor.StreamStartEvent{
				StreamID: 1,
				Sampling: struct {
					ACCHz             float64 `json:"acc_hz"`
					ACCRangeG         int     `json:"acc_range_g"`
					ACCResolutionBits int     `json:"acc_resolution_bits"`
					FrameType         int     `json:"frame_type"`
					DeltaCompressed   bool    `json:"delta_compressed"`
				}{
					ACCHz:     50.0,
					ACCRangeG: 8,
				},
			})

			// Expected 50 samples, provide only 30 (60% < 80%)
			v := make([][]float64, 30)
			for i := range v {
				v[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     10,
				Dt:       20.0,
				V:        v,
			})

			tl := tb.Build(source, 1000.0, nil, nil)
			Expect(tl.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusInsufficient))
			Expect(tl.Points[0].AccMagnitudeStdG.Value).To(BeNil())
		})

		It("marks bucket insufficient when internal gap exceeds 3 * nominal_dt", func() {
			tb := sensor.NewTimelineBuilder()
			tb.HandleStreamStart(sensor.StreamStartEvent{
				StreamID: 1,
				Sampling: struct {
					ACCHz             float64 `json:"acc_hz"`
					ACCRangeG         int     `json:"acc_range_g"`
					ACCResolutionBits int     `json:"acc_resolution_bits"`
					FrameType         int     `json:"frame_type"`
					DeltaCompressed   bool    `json:"delta_compressed"`
				}{
					ACCHz:     50.0, // nominal_dt = 20ms, max allowed gap = 60ms
					ACCRangeG: 8,
				},
			})

			// 20 samples from 10ms to 390ms
			v1 := make([][]float64, 20)
			for i := range v1 {
				v1[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     10,
				Dt:       20.0,
				V:        v1,
			})

			// Gap of 80ms (> 60ms) between 390ms and 470ms!
			v2 := make([][]float64, 24)
			for i := range v2 {
				v2[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     470,
				Dt:       20.0,
				V:        v2,
			})

			tl := tb.Build(source, 1000.0, nil, nil)
			Expect(tl.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusInsufficient))
			Expect(tl.Points[0].AccMagnitudeStdG.Reason).To(Equal("gap_too_large"))
		})

		It("marks bucket invalid on sensor range saturation", func() {
			tb := sensor.NewTimelineBuilder()
			tb.HandleStreamStart(sensor.StreamStartEvent{
				StreamID: 1,
				Sampling: struct {
					ACCHz             float64 `json:"acc_hz"`
					ACCRangeG         int     `json:"acc_range_g"`
					ACCResolutionBits int     `json:"acc_resolution_bits"`
					FrameType         int     `json:"frame_type"`
					DeltaCompressed   bool    `json:"delta_compressed"`
				}{
					ACCHz:     50.0,
					ACCRangeG: 8, // max 8g
				},
			})

			v := make([][]float64, 48)
			for i := range v {
				v[i] = []float64{0, 0, 1.0}
			}
			v[20] = []float64{8.5, 0, 0} // Saturated sample!
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     10,
				Dt:       20.0,
				V:        v,
			})

			tl := tb.Build(source, 1000.0, nil, nil)
			Expect(tl.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusInvalid))
			Expect(tl.Points[0].AccMagnitudeStdG.Reason).To(Equal("saturation"))
		})

		It("marks bucket invalid on timestamp reversal", func() {
			tb := sensor.NewTimelineBuilder()
			tb.HandleStreamStart(sensor.StreamStartEvent{
				StreamID: 1,
				Sampling: struct {
					ACCHz             float64 `json:"acc_hz"`
					ACCRangeG         int     `json:"acc_range_g"`
					ACCResolutionBits int     `json:"acc_resolution_bits"`
					FrameType         int     `json:"frame_type"`
					DeltaCompressed   bool    `json:"delta_compressed"`
				}{
					ACCHz:     50.0,
					ACCRangeG: 8,
				},
			})

			v1 := make([][]float64, 25)
			for i := range v1 {
				v1[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     500,
				Dt:       20.0,
				V:        v1,
			})
			// Reverse time!
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     200,
				Dt:       20.0,
				V:        v1,
			})

			tl := tb.Build(source, 1000.0, nil, nil)
			Expect(tl.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusInvalid))
			Expect(tl.Points[0].AccMagnitudeStdG.Reason).To(Equal("timestamp_reversed"))
		})

		It("marks bucket paused when overlapping a pause interval", func() {
			tb := sensor.NewTimelineBuilder()
			tb.HandleStreamStart(sensor.StreamStartEvent{
				StreamID: 1,
				Sampling: struct {
					ACCHz             float64 `json:"acc_hz"`
					ACCRangeG         int     `json:"acc_range_g"`
					ACCResolutionBits int     `json:"acc_resolution_bits"`
					FrameType         int     `json:"frame_type"`
					DeltaCompressed   bool    `json:"delta_compressed"`
				}{
					ACCHz:     50.0,
					ACCRangeG: 8,
				},
			})

			v := make([][]float64, 50)
			for i := range v {
				v[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     10,
				Dt:       20.0,
				V:        v,
			})

			pauses := []sensor.PauseInterval{
				{StartOffsetMs: 400, EndOffsetMs: 700},
			}

			tl := tb.Build(source, 1000.0, pauses, nil)
			Expect(tl.Points[0].AccMagnitudeStdG.Status).To(Equal(sensor.StatusPaused))
			Expect(tl.Points[0].AccMagnitudeStdG.Value).To(BeNil())
			Expect(tl.Points[0].HeartRateBPM.Status).To(Equal(sensor.StatusPaused))
		})

		It("validates last partial bucket using actual duration", func() {
			tb := sensor.NewTimelineBuilder()
			tb.HandleStreamStart(sensor.StreamStartEvent{
				StreamID: 1,
				Sampling: struct {
					ACCHz             float64 `json:"acc_hz"`
					ACCRangeG         int     `json:"acc_range_g"`
					ACCResolutionBits int     `json:"acc_resolution_bits"`
					FrameType         int     `json:"frame_type"`
					DeltaCompressed   bool    `json:"delta_compressed"`
				}{
					ACCHz:     50.0,
					ACCRangeG: 8,
				},
			})

			// Total duration is 1500ms. Second bucket [1000, 1500) has 500ms duration.
			// Expected samples = (500/1000)*50 = 25 samples.
			// Provide 24 samples (96% of 25) -> should be valid!
			v1 := make([][]float64, 48)
			for i := range v1 {
				v1[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     10,
				Dt:       20.0,
				V:        v1,
			})

			v2 := make([][]float64, 24)
			for i := range v2 {
				v2[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{
				StreamID: 1,
				Time:     1010,
				Dt:       20.0,
				V:        v2,
			})

			tl := tb.Build(source, 1500.0, nil, nil)
			Expect(tl.Points).To(HaveLen(2))
			Expect(tl.Points[1].StartMs).To(Equal(int64(1000)))
			Expect(tl.Points[1].EndMs).To(Equal(int64(1500)))
			Expect(tl.Points[1].AccMagnitudeStdG.Status).To(Equal(sensor.StatusValid))
		})
	})

	Context("Heart Rate 1-second timeline calculation", func() {
		It("computes time-weighted mean BPM for valid intervals", func() {
			tb := sensor.NewTimelineBuilder()

			contact := true
			hrEvents := []sensor.HREvent{
				{Time: 0, BPM: 120, Contact: &contact},
				{Time: 500, BPM: 140, Contact: &contact},
				{Time: 1000, BPM: 160, Contact: &contact},
			}
			// Wrap in internal observations
			tl := tb.BuildWithHREvents(source, 1000.0, nil, hrEvents)
			Expect(tl.Points).To(HaveLen(1))
			Expect(tl.Points[0].HeartRateBPM.Status).To(Equal(sensor.StatusValid))
			Expect(tl.Points[0].HeartRateBPM.Value).NotTo(BeNil())
			// Interval [0, 500) has 120, interval [500, 1000) has 140
			// Weighted mean: (120*500 + 140*500)/1000 = 130
			Expect(*tl.Points[0].HeartRateBPM.Value).To(Equal(130.0))
		})

		It("marks HR insufficient when valid duration is less than 80%", func() {
			tb := sensor.NewTimelineBuilder()

			contact := true
			hrEvents := []sensor.HREvent{
				{Time: 0, BPM: 120, Contact: &contact},
				{Time: 600, BPM: 140, Contact: &contact}, // interval is [0, 600) -> 600ms = 60% < 80%
				// Next event at 6000ms (> 5000ms dropout)
				{Time: 6000, BPM: 150, Contact: &contact},
			}

			tl := tb.BuildWithHREvents(source, 1000.0, nil, hrEvents)
			Expect(tl.Points).To(HaveLen(1))
			Expect(tl.Points[0].HeartRateBPM.Status).To(Equal(sensor.StatusInsufficient))
			Expect(tl.Points[0].HeartRateBPM.Value).To(BeNil())
		})
	})

	Context("Gaps consolidation", func() {
		It("consolidates long gaps of >= 5 seconds into gaps array", func() {
			tb := sensor.NewTimelineBuilder()
			tb.HandleStreamStart(sensor.StreamStartEvent{
				StreamID: 1,
				Sampling: struct {
					ACCHz             float64 `json:"acc_hz"`
					ACCRangeG         int     `json:"acc_range_g"`
					ACCResolutionBits int     `json:"acc_resolution_bits"`
					FrameType         int     `json:"frame_type"`
					DeltaCompressed   bool    `json:"delta_compressed"`
				}{
					ACCHz:     50.0,
					ACCRangeG: 8,
				},
			})

			// 10-second workout: data at [0, 2s) and [8, 10s). Gap between 2s and 8s (6s long).
			v := make([][]float64, 48)
			for i := range v {
				v[i] = []float64{0, 0, 1.0}
			}
			tb.HandleAcc(sensor.AccEvent{StreamID: 1, Time: 10, Dt: 20.0, V: v})
			tb.HandleAcc(sensor.AccEvent{StreamID: 1, Time: 1010, Dt: 20.0, V: v})
			tb.HandleAcc(sensor.AccEvent{StreamID: 1, Time: 8010, Dt: 20.0, V: v})
			tb.HandleAcc(sensor.AccEvent{StreamID: 1, Time: 9010, Dt: 20.0, V: v})

			tl := tb.Build(source, 10000.0, nil, nil)
			Expect(tl.Gaps).To(HaveLen(1))
			Expect(tl.Gaps[0].StartMs).To(Equal(int64(2000)))
			Expect(tl.Gaps[0].EndMs).To(Equal(int64(8000)))
			Expect(tl.Gaps[0].Channel).To(Equal("both"))

			// The points array should only have 4 points (0, 1, 8, 9)
			Expect(tl.Points).To(HaveLen(4))
			Expect(tl.Points[0].StartMs).To(Equal(int64(0)))
			Expect(tl.Points[1].StartMs).To(Equal(int64(1000)))
			Expect(tl.Points[2].StartMs).To(Equal(int64(8000)))
			Expect(tl.Points[3].StartMs).To(Equal(int64(9000)))
		})
	})
})
