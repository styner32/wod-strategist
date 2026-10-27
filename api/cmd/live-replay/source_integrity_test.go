package main

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live replay source integrity", func() {
	It("honors accepted-upload checksums while allowing historical metadata without them", func() {
		raw := []byte("measured telemetry")
		metadata, _ := json.Marshal(map[string]any{"expected_sha256": digest(raw), "expected_size_bytes": len(raw)})
		Expect(validateSourceSensorIntegrity(raw, metadata)).To(Succeed())
		Expect(validateSourceSensorIntegrity(raw, []byte(`{"sha256":"","size_bytes":0}`))).To(Succeed())
		Expect(validateSourceSensorIntegrity(raw, []byte(`{"expected_sha256":"wrong"}`))).To(MatchError(ContainSubstring("SHA256")))
		Expect(validateSourceSensorIntegrity(raw, []byte(`{"expected_size_bytes":1}`))).To(MatchError(ContainSubstring("size")))
	})
})
