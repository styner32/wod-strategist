package worker_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var _ = Describe("Sensor quality worker", func() {
	var conn *gorm.DB
	BeforeAll(func() { var err error; conn, err = testhelpers.InitDB(); Expect(err).NotTo(HaveOccurred()) })
	AfterAll(func() {
		if conn != nil {
			sql, err := conn.DB()
			Expect(err).NotTo(HaveOccurred())
			Expect(sql.Close()).To(Succeed())
		}
	})
	BeforeEach(func() { testhelpers.CleanupDB(conn) })
	It("persists the pinned summary and isolates optional timeline failures", func() {
		profile := testhelpers.CreateProfile(conn, &db.Profile{})
		for i, tc := range []struct {
			version        int
			endMs          string
			timelineFailed bool
		}{
			{version: 1, endMs: "5000"},
			{version: 2, endMs: "5000"},
			{version: 2, endMs: "1e25", timelineFailed: true},
		} {
			version := tc.version
			sid := fmt.Sprintf("WOD-20260908-quality-%d-%d", version, i)
			content := fmt.Sprintf(`{"k":"meta","schema_version":"2.0.0","workout_session_id":"%s","profile_id":%d,"clock_source":"capture_clock","base_epoch_ms":1000}
{"k":"hr","t":0,"bpm":150,"contact":true}
{"k":"hr","t":1000,"bpm":150,"contact":true}
{"k":"hr","t":2000,"bpm":150,"contact":true}
{"k":"hr","t":3000,"bpm":45,"contact":false}
{"k":"hr","t":4000,"bpm":150,"contact":true}
{"k":"end","t":%s}
`, sid, profile.ID, tc.endMs)
			object := fmt.Sprintf("videos/%d/%s/sensor_telemetry_v1_r.ndjson", profile.ID, sid)
			digest := sha256.Sum256([]byte(content))
			proc := worker.SensorProcessingRecord{RequestID: "r", ObjectName: object, TargetGeneration: "100", SizeBytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
			proc.CalculationInputs.CalculationVersion = version
			raw, err := json.Marshal(proc)
			Expect(err).NotTo(HaveOccurred())
			row := testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{SessionID: sid, ProfileID: profile.ID, Status: "PENDING", SensorState: db.SensorStatePending, SensorVersion: 1, SensorProcessing: db.JSONDocument(raw)})
			transport := testhelpers.NewMockTransport()
			testhelpers.MockGCSDownloadWithBody(transport, "gs://test-bucket/"+object, []byte(content))
			storage, err := testhelpers.NewStorageClient("test-bucket", transport)
			Expect(err).NotTo(HaveOccurred())
			w := worker.NewWorker(conn, storage, "test-bucket", nil, nil, zap.NewNop())
			task, err := worker.NewSensorTelemetryTask(row.ID, profile.ID, "r", 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(w.HandleSensorTelemetryTask(context.Background(), task)).To(Succeed())
			var updated db.AnalysisResult
			Expect(conn.First(&updated, row.ID).Error).To(Succeed())
			Expect(updated.SensorState).To(Equal(db.SensorStateCompleted))
			var summary sensor.SensorSummaryResult
			Expect(json.Unmarshal(updated.SensorSummary, &summary)).To(Succeed())
			Expect(summary.CalculationVersion).To(Equal(version))
			Expect(summary.RequestID).To(Equal("r"))
			Expect(summary.SourceGeneration).To(Equal("100"))
			Expect(summary.Quality.IsComplete).To(BeTrue())
			if version == 2 {
				Expect(summary.Metrics.HR.MinBPM).To(HaveValue(Equal(150)))
				Expect(summary.Metrics.HR.ExcludedSeconds).To(Equal(1.0))
				if tc.timelineFailed {
					var failure struct {
						Status string `json:"status"`
						Error  string `json:"error"`
					}
					Expect(json.Unmarshal(updated.SensorTimeline, &failure)).To(Succeed())
					Expect(failure.Status).To(Equal("failed"))
					Expect(failure.Error).NotTo(BeEmpty())
				} else {
					var timeline sensor.SensorTimelineData
					Expect(json.Unmarshal(updated.SensorTimeline, &timeline)).To(Succeed())
					Expect(timeline.DurationMs).To(Equal(int64(5000)))
					Expect(timeline.Source).To(Equal(sensor.TimelineSource{
						SensorVersion: "1", RequestID: "r", SourceGeneration: "100", HRCalculationVersion: 2,
					}))
					Expect(timeline.Points).To(HaveLen(5))
					Expect(timeline.Points[0].HeartRateBPM.Value).To(HaveValue(Equal(150.0)))
					Expect(timeline.Points[3].HeartRateBPM.Value).To(BeNil())
					Expect(timeline.Points[3].HeartRateBPM.Status).To(Equal(sensor.StatusMissing))
				}
			} else {
				Expect(summary.Metrics.HR.MinBPM).To(HaveValue(Equal(45)))
				Expect(updated.SensorTimeline).To(BeEmpty())
			}
			Expect(transport.Verify()).To(Succeed())
			Expect(transport.Requests()).NotTo(BeEmpty())
			for _, request := range transport.Requests() {
				requestURL, err := url.Parse(request.URL)
				Expect(err).NotTo(HaveOccurred())
				Expect(requestURL.Query().Get("generation")).To(Equal("100"))
			}
		}
	})
}, Ordered)
