package wodeval

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestWODEvaluation(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "WOD Evaluation Suite")
}
