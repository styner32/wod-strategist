package sensor_test

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/sensor"
	"os"
	"strings"
)

var _ = Describe("Heart rate quality version 2", func() {
	It("matches the same fixtures as the mobile filter", func() {
		data, err := os.ReadFile("../../../shared/__fixtures__/heart-rate-quality.json")
		Expect(err).NotTo(HaveOccurred())
		var fixtures []struct {
			Name     string
			Readings []struct {
				T       float64
				BPM     int
				Contact *bool
				Reset   bool
				Reason  string
			}
		}
		Expect(json.Unmarshal(data, &fixtures)).To(Succeed())
		for _, f := range fixtures {
			q := sensor.HRQuality{}
			for _, e := range f.Readings {
				if e.Reset {
					q.Reset(true)
				}
				Expect(q.Evaluate(sensor.HREvent{Time: e.T, BPM: e.BPM, Contact: e.Contact})).To(Equal(e.Reason), f.Name)
			}
		}
	})
	parse := func(events, end string, version int, maxHR *int) *sensor.SensorSummaryResult {
		content := `{"k":"meta","schema_version":"2.0.0","workout_session_id":"WOD-test","profile_id":1,"clock_source":"capture_clock","base_epoch_ms":1000}` + "\n" + events + "\n" + end + "\n"
		result, err := sensor.ParseAndProcess(strings.NewReader(content), sensor.ParseOptions{CalculationVersion: version, EstimatedMaxHR: maxHR})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Quality.IsComplete).To(BeTrue(), "%v", result.Quality.Errors)
		return result
	}
	It("excludes contact loss and recovery without filling gaps or changing version 1", func() {
		events := `{"k":"hr","t":0,"bpm":150,"contact":true}
{"k":"hr","t":1000,"bpm":150,"contact":true}
{"k":"hr","t":2000,"bpm":150,"contact":true}
{"k":"hr","t":3000,"bpm":45,"contact":false}
{"k":"hr","t":4000,"bpm":150,"contact":true}
{"k":"hr","t":5000,"bpm":150,"contact":true}
{"k":"hr","t":6000,"bpm":150,"contact":true}
{"k":"hr","t":7000,"bpm":150,"contact":true}
{"k":"hr","t":8000,"bpm":150,"contact":true}
{"k":"hr","t":9000,"bpm":150,"contact":true}
{"k":"hr","t":10000,"bpm":150,"contact":true}`
		max := 200
		res := parse(events, `{"k":"end","t":11000}`, 2, &max)
		hr := res.Metrics.HR
		Expect(*hr.WeightedMeanBPM).To(Equal(150.0))
		Expect(*hr.MinBPM).To(Equal(150))
		Expect(*hr.PeakBPM).To(Equal(150))
		Expect(hr.ValidSeconds).To(Equal(4.0))
		Expect(hr.ExcludedSeconds).To(Equal(6.0))
		Expect(hr.UnknownSeconds).To(Equal(1.0))
		Expect(hr.ExcludedByReason).To(Equal(map[string]float64{"contact_loss": 1, "recovering": 5}))
		Expect(hr.Zones.Zone3Ratio).To(Equal(1.0))
		Expect(hr.ValidHR).To(BeFalse())
		Expect(res.HRBonus).To(BeZero())
		old := parse(events, `{"k":"end","t":11000}`, 1, &max)
		Expect(*old.Metrics.HR.MinBPM).To(Equal(45))
		Expect(old.CalculationVersion).To(Equal(1))
	})
	It("includes stable low BPM, omits unknown zones and preserves the fifty percent boundary", func() {
		events := `{"k":"hr","t":0,"bpm":45}
{"k":"hr","t":1000,"bpm":45}
{"k":"hr","t":2000,"bpm":45}`
		res := parse(events, `{"k":"end","t":4000}`, 2, nil)
		Expect(res.Metrics.HR.ValidHR).To(BeTrue())
		Expect(res.Metrics.HR.Coverage).To(Equal(0.5))
		Expect(res.Metrics.HR.LowBPMSeconds).To(Equal(2.0))
		Expect(res.Metrics.HR.Zones).To(BeNil())
		Expect(res.Metrics.HR.ContactCoverage).To(HaveValue(BeZero()))
		Expect(parse(events, `{"k":"end","t":4001}`, 2, nil).Metrics.HR.ValidHR).To(BeFalse())
	})
	It("partitions active duration with pauses and leaves all-bad numeric statistics absent", func() {
		res := parse(`{"k":"hr","t":0,"bpm":45,"contact":false}
{"k":"hr","t":1000,"bpm":45,"contact":false}
{"k":"hr","t":2000,"bpm":45,"contact":false}
{"k":"hr","t":8000,"bpm":45,"contact":false}
{"k":"hr","t":9000,"bpm":45,"contact":false}`, `{"k":"end","t":10000,"pause_intervals":[{"start_offset_ms":2000,"end_offset_ms":8000}]}`, 2, nil)
		hr := res.Metrics.HR
		Expect(hr.ValidSeconds + hr.ExcludedSeconds + hr.UnknownSeconds).To(Equal(4.0))
		Expect(hr.ValidSeconds).To(BeZero())
		Expect(hr.ExcludedSeconds).To(Equal(3.0))
		Expect(hr.UnknownSeconds).To(Equal(1.0))
		Expect(hr.WeightedMeanBPM).To(BeNil())
		Expect(hr.MinBPM).To(BeNil())
		Expect(hr.PeakBPM).To(BeNil())
	})
	It("does not include sudden drops, malformed observations, or missing intervals in aggregates", func() {
		for _, bad := range []string{`{"k":"hr","t":3000,"bpm":45}`, `{"k":"hr","t":3000,"bpm":"bad"}`} {
			res := parse(`{"k":"hr","t":0,"bpm":150}
{"k":"hr","t":1000,"bpm":150}
{"k":"hr","t":2000,"bpm":150}`+"\n"+bad+"\n"+`{"k":"hr","t":4000,"bpm":150}
{"k":"hr","t":9000,"bpm":150}`, `{"k":"end","t":10000}`, 2, nil)
			Expect(res.Metrics.HR.WeightedMeanBPM).To(HaveValue(Equal(150.0)))
			Expect(res.Metrics.HR.MinBPM).To(HaveValue(Equal(150)))
			Expect(res.Metrics.HR.ValidSeconds).To(Equal(3.0))
			Expect(res.Metrics.HR.ExcludedSeconds).To(Equal(1.0))
			Expect(res.Metrics.HR.UnknownSeconds).To(Equal(6.0))
		}
	})
	It("includes the final accepted peak without extending its duration", func() {
		res := parse(`{"k":"hr","t":0,"bpm":150}
{"k":"hr","t":1000,"bpm":180}`, `{"k":"end","t":3000}`, 2, nil)
		Expect(res.Metrics.HR.PeakBPM).To(HaveValue(Equal(180)))
		Expect(res.Metrics.HR.WeightedMeanBPM).To(HaveValue(Equal(150.0)))
		Expect(res.Metrics.HR.UnknownSeconds).To(Equal(2.0))
	})

})
