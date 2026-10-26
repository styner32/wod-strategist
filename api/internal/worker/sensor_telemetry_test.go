package worker_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	gcs "cloud.google.com/go/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"strings"
)

type mockSensorStorageClient struct {
	content    string
	generation int64
	readErr    error
}

func (m *mockSensorStorageClient) DownloadFile(ctx context.Context, gcsURI, destPath string) error {
	return nil
}
func (m *mockSensorStorageClient) UploadFromFile(ctx context.Context, localPath, objectName string) (string, error) {
	return "", nil
}
func (m *mockSensorStorageClient) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	return nil, nil
}
func (m *mockSensorStorageClient) ObjectAttrs(ctx context.Context, objectName string) (*gcs.ObjectAttrs, error) {
	return &gcs.ObjectAttrs{
		Size:        int64(len(m.content)),
		ContentType: "application/x-ndjson",
		Generation:  m.generation,
	}, nil
}
func (m *mockSensorStorageClient) NewReaderWithGeneration(ctx context.Context, objectName string, gen int64) (io.ReadCloser, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	return io.NopCloser(stringsReader(m.content)), nil
}

func stringsReader(s string) io.Reader {
	return bytes.NewReader([]byte(s))
}

var _ = Describe("SensorTelemetry", func() {
	var (
		dbConn      *gorm.DB
		w           *worker.Worker
		mockStorage *mockSensorStorageClient
		profile     db.Profile
		analysis    db.AnalysisResult
	)

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		birthYear := 1990
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{
			BirthYear: &birthYear,
		})

		mockStorage = &mockSensorStorageClient{generation: 100}
		w = worker.NewWorker(dbConn, mockStorage, "test-bucket", nil, nil, zap.NewNop())
	})

	It("processes valid sensor telemetry, computes HR bonus, and transitions state to COMPLETED", func() {
		sessionID := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		reqUUID := "c0000000-0000-0000-0000-000000000001"

		ndjsonContent := fmt.Sprintf(`{"k":"meta","schema_version":"2.0.0","workout_session_id":"%s","profile_id":%d,"clock_source":"capture_clock","base_epoch_ms":1700000000000}
{"k":"hr","t":1000,"bpm":160}
{"k":"hr","t":3000,"bpm":160}
{"k":"hr","t":5000,"bpm":160}
{"k":"end","t":6000,"pause_intervals":[],"device":{"battery_percent_end":90},"summary":{"hr_samples":3,"acc_samples":0,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}
`, sessionID, profile.ID)

		mockStorage.content = ndjsonContent
		h := sha256.Sum256([]byte(ndjsonContent))
		shaStr := hex.EncodeToString(h[:])

		age := 36
		maxHR := 184
		procData := map[string]any{
			"schema_version":    1,
			"request_id":        reqUUID,
			"object_name":       fmt.Sprintf("videos/%d/%s/sensor_telemetry_v1_%s.ndjson", profile.ID, sessionID, reqUUID),
			"target_generation": "100",
			"size_bytes":        int64(len(ndjsonContent)),
			"sha256":            shaStr,
			"attempts":          0,
			"calculation_inputs": map[string]any{
				"calculation_version": 1,
				"age":                 age,
				"max_hr":              maxHR,
				"has_hr_zones":        true,
			},
		}
		procJSON, _ := json.Marshal(procData)

		now := time.Now().UTC()
		analysis = testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:           sessionID,
			ProfileID:           profile.ID,
			Status:              "COMPLETED",
			SensorState:         db.SensorStatePending,
			SensorVersion:       1,
			SensorProcessing:    db.JSONDocument(procJSON),
			SensorNextAttemptAt: &now,
		})

		task, err := worker.NewSensorTelemetryTask(analysis.ID, profile.ID, reqUUID, 1)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleSensorTelemetryTask(context.Background(), task)
		Expect(err).NotTo(HaveOccurred())

		// Verify row in DB is COMPLETED
		var updated db.AnalysisResult
		Expect(dbConn.First(&updated, analysis.ID).Error).NotTo(HaveOccurred())
		Expect(updated.SensorState).To(Equal(db.SensorStateCompleted))
		Expect(updated.SensorNextAttemptAt).To(BeNil())

		// Verify summary contains HR bonus
		var summary map[string]any
		Expect(json.Unmarshal([]byte(updated.SensorSummary), &summary)).To(Succeed())
		Expect(summary["version"]).To(BeNumerically("==", 1))
		Expect(summary["request_id"]).To(Equal(reqUUID))
		Expect(summary["hr_bonus"]).To(BeNumerically(">", 0))
	})

	It("rejects stale worker updates if lease expired and another worker superseded", func() {
		sessionID := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		reqUUID := "c0000000-0000-0000-0000-000000000002"

		procData := map[string]any{
			"schema_version":    1,
			"request_id":        reqUUID,
			"object_name":       "some/path.ndjson",
			"target_generation": "100",
			"attempts":          1,
		}
		procJSON, _ := json.Marshal(procData)

		// Set state already COMPLETED with higher version
		analysis = testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:        sessionID,
			ProfileID:        profile.ID,
			Status:           "COMPLETED",
			SensorState:      db.SensorStateCompleted,
			SensorVersion:    2,
			SensorProcessing: db.JSONDocument(procJSON),
		})

		// Worker receives old task for version 1
		task, err := worker.NewSensorTelemetryTask(analysis.ID, profile.ID, reqUUID, 1)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleSensorTelemetryTask(context.Background(), task)
		Expect(err).NotTo(HaveOccurred())

		// Verify row was NOT modified
		var current db.AnalysisResult
		Expect(dbConn.First(&current, analysis.ID).Error).NotTo(HaveOccurred())
		Expect(current.SensorVersion).To(Equal(int64(2)))
		Expect(current.SensorState).To(Equal(db.SensorStateCompleted))
	})

	It("transitions to FAILED when max attempts (5) are reached", func() {
		sessionID := "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
		reqUUID := "c0000000-0000-0000-0000-000000000003"

		procData := map[string]any{
			"schema_version":    1,
			"request_id":        reqUUID,
			"object_name":       "some/path.ndjson",
			"target_generation": "100",
			"attempts":          5, // Already reached 5 attempts
		}
		procJSON, _ := json.Marshal(procData)

		now := time.Now().UTC()
		analysis = testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
			SessionID:           sessionID,
			ProfileID:           profile.ID,
			Status:              "COMPLETED",
			SensorState:         db.SensorStatePending,
			SensorVersion:       1,
			SensorProcessing:    db.JSONDocument(procJSON),
			SensorNextAttemptAt: &now,
		})

		task, err := worker.NewSensorTelemetryTask(analysis.ID, profile.ID, reqUUID, 1)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleSensorTelemetryTask(context.Background(), task)
		Expect(err).NotTo(HaveOccurred())

		var current db.AnalysisResult
		Expect(dbConn.First(&current, analysis.ID).Error).NotTo(HaveOccurred())
		Expect(current.SensorState).To(Equal(db.SensorStateFailed))
	})

	It("computes v3 HRR+ENMO hybrid intensity and promotes profile to personal_chest_v1 after 3 valid completed sessions without mutating past summaries", func() {
		buildV3NDJSON := func(sessionID string) string {
			const totalSec = 125
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf(`{"k":"meta","t":0,"schema_version":"2.0.0","workout_session_id":"%s","profile_id":%d,"clock_source":"capture_clock","base_epoch_ms":1700000000000}`+"\n", sessionID, profile.ID))
			sb.WriteString(`{"k":"stream_start","t":0,"stream_id":1,"sampling":{"acc_hz":25,"acc_range_g":8,"acc_resolution_bits":16}}` + "\n")
			accSamples := strings.Repeat("[0,0,1.25],", 24) + "[0,0,1.25]"
			for sec := 0; sec <= totalSec; sec++ {
				tMs := sec * 1000
				sb.WriteString(fmt.Sprintf(`{"k":"hr","t":%d,"bpm":150,"contact":true}`+"\n", tMs))
				if sec < totalSec {
					// 1.25g -> ENMO = 0.25g across 25Hz samples
					sb.WriteString(fmt.Sprintf(`{"k":"acc","t":%d,"stream_id":1,"dt":40,"v":[%s]}`+"\n", tMs, accSamples))
				}
			}
			sb.WriteString(fmt.Sprintf(`{"k":"end","t":%d,"pause_intervals":[],"device":{"battery_percent_end":88},"summary":{"hr_samples":%d,"acc_samples":%d,"dropped_packets":null,"gaps":0,"write_failures":0,"dropped_lines":0}}`+"\n", totalSec*1000, totalSec+1, totalSec*25))
			return sb.String()
		}

		var firstRowID uint
		for idx := 1; idx <= 3; idx++ {
			sid := fmt.Sprintf("WOD-20261025-01HYBRIDV3000%d", idx)
			reqID := fmt.Sprintf("d0000000-0000-0000-0000-00000000000%d", idx)
			ndjson := buildV3NDJSON(sid)
			mockStorage.content = ndjson
			sumHash := sha256.Sum256([]byte(ndjson))

			procData := map[string]any{
				"schema_version":    1,
				"request_id":        reqID,
				"object_name":       fmt.Sprintf("videos/%d/%s/sensor_telemetry_v1_%s.ndjson", profile.ID, sid, reqID),
				"target_generation": "100",
				"size_bytes":        int64(len(ndjson)),
				"sha256":            hex.EncodeToString(sumHash[:]),
				"attempts":          0,
				"calculation_inputs": map[string]any{
					"calculation_version": 3,
					"age":                 36,
					"max_hr":              180,
					"max_hr_source":       "estimated_220_minus_age",
					"has_hr_zones":        true,
					"resting_hr":          60,
					"enmo_rest_g":         0.01,
					"enmo_ref_g":          0.31,
					"calibration_version": "bootstrap_chest_v1",
				},
			}
			procJSON, _ := json.Marshal(procData)
			now := time.Now().UTC()
			row := testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:           sid,
				ProfileID:           profile.ID,
				Status:              "COMPLETED",
				SensorState:         db.SensorStatePending,
				SensorVersion:       1,
				SensorProcessing:    db.JSONDocument(procJSON),
				SensorNextAttemptAt: &now,
			})
			if idx == 1 {
				firstRowID = row.ID
			}

			task, err := worker.NewSensorTelemetryTask(row.ID, profile.ID, reqID, 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(w.HandleSensorTelemetryTask(context.Background(), task)).To(Succeed())

			var updatedRow db.AnalysisResult
			Expect(dbConn.First(&updatedRow, row.ID).Error).NotTo(HaveOccurred())
			Expect(updatedRow.SensorState).To(Equal(db.SensorStateCompleted), string(updatedRow.SensorProcessing))

			var refreshedProfile db.Profile
			Expect(dbConn.First(&refreshedProfile, profile.ID).Error).NotTo(HaveOccurred())
			if idx < 3 {
				Expect(refreshedProfile.SensorCalibrationVersion).To(BeNil())
			} else {
				Expect(refreshedProfile.SensorCalibrationVersion).To(HaveValue(Equal(sensor.PersonalCalibrationVersion)))
				Expect(refreshedProfile.SensorENMORestG).NotTo(BeNil())
				Expect(refreshedProfile.SensorENMORefG).NotTo(BeNil())
				Expect(refreshedProfile.SensorCalibrationSessions).To(Equal(3))
			}
		}

		// Past session 1 must retain its frozen bootstrap_chest_v1 summary
		var firstRow db.AnalysisResult
		Expect(dbConn.First(&firstRow, firstRowID).Error).NotTo(HaveOccurred())
		var firstSum sensor.SensorSummaryResult
		Expect(json.Unmarshal(firstRow.SensorSummary, &firstSum)).To(Succeed())
		Expect(firstSum.CalculationVersion).To(Equal(3))
		Expect(firstSum.Metrics.Hybrid).NotTo(BeNil())
		Expect(firstSum.Metrics.Hybrid.CalibrationVersion).To(Equal("bootstrap_chest_v1"))
		Expect(firstSum.Metrics.Hybrid.HybridIntensity).To(HaveValue(BeNumerically("~", 76.25, 0.05)))
	})
})
