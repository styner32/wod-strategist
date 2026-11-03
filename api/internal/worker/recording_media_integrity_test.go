package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
)

var _ = Describe("Recording media persisted integrity", func() {
	It("round-trips every frame timestamp and rejects unsupported or incomplete signatures", func() {
		signature := videoFrameSignature{FrameCount: 2, Digest: strings.Repeat("a", 64), Start: 0, End: 2.0 / 30, timings: []frameTiming{{0, 1000}, {1000, 1000}}}
		raw, err := encodeFrameSignature(signature)
		Expect(err).NotTo(HaveOccurred())
		decoded, err := decodeFrameSignature(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(Equal(signature))
		var stored storedFrameSignature
		Expect(json.Unmarshal(raw, &stored)).To(Succeed())
		stored.Timings = stored.Timings[:1]
		bad, err := json.Marshal(stored)
		Expect(err).NotTo(HaveOccurred())
		_, err = decodeFrameSignature(db.JSONDocument(bad))
		Expect(err).To(HaveOccurred())
		stored.Timings = []storedFrameTiming{{0, 1000}, {1000, 1000}}
		stored.Version = "unknown"
		bad, err = json.Marshal(stored)
		Expect(err).NotTo(HaveOccurred())
		_, err = decodeFrameSignature(db.JSONDocument(bad))
		Expect(err).To(HaveOccurred())
	})

	It("verifies 52 seconds across six persisted preparations including a short final chunk", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg required for the small recording fixture")
		}
		ctx, cancel := context.WithTimeout(withLimitedMediaThreads(context.Background()), 2*time.Minute)
		defer cancel()
		tmp := GinkgoT().TempDir()
		colors := []string{"red", "green", "blue", "yellow", "cyan", "magenta"}
		var expected []videoFrameSignature
		var manifest strings.Builder
		var total float64
		for i, color := range colors {
			duration := 10.0
			if i == len(colors)-1 {
				duration = 2
			}
			source := integrityTestVideo(ctx, tmp, fmt.Sprintf("source-%d", i), fmt.Sprintf("color=c=%s:size=160x120:rate=30:duration=%.1f", color, duration))
			profile, err := probeChunkStreamProfile(ctx, source)
			Expect(err).NotTo(HaveOccurred())
			timeline, err := probeVideoTimeline(ctx, source)
			Expect(err).NotTo(HaveOccurred())
			prepared, err := prepareChunkForConcatWithEvidence(ctx, zap.NewNop(), tmp, i, source, profile, 160, 120, false, timeline, 0)
			Expect(err).NotTo(HaveOccurred())
			raw, err := encodeFrameSignature(prepared.signature)
			Expect(err).NotTo(HaveOccurred())
			decoded, err := decodeFrameSignature(raw)
			Expect(err).NotTo(HaveOccurred())
			expected = append(expected, decoded)
			fmt.Fprintf(&manifest, "file '%s'\nduration %.9f\n", prepared.normPath, prepared.duration)
			total += prepared.duration
		}
		Expect(total).To(BeNumerically("~", 52, 1.0/30000))
		list := filepath.Join(tmp, "concat.txt")
		Expect(os.WriteFile(list, []byte(manifest.String()), 0o600)).To(Succeed())
		merged := filepath.Join(tmp, "merged.mp4")
		Expect(runFFmpegStreamCopyConcat(ctx, zap.NewNop(), list, merged, total, false)).To(Succeed())
		Expect(verifyVideoFrameSequence(ctx, merged, expected)).To(Succeed())
		Expect(verifyMediaEndpoints(ctx, merged, total, 2.0/30+1.0/30000)).To(Succeed())
		wrongOrder := append([]videoFrameSignature(nil), expected...)
		wrongOrder[0], wrongOrder[1] = wrongOrder[1], wrongOrder[0]
		Expect(verifyVideoFrameSequence(ctx, merged, wrongOrder)).To(HaveOccurred())
	})
})
