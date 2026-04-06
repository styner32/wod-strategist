package testhelpers

import (
	"fmt"
	"os"
	"strings"

	g "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func InitDB() (*gorm.DB, error) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgresql://sunjinlee@localhost:5432/wod_test?sslmode=disable"
	}

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("testDB: open: %w", err)
	}

	return gdb, nil
}

func CleanupDB(db *gorm.DB) {
	var dbName string
	if err := db.Raw("SELECT current_database()").Scan(&dbName).Error; err != nil {
		panic(fmt.Sprintf("failed to get current database name: %v", err))
	}

	// if database name is not end with _test, panic
	g.Expect(strings.HasSuffix(dbName, "_test")).To(g.BeTrue(),
		"database name '%s' does not end with _test, skipping cleanup to prevent data loss", dbName)

	var tables []string

	err := db.Raw("SELECT tablename FROM pg_tables WHERE schemaname = 'public'").Scan(&tables).Error
	g.Expect(err).NotTo(g.HaveOccurred())

	if len(tables) == 0 {
		return
	}

	for _, table := range tables {
		if table == "spatial_ref_sys" || table == "schema_migrations" {
			continue
		}

		query := fmt.Sprintf("TRUNCATE TABLE \"%s\" RESTART IDENTITY CASCADE", table)
		err := db.Exec(query).Error
		g.Expect(err).NotTo(g.HaveOccurred(), "Failed to truncate table: "+table)
	}
}
