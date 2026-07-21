package worker

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProbeMotionScoreDirect(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	// 1. Create a static video (pure black)
	staticPath := filepath.Join(t.TempDir(), "static.mp4")
	cmdStatic := exec.Command("ffmpeg",
		"-f", "lavfi", "-i", "color=c=black:size=64x64:rate=10",
		"-t", "3",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "51",
		"-y", staticPath,
	)
	if output, err := cmdStatic.CombinedOutput(); err != nil {
		t.Fatalf("Failed to create static video: %s: %v", output, err)
	}

	// 2. Create a dynamic video (testsrc with moving patterns)
	dynamicPath := filepath.Join(t.TempDir(), "dynamic.mp4")
	cmdDynamic := exec.Command("ffmpeg",
		"-f", "lavfi", "-i", "testsrc=size=64x64:rate=10",
		"-t", "3",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "51",
		"-y", dynamicPath,
	)
	if output, err := cmdDynamic.CombinedOutput(); err != nil {
		t.Fatalf("Failed to create dynamic video: %s: %v", output, err)
	}

	// 3. Probe motion scores
	staticScore, err := probeMotionScore(context.Background(), staticPath)
	if err != nil {
		t.Fatalf("probeMotionScore for static failed: %v", err)
	}

	dynamicScore, err := probeMotionScore(context.Background(), dynamicPath)
	if err != nil {
		t.Fatalf("probeMotionScore for dynamic failed: %v", err)
	}

	t.Logf("Static score: %f, Dynamic score: %f", staticScore, dynamicScore)

	// Static video should have very low scene changes (often exactly 0.0 or near 0)
	if staticScore > 0.05 {
		t.Errorf("Expected static video score to be very low, got %f", staticScore)
	}

	// Dynamic video should have higher scene changes
	if dynamicScore <= staticScore {
		t.Errorf("Expected dynamic video score (%f) to be higher than static video score (%f)", dynamicScore, staticScore)
	}
}
