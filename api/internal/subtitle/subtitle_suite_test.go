package subtitle_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSubtitle(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Subtitle Suite")
}

