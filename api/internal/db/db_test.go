package db

import (
	"github.com/jackc/pgx/v5"
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
