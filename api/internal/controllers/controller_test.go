package controllers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
)

var _ = Describe("New", func() {
	It("returns an error when the required storage client is missing", func() {
		controller, err := controllers.New(controllers.Config{})

		Expect(controller).To(BeNil())
		Expect(err).To(MatchError(controllers.ErrStorageClientRequired))
	})
})
