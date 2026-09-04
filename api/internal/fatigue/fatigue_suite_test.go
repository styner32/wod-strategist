package fatigue_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
)

func TestFatigue(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fatigue Suite")
}

var _ = BeforeSuite(func() {
	testhelpers.InitLogger()
})
