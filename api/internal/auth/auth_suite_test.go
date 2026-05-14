package auth_test

import (
	"log"
	"testing"

	"github.com/joho/godotenv"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAuth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Auth Suite")
}

var _ = BeforeSuite(func() {
	if err := godotenv.Load("../../.env.test"); err != nil {
		log.Printf("Warning: could not load .env.test: %v", err)
	}
})
