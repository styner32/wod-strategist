package gemini

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGemini(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gemini Suite")
}
