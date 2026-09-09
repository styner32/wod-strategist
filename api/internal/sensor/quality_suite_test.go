package sensor_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestQuality(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Sensor quality") }
