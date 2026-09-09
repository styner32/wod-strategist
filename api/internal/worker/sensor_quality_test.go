package worker_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"net/url"
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
	It("uses the pinned version with a real generation-bound storage reader", func() {
		profile := testhelpers.CreateProfile(conn, &db.Profile{})
		for _, version := range []int{1, 2} {
			sid := fmt.Sprintf("WOD-20260908-quality-%d", version)
			content := fmt.Sprintf(`{"k":"meta","schema_version":"2.0.0","workout_session_id":"%s","profile_id":%d,"clock_source":"capture_clock","base_epoch_ms":1000}
{"k":"hr","t":0,"bpm":150,"contact":true}
{"k":"hr","t":1000,"bpm":150,"contact":true}
{"k":"hr","t":2000,"bpm":150,"contact":true}
{"k":"hr","t":3000,"bpm":45,"contact":false}
{"k":"hr","t":4000,"bpm":150,"contact":true}
{"k":"end","t":5000}
`, sid, profile.ID)
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
			if version == 2 {
				Expect(summary.Metrics.HR.MinBPM).To(HaveValue(Equal(150)))
				Expect(summary.Metrics.HR.ExcludedSeconds).To(Equal(1.0))
			} else {
				Expect(summary.Metrics.HR.MinBPM).To(HaveValue(Equal(45)))
			}
			Expect(transport.Requests()).To(HaveLen(1))
			requestURL, err := url.Parse(transport.Requests()[0].URL)
			Expect(err).NotTo(HaveOccurred())
			Expect(requestURL.Query().Get("generation")).To(Equal("100"))
		}
	})
}, Ordered)
