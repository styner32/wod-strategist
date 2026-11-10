package history

import (
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
	"gorm.io/gorm"
)

var (
	dbConn *gorm.DB
	dbOnce sync.Once
)

func ensureTestDB() {
	dbOnce.Do(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
	})
}

var _ = BeforeSuite(func() {
	testhelpers.InitLogger()
})

var _ = AfterSuite(func() {
	if dbConn == nil {
		return
	}

	sqlDB, err := dbConn.DB()
	if err == nil {
		_ = sqlDB.Close()
	}
})

func TestHistory(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "History Suite")
}
