package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	SlowSQLThreshold        = 200 * time.Millisecond
	PoolStatsReportInterval = 60 * time.Second
	maxLoggedSQLBytes       = 2048
)

type queryStatsKey struct{}

// QueryStats tracks SQL execution counts and timings within a request or task context.
type QueryStats struct {
	queries     atomic.Int64
	durationNs  atomic.Int64
	slowQueries atomic.Int64
	errCount    atomic.Int64
}

type QueryStatsSnapshot struct {
	Queries     int64
	Duration    time.Duration
	SlowQueries int64
	Errors      int64
}

func WithQueryStats(ctx context.Context) (context.Context, *QueryStats) {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, ok := ctx.Value(queryStatsKey{}).(*QueryStats); ok && existing != nil {
		return ctx, existing
	}
	stats := &QueryStats{}
	return context.WithValue(ctx, queryStatsKey{}, stats), stats
}

func QueryStatsFromContext(ctx context.Context) (QueryStatsSnapshot, bool) {
	if ctx == nil {
		return QueryStatsSnapshot{}, false
	}
	stats, ok := ctx.Value(queryStatsKey{}).(*QueryStats)
	if !ok || stats == nil {
		return QueryStatsSnapshot{}, false
	}
	return stats.Snapshot(), true
}

func (s *QueryStats) Snapshot() QueryStatsSnapshot {
	if s == nil {
		return QueryStatsSnapshot{}
	}
	return QueryStatsSnapshot{
		Queries:     s.queries.Load(),
		Duration:    time.Duration(s.durationNs.Load()),
		SlowQueries: s.slowQueries.Load(),
		Errors:      s.errCount.Load(),
	}
}

type zapGormLogger struct {
	log           *zap.Logger
	level         gormlogger.LogLevel
	slowThreshold time.Duration
}

// NewGormLogger creates a structured Zap GORM logger that suppresses normal SQL
// and ErrRecordNotFound noise while logging >= slowThreshold queries at WARN
// and real SQL errors at ERROR.
func NewGormLogger(log *zap.Logger, slowThreshold time.Duration) gormlogger.Interface {
	if slowThreshold <= 0 {
		slowThreshold = SlowSQLThreshold
	}
	return &zapGormLogger{
		log:           log,
		level:         gormlogger.Warn,
		slowThreshold: slowThreshold,
	}
}

func (l *zapGormLogger) logger() *zap.Logger {
	if l.log != nil {
		return l.log
	}
	if logger.Log != nil {
		return logger.Log
	}
	return zap.NewNop()
}

func (l *zapGormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	next := *l
	next.level = level
	return &next
}

func (l *zapGormLogger) Info(_ context.Context, msg string, data ...interface{}) {
	if l.level >= gormlogger.Info {
		l.logger().Sugar().Infof(msg, data...)
	}
}

func (l *zapGormLogger) Warn(_ context.Context, msg string, data ...interface{}) {
	if l.level >= gormlogger.Warn {
		l.logger().Sugar().Warnf(msg, data...)
	}
}

func (l *zapGormLogger) Error(_ context.Context, msg string, data ...interface{}) {
	if l.level >= gormlogger.Error {
		l.logger().Sugar().Errorf(msg, data...)
	}
}

func (l *zapGormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	elapsed := time.Since(begin)
	realErr := err != nil && !errors.Is(err, gorm.ErrRecordNotFound)
	isSlow := l.slowThreshold > 0 && elapsed >= l.slowThreshold

	if ctx != nil {
		if stats, ok := ctx.Value(queryStatsKey{}).(*QueryStats); ok && stats != nil {
			stats.queries.Add(1)
			stats.durationNs.Add(elapsed.Nanoseconds())
			if isSlow {
				stats.slowQueries.Add(1)
			}
			if realErr {
				stats.errCount.Add(1)
			}
		}
	}

	if l.level <= gormlogger.Silent {
		return
	}

	switch {
	case realErr && l.level >= gormlogger.Error:
		sqlStr, rows := fc()
		l.logger().Error("db query failed",
			zap.Int64("elapsed_ms", elapsed.Milliseconds()),
			zap.Int64("rows", rows),
			zap.String("sql", truncateSQLForLog(sqlStr)),
			zap.Error(err),
		)
	case isSlow && l.level >= gormlogger.Warn:
		sqlStr, rows := fc()
		l.logger().Warn("slow db query",
			zap.Int64("elapsed_ms", elapsed.Milliseconds()),
			zap.Int64("threshold_ms", l.slowThreshold.Milliseconds()),
			zap.Int64("rows", rows),
			zap.String("sql", truncateSQLForLog(sqlStr)),
		)
	case l.level >= gormlogger.Info:
		sqlStr, rows := fc()
		l.logger().Debug("db query",
			zap.Int64("elapsed_ms", elapsed.Milliseconds()),
			zap.Int64("rows", rows),
			zap.String("sql", truncateSQLForLog(sqlStr)),
		)
	}
}

func truncateSQLForLog(s string) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= maxLoggedSQLBytes {
		return trimmed
	}
	return trimmed[:maxLoggedSQLBytes] + "...(truncated)"
}

type PoolStatsReport struct {
	Process                string
	OpenConnections        int
	InUse                  int
	Idle                   int
	MaxOpenConnections     int
	WaitCountDelta         int64
	WaitDurationDeltaMs    int64
	MaxIdleClosedDelta     int64
	MaxIdleTimeClosedDelta int64
	MaxLifetimeClosedDelta int64
}

func ComputePoolStatsReport(process string, prev *sql.DBStats, cur sql.DBStats) PoolStatsReport {
	report := PoolStatsReport{
		Process:            process,
		OpenConnections:    cur.OpenConnections,
		InUse:              cur.InUse,
		Idle:               cur.Idle,
		MaxOpenConnections: cur.MaxOpenConnections,
	}
	if prev != nil {
		if d := cur.WaitCount - prev.WaitCount; d > 0 {
			report.WaitCountDelta = d
		}
		if d := cur.WaitDuration - prev.WaitDuration; d > 0 {
			report.WaitDurationDeltaMs = d.Milliseconds()
		}
		if d := cur.MaxIdleClosed - prev.MaxIdleClosed; d > 0 {
			report.MaxIdleClosedDelta = d
		}
		if d := cur.MaxIdleTimeClosed - prev.MaxIdleTimeClosed; d > 0 {
			report.MaxIdleTimeClosedDelta = d
		}
		if d := cur.MaxLifetimeClosed - prev.MaxLifetimeClosed; d > 0 {
			report.MaxLifetimeClosedDelta = d
		}
	} else {
		report.WaitCountDelta = cur.WaitCount
		report.WaitDurationDeltaMs = cur.WaitDuration.Milliseconds()
		report.MaxIdleClosedDelta = cur.MaxIdleClosed
		report.MaxIdleTimeClosedDelta = cur.MaxIdleTimeClosed
		report.MaxLifetimeClosedDelta = cur.MaxLifetimeClosed
	}
	return report
}

func LogPoolStatsReport(log *zap.Logger, r PoolStatsReport) {
	if log == nil {
		log = logger.Log
	}
	if log == nil {
		return
	}
	fields := []zap.Field{
		zap.String("process", r.Process),
		zap.Int("open_conns", r.OpenConnections),
		zap.Int("in_use", r.InUse),
		zap.Int("idle", r.Idle),
		zap.Int("max_open_conns", r.MaxOpenConnections),
		zap.Int64("wait_count_delta", r.WaitCountDelta),
		zap.Int64("wait_duration_delta_ms", r.WaitDurationDeltaMs),
		zap.Int64("max_idle_closed_delta", r.MaxIdleClosedDelta),
		zap.Int64("max_idle_time_closed_delta", r.MaxIdleTimeClosedDelta),
		zap.Int64("max_lifetime_closed_delta", r.MaxLifetimeClosedDelta),
	}
	if r.WaitCountDelta > 0 {
		log.Warn("db connection pool stats", fields...)
		return
	}
	log.Info("db connection pool stats", fields...)
}

// StartPoolStatsReporter logs sql.DB connection pool usage and wait deltas every interval
// until ctx is cancelled.
func StartPoolStatsReporter(ctx context.Context, sqlDB *sql.DB, log *zap.Logger, process string, interval time.Duration) {
	if sqlDB == nil {
		return
	}
	if interval <= 0 {
		interval = PoolStatsReportInterval
	}
	baseline := sqlDB.Stats()
	prev := baseline
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cur := sqlDB.Stats()
				report := ComputePoolStatsReport(process, &prev, cur)
				prev = cur
				LogPoolStatsReport(log, report)
			}
		}
	}()
}
