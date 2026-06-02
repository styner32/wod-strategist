package testhelpers

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	g "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
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

func CreateUser(dbConn *gorm.DB, userAttr *db.User) db.User {
	u := db.User{
		Username:     userAttr.Username,
		PasswordHash: userAttr.PasswordHash,
		DeletedAt:    userAttr.DeletedAt,
	}

	g.Expect(dbConn.Create(&u).Error).NotTo(g.HaveOccurred())
	return u
}

var userCounter uint64

func CreateProfile(dbConn *gorm.DB, profileAttr *db.Profile) db.Profile {
	p := db.Profile{
		UserID:       profileAttr.UserID,
		Name:         profileAttr.Name,
		BirthYear:    profileAttr.BirthYear,
		BirthMonth:   profileAttr.BirthMonth,
		BirthDay:     profileAttr.BirthDay,
		Gender:       profileAttr.Gender,
		HeightCm:     profileAttr.HeightCm,
		WeightKg:     profileAttr.WeightKg,
		FitnessLevel: profileAttr.FitnessLevel,
		Injuries:     profileAttr.Injuries,
		ArchivedAt:   profileAttr.ArchivedAt,
	}

	if p.UserID == 0 {
		val := atomic.AddUint64(&userCounter, 1)
		u := CreateUser(dbConn, &db.User{
			Username: "test-user-" + strconv.FormatUint(val, 10),
		})
		p.UserID = u.ID
	}

	g.Expect(dbConn.Create(&p).Error).NotTo(g.HaveOccurred())
	return p
}

func CreateSession(dbConn *gorm.DB, sessionAttr *db.Session) db.Session {
	s := db.Session{
		SessionID:      sessionAttr.SessionID,
		ProfileID:      sessionAttr.ProfileID,
		Status:         sessionAttr.Status,
		IdempotencyKey: sessionAttr.IdempotencyKey,
		WODDescription: sessionAttr.WODDescription,
	}

	g.Expect(dbConn.Create(&s).Error).NotTo(g.HaveOccurred())
	return s
}
