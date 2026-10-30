package db

import (
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("normalizeDatabaseURL", func() {
	It("supports a PostgreSQL URL", func() {
		dsn, err := normalizeDatabaseURL("postgresql://sunjinlee@localhost:5432/wod_dev")
		Expect(err).NotTo(HaveOccurred())

		config, err := pgx.ParseConfig(dsn)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.User).To(Equal("sunjinlee"))
		Expect(config.Host).To(Equal("localhost"))
		Expect(config.Port).To(Equal(uint16(5432)))
		Expect(config.Database).To(Equal("wod_dev"))
	})

	It("trims surrounding whitespace and quotes", func() {
		dsn, err := normalizeDatabaseURL("  \"postgresql://sunjinlee@localhost:5432/wod_dev?sslmode=disable\"  \n")
		Expect(err).NotTo(HaveOccurred())
		Expect(dsn).To(Equal("postgresql://sunjinlee@localhost:5432/wod_dev?sslmode=disable"))
	})

	It("rejects an empty quoted value", func() {
		_, err := normalizeDatabaseURL("  \"\"  ")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("PoolConfig", func() {
	It("keeps server and worker default budgets within the 20-connection total", func() {
		Expect(DefaultMaxOpenConns).To(Equal(20))
		Expect(DefaultServerMaxOpenConns + DefaultWorkerMaxOpenConns).To(Equal(DefaultMaxOpenConns))
	})

	It("matches MaxIdleConns to MaxOpenConns for connection reuse and clamps oversized idle limits", func() {
		defaults := normalizePoolConfig(PoolConfig{})
		Expect(defaults.MaxOpenConns).To(Equal(DefaultMaxOpenConns))
		Expect(defaults.MaxIdleConns).To(Equal(DefaultMaxOpenConns))
		Expect(defaults.ConnMaxIdleTime).To(Equal(DefaultConnMaxIdleTime))
		Expect(defaults.ConnMaxLifetime).To(Equal(DefaultConnMaxLifetime))

		workerPool := normalizePoolConfig(PoolConfig{MaxOpenConns: DefaultWorkerMaxOpenConns, MaxIdleConns: 99})
		Expect(workerPool.MaxOpenConns).To(Equal(8))
		Expect(workerPool.MaxIdleConns).To(Equal(8))

		custom := normalizePoolConfig(PoolConfig{
			MaxOpenConns:    10,
			MaxIdleConns:    6,
			ConnMaxIdleTime: 2 * time.Minute,
			ConnMaxLifetime: 15 * time.Minute,
		})
		Expect(custom.MaxOpenConns).To(Equal(10))
		Expect(custom.MaxIdleConns).To(Equal(6))
		Expect(custom.ConnMaxIdleTime).To(Equal(2 * time.Minute))
		Expect(custom.ConnMaxLifetime).To(Equal(15 * time.Minute))
	})

	It("applies MaxOpenConnections to sql.DB without opening network connections", func() {
		sqlDB, err := sql.Open("pgx", "postgresql://sunjinlee@localhost:5432/wod_dev?sslmode=disable")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)

		resolved := applyPoolConfig(sqlDB, PoolConfig{MaxOpenConns: DefaultWorkerMaxOpenConns})
		Expect(resolved.MaxOpenConns).To(Equal(DefaultWorkerMaxOpenConns))
		Expect(resolved.MaxIdleConns).To(Equal(DefaultWorkerMaxOpenConns))
		Expect(sqlDB.Stats().MaxOpenConnections).To(Equal(DefaultWorkerMaxOpenConns))
	})
})
