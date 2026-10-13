package controllers

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WOD start request replay", func() {
	It("keeps restore-automatic distinct from omitted and manual requests across SQL storage", func() {
		omitted := wodStartRequestDocument(nil)
		reset := wodStartRequestDocument(json.RawMessage("null"))
		manual := wodStartRequestDocument(json.RawMessage("120.5"))
		sqlValue, err := reset.Value()
		Expect(err).To(Succeed())
		Expect(sqlValue).NotTo(BeNil())
		Expect(wodStartRequestsEqual(omitted, reset)).To(BeFalse())
		Expect(wodStartRequestsEqual(manual, reset)).To(BeFalse())
		Expect(wodStartRequestsEqual(manual, wodStartRequestDocument(json.RawMessage("120.50")))).To(BeTrue())
	})
})
