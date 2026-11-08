package db_test

import (
	"context"
	"database/sql"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

var _ = Describe("GORM observability logger and pool reporter", func() {
	It("suppresses normal SQL and ErrRecordNotFound while logging >=200ms slow SQL at WARN and real errors at ERROR", func() {
		core, recorded := observer.New(zapcore.DebugLevel)
		glog := db.NewGormLogger(zap.New(core), 200*time.Millisecond)

		ctx, stats := db.WithQueryStats(context.Background())

		// Normal fast query: no log entry, counted in stats.
		glog.Trace(ctx, time.Now().Add(-15*time.Millisecond), func() (string, int64) {
			return "SELECT 1", 1
		}, nil)
		Expect(recorded.Len()).To(Equal(0))

		// ErrRecordNotFound: no log entry, not counted as error.
		glog.Trace(ctx, time.Now().Add(-20*time.Millisecond), func() (string, int64) {
			return "SELECT * FROM sessions WHERE id = 999", 0
		}, gorm.ErrRecordNotFound)
		Expect(recorded.Len()).To(Equal(0))

		// Slow query >= 200ms: logged at WARN.
		glog.Trace(ctx, time.Now().Add(-250*time.Millisecond), func() (string, int64) {
			return "SELECT * FROM chunk_analysis_results WHERE profile_id = 1", 2451
		}, nil)
		Expect(recorded.Len()).To(Equal(1))
		Expect(recorded.All()[0].Level).To(Equal(zapcore.WarnLevel))
		Expect(recorded.All()[0].Message).To(Equal("slow db query"))

		// Real SQL error: logged at ERROR.
		glog.Trace(ctx, time.Now().Add(-10*time.Millisecond), func() (string, int64) {
			return "INSERT INTO broken VALUES (1)", 0
		}, errors.New("syntax error"))
		Expect(recorded.Len()).To(Equal(2))
		Expect(recorded.All()[1].Level).To(Equal(zapcore.ErrorLevel))
		Expect(recorded.All()[1].Message).To(Equal("db query failed"))

		snap := stats.Snapshot()
		Expect(snap.Queries).To(Equal(int64(4)))
		Expect(snap.SlowQueries).To(Equal(int64(1)))
		Expect(snap.Errors).To(Equal(int64(1)))
		Expect(snap.Duration).To(BeNumerically(">=", 280*time.Millisecond))
	})

	It("computes pool wait deltas and elevates severity to WARN when wait_count_delta > 0", func() {
		prev := sql.DBStats{
			OpenConnections:    4,
			InUse:              2,
			Idle:               2,
			MaxOpenConnections: 6,
			WaitCount:          10,
			WaitDuration:       50 * time.Millisecond,
		}
		curNoWait := sql.DBStats{
			OpenConnections:    5,
			InUse:              1,
			Idle:               4,
			MaxOpenConnections: 6,
			WaitCount:          10,
			WaitDuration:       50 * time.Millisecond,
		}
		curWithWait := sql.DBStats{
			OpenConnections:    6,
			InUse:              6,
			Idle:               0,
			MaxOpenConnections: 6,
			WaitCount:          13,
			WaitDuration:       190 * time.Millisecond,
		}

		core, recorded := observer.New(zapcore.DebugLevel)
		zlog := zap.New(core)

		r1 := db.ComputePoolStatsReport("api", &prev, curNoWait)
		Expect(r1.WaitCountDelta).To(Equal(int64(0)))
		db.LogPoolStatsReport(zlog, r1)
		Expect(recorded.All()[0].Level).To(Equal(zapcore.InfoLevel))

		r2 := db.ComputePoolStatsReport("worker", &curNoWait, curWithWait)
		Expect(r2.WaitCountDelta).To(Equal(int64(3)))
		Expect(r2.WaitDurationDeltaMs).To(Equal(int64(140)))
		db.LogPoolStatsReport(zlog, r2)
		Expect(recorded.All()[1].Level).To(Equal(zapcore.WarnLevel))
	})
})
