package worker

import (
	"context"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("probeMotionScore", func() {
	It("differentiates static and dynamic videos", func() {
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			Skip("ffmpeg is required for motion probe tests but was not found in PATH")
		}

		// 1. Create a static video (pure black)
		staticPath := filepath.Join(GinkgoT().TempDir(), "static.mp4")
		cmdStatic := exec.Command("ffmpeg",
			"-f", "lavfi", "-i", "color=c=black:size=64x64:rate=10",
			"-t", "3",
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "51",
			"-y", staticPath,
		)
		output, err := cmdStatic.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "Failed to create static video with ffmpeg: %s", string(output))

		// 2. Create a dynamic video (testsrc with moving patterns)
		dynamicPath := filepath.Join(GinkgoT().TempDir(), "dynamic.mp4")
		cmdDynamic := exec.Command("ffmpeg",
			"-f", "lavfi", "-i", "testsrc=size=64x64:rate=10",
			"-t", "3",
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "51",
			"-y", dynamicPath,
		)
		output, err = cmdDynamic.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "Failed to create dynamic video with ffmpeg: %s", string(output))

		// 3. Probe motion scores
		staticScore, err := probeMotionScore(context.Background(), staticPath)
		Expect(err).NotTo(HaveOccurred())

		dynamicScore, err := probeMotionScore(context.Background(), dynamicPath)
		Expect(err).NotTo(HaveOccurred())

		// Static video should have very low scene changes (often exactly 0.0 or near 0)
		Expect(staticScore).To(BeNumerically("<=", 0.05))

		// Dynamic video should have higher scene changes
		Expect(dynamicScore).To(BeNumerically(">", staticScore))
	})
})
