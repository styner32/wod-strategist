package worker

import (
	"log"
	"testing"

	"github.com/joho/godotenv"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
)

func TestWorker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Worker Suite")
}

var _ = BeforeSuite(func() {
	testhelpers.InitLogger()

	if err := godotenv.Load("../../.env.test"); err != nil {
		log.Printf("Warning: could not load .env.test: %v", err)
	}
})
