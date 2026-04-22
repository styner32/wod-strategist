package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Unit tests for split_video.go
// ---------------------------------------------------------------------------

var _ = Describe("discoverChunkFiles", func() {
	It("returns sorted chunk files matching chunk_*.mp4 pattern", func() {
		tmpDir := GinkgoT().TempDir()

		// Create files in unsorted order
		for _, name := range []string{"chunk_002.mp4", "chunk_000.mp4", "chunk_001.mp4", "other.txt"} {
			Expect(os.WriteFile(filepath.Join(tmpDir, name), []byte("test"), 0o644)).To(Succeed())
		}

		files, err := discoverChunkFiles(tmpDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(Equal([]string{"chunk_000.mp4", "chunk_001.mp4", "chunk_002.mp4"}))
	})

	It("returns empty when no chunk files exist", func() {
		tmpDir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(tmpDir, "other.mp4"), []byte("test"), 0o644)).To(Succeed())

		files, err := discoverChunkFiles(tmpDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(BeEmpty())
	})

	It("ignores directories", func() {
		tmpDir := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(tmpDir, "chunk_000.mp4"), 0o755)).To(Succeed())

		files, err := discoverChunkFiles(tmpDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(BeEmpty())
	})
})

// TestSplitFFmpegDirect is a standalone Go test for FFmpeg splitting.
// Run in isolation with: go test -run TestSplitFFmpegDirect -v
func TestSplitFFmpegDirect(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	// Create a 25-second test video
	inputPath := filepath.Join(t.TempDir(), "input.mp4")
	cmd := exec.Command("ffmpeg",
		"-f", "lavfi", "-i", "color=c=black:size=64x64:rate=30",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo",
		"-t", "25",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "51",
		"-c:a", "aac", "-b:a", "32k",
		"-y", inputPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("Failed to create test video: %s", output)
	}

	// Split into chunks using our wrapper
	outDir := t.TempDir()
	pattern := filepath.Join(outDir, "chunk_%03d.mp4")

	if err := runFFmpegSplit(context.Background(), zap.NewNop(), inputPath, pattern, 10); err != nil {
		t.Fatalf("runFFmpegSplit failed: %v", err)
	}

	// Verify chunks
	chunks, err := discoverChunkFiles(outDir)
	if err != nil {
		t.Fatalf("discoverChunkFiles: %v", err)
	}

	// 25s at ~10s keyframe boundaries → should produce 2-4 chunks
	if len(chunks) < 2 || len(chunks) > 4 {
		t.Errorf("expected 2-4 chunks, got %d: %v", len(chunks), chunks)
	}

	t.Logf("Produced %d chunks: %v", len(chunks), chunks)

	// Verify each chunk is a valid file with non-zero size
	for _, name := range chunks {
		fi, err := os.Stat(filepath.Join(outDir, name))
		if err != nil {
			t.Errorf("chunk %s: stat failed: %v", name, err)
			continue
		}
		if fi.Size() == 0 {
			t.Errorf("chunk %s: file is empty", name)
		}
		t.Logf("  %s: %d bytes", name, fi.Size())
	}
}
