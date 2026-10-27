package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLiveReplay(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Live Replay Suite")
}
