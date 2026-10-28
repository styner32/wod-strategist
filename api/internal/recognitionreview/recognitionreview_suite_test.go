package recognitionreview_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestRecognitionReview(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Recognition Review Suite")
}
