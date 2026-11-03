package db_test

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("performance record outcome migration", func() {
	It("preserves all outcome shapes through repeated down/up cycles", func() {
		dsn := os.Getenv("TEST_MIGRATION_DATABASE_URL")
		if dsn == "" {
			Skip("set TEST_MIGRATION_DATABASE_URL to a dedicated database ending in _test")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, dsn)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(conn.Close(context.Background())).To(Succeed()) })
		var database string
		Expect(conn.QueryRow(ctx, "SELECT current_database()").Scan(&database)).To(Succeed())
		Expect(strings.HasSuffix(database, "_test")).To(BeTrue(), "migration specs require a test database")
		tx, err := conn.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(tx.Rollback(context.Background())).To(Succeed()) })
		exec := func(sql string) {
			_, err := tx.Exec(ctx, sql)
			Expect(err).NotTo(HaveOccurred())
		}
		migration := func(name string) string {
			raw, err := os.ReadFile("migrations/" + name)
			Expect(err).NotTo(HaveOccurred())
			return string(raw)
		}
		// Temporary tables shadow any existing tables only on this connection.
		// Use the real original DDL; never roll back the database's schema version.
		exec("CREATE TEMP TABLE profiles (id BIGINT PRIMARY KEY); CREATE TEMP TABLE analysis_results (id BIGINT PRIMARY KEY); INSERT INTO profiles VALUES (1); INSERT INTO analysis_results VALUES (1)")
		initial := migration("000056_workout_progress.up.sql")
		start, end := strings.Index(initial, "CREATE TABLE performance_records ("), strings.Index(initial, "CREATE INDEX performance_records_")
		Expect(start).To(BeNumerically(">=", 0))
		Expect(end).To(BeNumerically(">", start))
		exec(strings.Replace(initial[start:end], "CREATE TABLE performance_records (", "CREATE TEMP TABLE performance_records (", 1))
		exec(migration("000057_allow_zero_rep_performance_records.up.sql"))
		exec(`INSERT INTO performance_records
			(profile_id, analysis_id, session_id, interval_id, source_version, status, movement,
			start_secs, end_secs, elapsed_seconds, reps, measurement, technique, workout_prescription,
			reps_per_minute, rate_basis, median_cycle_seconds, original_candidate, performed_at,
			performed_at_source, confirmed_at, created_at, updated_at)
			SELECT 1, 1, 'migration-session', v.interval_id, 'source', 'confirmed', 'Power Clean',
			0, 60, 60, v.reps, 'window', 'singles', 'Grace', v.reps, 'actual_60_second_window',
			2, jsonb_build_object('evidence', v.interval_id), '2026-10-30T00:00:00Z', 'session',
			'2026-10-30T01:00:00Z', '2026-10-30T01:00:00Z', '2026-10-30T01:00:00Z'
			FROM (VALUES ('legacy-positive', 3), ('legacy-zero', 0)) AS v(interval_id, reps)`)
		up := migration("000062_performance_record_outcome_and_optional_reps.up.sql")
		down := migration("000062_performance_record_outcome_and_optional_reps.down.sql")
		exec(up)
		var zeroBackfilled bool
		Expect(tx.QueryRow(ctx, `SELECT outcome = 'incomplete' AND reps IS NULL
			AND reps_per_minute IS NULL AND rate_basis IS NULL AND median_cycle_seconds IS NULL
			FROM performance_records WHERE interval_id = 'legacy-zero'`).Scan(&zeroBackfilled)).To(Succeed())
		Expect(zeroBackfilled).To(BeTrue())
		exec(`INSERT INTO performance_records
			(profile_id, analysis_id, session_id, interval_id, source_version, status, outcome, movement,
			start_secs, end_secs, elapsed_seconds, reps, load_value, load_unit, load_basis, load_kg,
			measurement, technique, workout_prescription, reps_per_minute, rate_basis,
			original_candidate, performed_at, performed_at_source, confirmed_at, created_at, updated_at)
			SELECT 1, 1, 'migration-session', v.interval_id, 'source', 'confirmed', v.outcome, v.movement,
			60, 120, 60, v.reps, v.weight, CASE WHEN v.weight IS NOT NULL THEN 'kg' END,
			CASE WHEN v.weight IS NOT NULL THEN 'total' END, v.weight,
			'set', 'unspecified', 'prescription', v.rpm, v.rate_basis,
			jsonb_build_object('evidence', v.interval_id), '2026-10-30T00:00:00Z', 'session',
			'2026-10-30T01:00:00Z', '2026-10-30T01:00:00Z', '2026-10-30T01:00:00Z'
			FROM (VALUES
			('rest', 'rest', '', NULL::integer, NULL::float8, NULL::text, NULL::float8),
			('incomplete', 'incomplete', 'Power Clean', NULL, NULL, NULL, 60),
			('completed-without-count', 'completed', 'Power Clean', NULL, NULL, NULL, 50),
			('completed-with-count', 'completed', 'Power Clean', 5, 5, 'normalized_per_minute', 70)
			) AS v(interval_id, outcome, movement, reps, rpm, rate_basis, weight)`)
		snapshot := func() string {
			var raw string
			Expect(tx.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM performance_records r").Scan(&raw)).To(Succeed())
			return raw
		}
		before := snapshot()
		for cycle := 0; cycle < 2; cycle++ {
			exec(down)
			var distinctShapes bool
			Expect(tx.QueryRow(ctx, `SELECT
				(SELECT movement = '' AND reps IS NULL AND reps_per_minute IS NULL AND rate_basis IS NULL FROM performance_records WHERE interval_id = 'rest')
				AND (SELECT movement <> '' AND reps = 0 AND reps_per_minute = 0 AND rate_basis = 'normalized_per_minute' FROM performance_records WHERE interval_id = 'incomplete')
				AND (SELECT movement <> '' AND reps IS NULL AND reps_per_minute IS NULL AND rate_basis IS NULL FROM performance_records WHERE interval_id = 'completed-without-count')`).Scan(&distinctShapes)).To(Succeed())
			Expect(distinctShapes).To(BeTrue())
			exec(up)
			Expect(snapshot()).To(Equal(before))
		}
		exec("SAVEPOINT invalid_rest")
		_, err = tx.Exec(ctx, "UPDATE performance_records SET load_value = 20, load_unit = 'kg', load_basis = 'total', load_kg = 20 WHERE interval_id = 'rest'")
		Expect(err).To(HaveOccurred(), "rest must still reject non-null load fields")
		exec("ROLLBACK TO SAVEPOINT invalid_rest")
		Expect(snapshot()).To(Equal(before))
	})
})
