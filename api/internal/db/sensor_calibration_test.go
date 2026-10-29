package db_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/sensor"
	"github.com/wod-strategist/api/internal/testhelpers"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var archiveCalibrationConnection struct {
	once sync.Once
	db   *gorm.DB
	err  error
}

func archiveCalibrationDB() *gorm.DB {
	archiveCalibrationConnection.once.Do(func() {
		archiveCalibrationConnection.db, archiveCalibrationConnection.err = testhelpers.InitDB()
	})
	Expect(archiveCalibrationConnection.err).NotTo(HaveOccurred())
	return archiveCalibrationConnection.db
}

func calibrationFloat(value float64) *float64 { return &value }
func calibrationString(value string) *string  { return &value }

func rawCalibrationSummary(low, high float64) sensor.SensorSummaryResult {
	return sensor.SensorSummaryResult{
		Version: 1, CalculationVersion: 3,
		Quality: sensor.QualityReport{IsComplete: true, Status: "ok"},
		CalculationInputs: sensor.CalculationInputs{
			CalculationVersion: 3, CalibrationVersion: sensor.PersonalCalibrationVersion,
			ENMORestG: calibrationFloat(0.025), ENMORefG: calibrationFloat(0.6),
		},
		Metrics: sensor.SensorMetrics{Hybrid: &sensor.HybridMetrics{
			CalibrationVersion: sensor.PersonalCalibrationVersion,
			ValidACCSeconds:    180, ENMOP05G: calibrationFloat(low), ENMOP95G: calibrationFloat(high),
			// These normalized values are deliberately unrelated to the raw percentiles.
			MotionIntensity: calibrationFloat(98), HybridIntensity: calibrationFloat(99),
		}},
	}
}

func seedCalibrationSource(conn *gorm.DB, profileID uint, name string, summary sensor.SensorSummaryResult) db.AnalysisResult {
	encoded, err := json.Marshal(summary)
	Expect(err).NotTo(HaveOccurred())
	return testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{
		SessionID: "WOD-20261029-" + name, ProfileID: profileID, Status: "COMPLETED",
		SensorVersion: summary.Version, SensorState: db.SensorStateCompleted, SensorSummary: db.JSONDocument(encoded),
	})
}

var _ = Describe("archive calibration", func() {
	var conn *gorm.DB
	var profile db.Profile
	BeforeEach(func() {
		conn = archiveCalibrationDB()
		testhelpers.CleanupDB(conn)
		profile = testhelpers.CreateProfile(conn, &db.Profile{Name: "Archive calibration"})
	})
	readProfile := func() db.Profile {
		var current db.Profile
		Expect(conn.First(&current, profile.ID).Error).To(Succeed())
		return current
	}
	reconcile := func(onlyIfUnset bool) {
		Expect(conn.Transaction(func(tx *gorm.DB) error {
			return db.RecomputeProfileENMOCalibrationTx(context.Background(), tx, profile.ID, onlyIfUnset)
		})).To(Succeed())
	}

	It("recomputes from remaining raw percentiles after archive and restore without rewriting another frozen summary", func() {
		a := seedCalibrationSource(conn, profile.ID, "CAL-A", rawCalibrationSummary(0.01, 0.2))
		b := seedCalibrationSource(conn, profile.ID, "CAL-B", rawCalibrationSummary(0.02, 0.5))
		seedCalibrationSource(conn, profile.ID, "CAL-C", rawCalibrationSummary(0.03, 0.7))
		seedCalibrationSource(conn, profile.ID, "CAL-D", rawCalibrationSummary(0.04, 0.9))
		reconcile(false)
		initial := readProfile()
		Expect(initial.SensorENMORestG).To(HaveValue(BeNumerically("~", 0.025, 0.00001)))
		Expect(initial.SensorENMORefG).To(HaveValue(BeNumerically("~", 0.6, 0.00001)))
		Expect(initial.SensorCalibrationSessions).To(Equal(4))
		Expect(conn.First(&b, b.ID).Error).To(Succeed())
		frozenB := string(b.SensorSummary)

		Expect(conn.Model(&db.AnalysisResult{}).Where("id = ?", a.ID).Update("archived_at", time.Now().UTC()).Error).To(Succeed())
		reconcile(false)
		remaining := readProfile()
		Expect(remaining.SensorCalibrationVersion).To(HaveValue(Equal(sensor.PersonalCalibrationVersion)))
		Expect(remaining.SensorENMORestG).To(HaveValue(BeNumerically("~", 0.03, 0.00001)))
		Expect(remaining.SensorENMORefG).To(HaveValue(BeNumerically("~", 0.7, 0.00001)))
		Expect(remaining.SensorCalibrationSessions).To(Equal(3))
		Expect(conn.First(&b, b.ID).Error).To(Succeed())
		Expect(string(b.SensorSummary)).To(Equal(frozenB))
		var frozen sensor.SensorSummaryResult
		Expect(json.Unmarshal(b.SensorSummary, &frozen)).To(Succeed())
		Expect(db.SensorCalibrationExclusionReason(frozen, remaining)).To(Equal("calibration_changed"))

		Expect(conn.Model(&db.AnalysisResult{}).Where("id = ?", a.ID).Update("archived_at", nil).Error).To(Succeed())
		reconcile(false)
		restored := readProfile()
		Expect(restored.SensorENMORestG).To(HaveValue(BeNumerically("~", 0.025, 0.00001)))
		Expect(restored.SensorENMORefG).To(HaveValue(BeNumerically("~", 0.6, 0.00001)))
		Expect(restored.SensorCalibrationSessions).To(Equal(4))
		Expect(db.SensorCalibrationExclusionReason(frozen, restored)).To(BeEmpty())
	})

	It("clears personal calibration below three eligible sources and restores it when the source returns", func() {
		a := seedCalibrationSource(conn, profile.ID, "THREE-A", rawCalibrationSummary(0.01, 0.3))
		seedCalibrationSource(conn, profile.ID, "THREE-B", rawCalibrationSummary(0.02, 0.4))
		seedCalibrationSource(conn, profile.ID, "THREE-C", rawCalibrationSummary(0.03, 0.5))
		reconcile(false)
		Expect(readProfile().SensorCalibrationSessions).To(Equal(3))
		Expect(conn.Model(&db.AnalysisResult{}).Where("id = ?", a.ID).Update("archived_at", time.Now().UTC()).Error).To(Succeed())
		reconcile(false)
		cleared := readProfile()
		Expect(cleared.SensorENMORestG).To(BeNil())
		Expect(cleared.SensorENMORefG).To(BeNil())
		Expect(cleared.SensorCalibrationVersion).To(BeNil())
		Expect(cleared.SensorCalibratedAt).To(BeNil())
		Expect(cleared.SensorCalibrationSessions).To(BeZero())
		Expect(conn.Model(&db.AnalysisResult{}).Where("id = ?", a.ID).Update("archived_at", nil).Error).To(Succeed())
		reconcile(false)
		Expect(readProfile().SensorCalibrationSessions).To(Equal(3))
		Expect(readProfile().SensorENMORefG).To(HaveValue(BeNumerically("~", 0.4, 0.00001)))
	})

	It("retains a pinned calibration during automatic promotion until explicit reconciliation", func() {
		Expect(conn.Table("profiles").Where("id = ?", profile.ID).Updates(map[string]any{
			"sensor_enmo_rest_g": 0.04, "sensor_enmo_ref_g": 0.9,
			"sensor_calibration_version": sensor.PersonalCalibrationVersion, "sensor_calibration_sessions": 5,
		}).Error).To(Succeed())
		for i := 0; i < 3; i++ {
			seedCalibrationSource(conn, profile.ID, fmt.Sprintf("PINNED-%d", i), rawCalibrationSummary(0.02, 0.4))
		}
		reconcile(true)
		Expect(readProfile().SensorENMORefG).To(HaveValue(Equal(0.9)))
		Expect(readProfile().SensorCalibrationSessions).To(Equal(5))
		reconcile(false)
		Expect(readProfile().SensorENMORestG).To(HaveValue(Equal(0.02)))
		Expect(readProfile().SensorENMORefG).To(HaveValue(Equal(0.4)))
		Expect(readProfile().SensorCalibrationSessions).To(Equal(3))
	})

	It("does not promote incomplete, old-version or short measurements into a personal baseline", func() {
		for i := 0; i < 2; i++ {
			seedCalibrationSource(conn, profile.ID, fmt.Sprintf("VALID-%d", i), rawCalibrationSummary(0.02, 0.4))
		}
		incomplete := rawCalibrationSummary(0.02, 0.4)
		incomplete.Quality.IsComplete = false
		seedCalibrationSource(conn, profile.ID, "INCOMPLETE", incomplete)
		old := rawCalibrationSummary(0.02, 0.4)
		old.CalculationVersion = 2
		seedCalibrationSource(conn, profile.ID, "OLD", old)
		short := rawCalibrationSummary(0.02, 0.4)
		short.Metrics.Hybrid.ValidACCSeconds = 119
		seedCalibrationSource(conn, profile.ID, "SHORT", short)
		reconcile(false)
		Expect(readProfile().SensorCalibrationVersion).To(BeNil())
		Expect(readProfile().SensorCalibrationSessions).To(BeZero())
	})
})

var _ = Describe("archive calibration provenance", func() {
	current := db.Profile{
		SensorCalibrationVersion: calibrationString(sensor.PersonalCalibrationVersion),
		SensorENMORestG:          calibrationFloat(0.02), SensorENMORefG: calibrationFloat(0.4),
	}
	summary := func(version string, low, high *float64) sensor.SensorSummaryResult {
		return sensor.SensorSummaryResult{CalculationInputs: sensor.CalculationInputs{
			CalibrationVersion: version, ENMORestG: low, ENMORefG: high,
		}, Metrics: sensor.SensorMetrics{Hybrid: &sensor.HybridMetrics{CalibrationVersion: version}}}
	}
	mismatchedHybrid := summary(sensor.PersonalCalibrationVersion, calibrationFloat(0.02), calibrationFloat(0.4))
	mismatchedHybrid.Metrics.Hybrid.CalibrationVersion = sensor.BootstrapCalibrationVersion
	DescribeTable("distinguishes unchanged calibration from unavailable or changed provenance",
		func(value sensor.SensorSummaryResult, profile db.Profile, reason string) {
			Expect(db.SensorCalibrationExclusionReason(value, profile)).To(Equal(reason))
		},
		Entry("fixed bootstrap values remain comparable", summary(sensor.BootstrapCalibrationVersion, calibrationFloat(0.01), calibrationFloat(0.30)), current, ""),
		Entry("matching personal values remain comparable", summary(sensor.PersonalCalibrationVersion, calibrationFloat(0.02), calibrationFloat(0.4)), current, ""),
		Entry("changed personal values are excluded", summary(sensor.PersonalCalibrationVersion, calibrationFloat(0.03), calibrationFloat(0.4)), current, "calibration_changed"),
		Entry("cleared current personal baseline is excluded", summary(sensor.PersonalCalibrationVersion, calibrationFloat(0.02), calibrationFloat(0.4)), db.Profile{}, "calibration_changed"),
		Entry("missing frozen values are unknown", summary(sensor.PersonalCalibrationVersion, nil, calibrationFloat(0.4)), current, "calibration_provenance_unknown"),
		Entry("missing version is unknown", summary("", calibrationFloat(0.02), calibrationFloat(0.4)), current, "calibration_provenance_unknown"),
		Entry("nonstandard bootstrap values are unknown", summary(sensor.BootstrapCalibrationVersion, calibrationFloat(0.02), calibrationFloat(0.3)), current, "calibration_provenance_unknown"),
		Entry("conflicting hybrid and input calibration versions are unknown", mismatchedHybrid, current, "calibration_provenance_unknown"),
		Entry("nonfinite frozen values are unknown", summary(sensor.PersonalCalibrationVersion, calibrationFloat(math.NaN()), calibrationFloat(0.4)), current, "calibration_provenance_unknown"),
	)
})

var _ = Describe("automatic processing publication fence", func() {
	var conn *gorm.DB
	var profile db.Profile
	BeforeEach(func() {
		conn = archiveCalibrationDB()
		testhelpers.CleanupDB(conn)
		profile = testhelpers.CreateProfile(conn, &db.Profile{Name: "Automatic processing"})
	})

	It("locks the profile before the parent and publishes through the guarded transaction", func() {
		parent := testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{SessionID: "WOD-20261029-AUTO", ProfileID: profile.ID, Status: "PENDING", Output: "before"})
		var sql bytes.Buffer
		logged := conn.Session(&gorm.Session{Logger: gormlogger.New(log.New(&sql, "", 0), gormlogger.Config{LogLevel: gormlogger.Info, Colorful: false})})
		Expect(db.WithSessionAutomaticWork(context.Background(), logged, profile.ID, parent.SessionID, func(tx *gorm.DB) error {
			return tx.Model(&db.AnalysisResult{}).Where("id = ?", parent.ID).Update("output", "published").Error
		})).To(Succeed())
		profileLock := strings.Index(sql.String(), `FROM "profiles"`)
		parentLock := strings.Index(sql.String(), `FROM "analysis_results"`)
		Expect(profileLock).To(BeNumerically(">=", 0))
		Expect(parentLock).To(BeNumerically(">", profileLock))
		Expect(strings.Count(sql.String(), "FOR UPDATE")).To(Equal(2))
		Expect(conn.First(&parent, parent.ID).Error).To(Succeed())
		Expect(parent.Output).To(Equal("published"))
	})

	It("keeps archived and restored-but-cancelled original work terminal without publishing", func() {
		now := time.Now().UTC()
		parent := testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{
			SessionID: "WOD-20261029-STOPPED", ProfileID: profile.ID, Status: "CANCELLED", Output: "frozen",
			ArchivedAt: &now, AutomaticProcessingCancelledAt: &now,
		})
		calls := 0
		publish := func(tx *gorm.DB) error {
			calls++
			return tx.Model(&db.AnalysisResult{}).Where("id = ?", parent.ID).Update("output", "late").Error
		}
		Expect(db.WithSessionAutomaticWork(context.Background(), conn, profile.ID, parent.SessionID, publish)).To(MatchError(db.ErrSessionWorkCancelled))
		Expect(conn.Model(&db.AnalysisResult{}).Where("id = ?", parent.ID).Update("archived_at", nil).Error).To(Succeed())
		Expect(db.SessionAutomaticWorkAllowed(context.Background(), conn, profile.ID, parent.SessionID)).To(MatchError(db.ErrSessionWorkCancelled))
		Expect(db.WithSessionAutomaticWork(context.Background(), conn, profile.ID, parent.SessionID, publish)).To(MatchError(db.ErrSessionWorkCancelled))
		Expect(calls).To(BeZero())
		Expect(conn.First(&parent, parent.ID).Error).To(Succeed())
		Expect(parent.Output).To(Equal("frozen"))
	})

	It("rejects a parent from another profile without invoking publication", func() {
		parent := testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{SessionID: "WOD-20261029-OTHER", ProfileID: profile.ID, Status: "PENDING"})
		other := testhelpers.CreateProfile(conn, &db.Profile{Name: "Another profile"})
		called := false
		Expect(db.WithSessionAutomaticWork(context.Background(), conn, other.ID, parent.SessionID, func(*gorm.DB) error { called = true; return nil })).To(MatchError(db.ErrSessionWorkCancelled))
		Expect(called).To(BeFalse())
	})

	It("allows a legacy session without an analysis parent to publish its initial work", func() {
		session := testhelpers.CreateSession(conn, &db.Session{SessionID: fmt.Sprintf("P%d-WOD-2025-02-01-07-30", profile.ID), ProfileID: profile.ID})
		Expect(db.WithSessionAutomaticWork(context.Background(), conn, profile.ID, session.SessionID, func(tx *gorm.DB) error {
			return tx.Model(&db.Session{}).Where("id = ?", session.ID).Update("wod_description", "Initial work published").Error
		})).To(Succeed())
		Expect(conn.First(&session, session.ID).Error).To(Succeed())
		Expect(session.WODDescription).To(Equal("Initial work published"))
	})
})
