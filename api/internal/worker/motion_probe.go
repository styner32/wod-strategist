package worker

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
)

var sceneScoreRegex = regexp.MustCompile(`lavfi\.scene_score=([\d.]+)`)

// probeMotionScore returns the mean inter-frame scene-change score of a chunk.
// Downscaled to 160px @ 2fps so the probe costs ~100ms CPU per 10s chunk.
// Score ~0.0 = static frame, higher = more motion.
func probeMotionScore(ctx context.Context, chunkPath string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", chunkPath,
		"-vf", "scale=160:-2,fps=2,select='gte(scene,0)',metadata=print",
		"-an", "-f", "null", "-")
	out, err := cmd.CombinedOutput() // metadata=print writes to stderr
	if err != nil {
		return 0, fmt.Errorf("ffmpeg motion probe: %w", err)
	}
	matches := sceneScoreRegex.FindAllStringSubmatch(string(out), -1)
	if len(matches) == 0 {
		return 0, fmt.Errorf("no scene scores in probe output")
	}
	var sum float64
	for _, m := range matches {
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		sum += v
	}
	return sum / float64(len(matches)), nil
}
