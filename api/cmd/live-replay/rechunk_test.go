package main

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live replay regeneration", func() {
	It("reuses measured sensor bytes and immutable provenance without consuming broken old video chunks", func() {
		raw := validationTelemetry()
		m := validationManifest(raw)
		m.SourceProfileID, m.SourceSessionID = 10, "WOD-20261026-VALIDATION"
		m.Video.Object = "videos/10/WOD-20261026-VALIDATION/merged.mp4"
		m.Chunks = []ReplayChunk{{File: "old-broken-chunk.mp4", MediaEndSecs: 18.333333}}
		dir := GinkgoT().TempDir()
		Expect(writeJSON(filepath.Join(dir, "manifest.json"), m)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, m.SensorFile), raw, 0600)).To(Succeed())
		got, data, err := loadRechunkSource(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(Equal(raw))
		Expect(got.Video).To(Equal(m.Video))
		Expect(got.SensorSHA256).To(Equal(m.SensorSHA256))
		Expect(os.WriteFile(filepath.Join(dir, m.SensorFile), append(raw, '\n'), 0600)).To(Succeed())
		_, _, err = loadRechunkSource(dir)
		Expect(err).To(MatchError(ContainSubstring("integrity")))
	})

	It("refuses to overwrite a fixture before requiring DB or cloud credentials", func() {
		dir := GinkgoT().TempDir()
		err := prepareFixture(context.Background(), PrepareOptions{OutputDir: dir, FromFixtureDir: "unused", ChunkSeconds: 10})
		Expect(err).To(MatchError(ContainSubstring("already exists")))
	})

	It("rejects the old long-first/short-last cadence even with the right count and total", func() {
		chunks := []ReplayChunk{{File: "first", MediaEndSecs: 18.333333}, {File: "second", MediaStartSecs: 18.333333, MediaEndSecs: 28.333333}, {File: "last", MediaStartSecs: 28.333333, MediaEndSecs: 30}}
		Expect(validateChunkCadence(chunks, 10, 30, 30)).To(MatchError(ContainSubstring("cadence")))
		chunks = []ReplayChunk{{MediaEndSecs: 10}, {MediaStartSecs: 10, MediaEndSecs: 20}, {MediaStartSecs: 20, MediaEndSecs: 25}}
		Expect(validateChunkCadence(chunks, 10, 25, 30)).To(Succeed())
	})
})
