package movement_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestMovement(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Movement Suite")
}
