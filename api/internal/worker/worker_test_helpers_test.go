package worker

// worker_test_helpers_test.go — shared test infrastructure for the worker package.
//
// Migration status:
//   - GeminiClient:  uses real gemini.Client + testhelpers.MockTransport ✓
//   - QueueClient:   uses real asynq.Client + testhelpers.NewQueueClient (Redis) ✓
//   - StorageClient: uses real storage.Client + testhelpers.MockTransport ✓
//     Use testhelpers.MockGCSDownload/MockGCSListObjects/MockGCSUpload to
//     register GCS HTTP expectations. Use testhelpers.NewStorageClient to
//     create the real client backed by MockTransport.

import (
	"encoding/json"
	"os/exec"
	"path/filepath"

	"github.com/hibiken/asynq"
)

// ---------------------------------------------------------------------------
// Task payload helpers
// ---------------------------------------------------------------------------

func makeVideoAnalysisTask(p VideoAnalysisPayload) *asynq.Task {
	data, _ := json.Marshal(p)
	return asynq.NewTask(TypeVideoAnalysis, data)
}

// ---------------------------------------------------------------------------
// FFmpeg utilities (gated tests)
// ---------------------------------------------------------------------------

// hasFfmpeg returns true if ffmpeg is available in PATH.
func hasFfmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// createTinyMP4 uses ffmpeg to create a minimal 1-second silent black mp4.
// Skips the test if ffmpeg is unavailable or creation fails.
func createTinyMP4(t interface {
	Helper()
	Skip(args ...interface{})
	TempDir() string
}) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "tiny.mp4")
	cmd := exec.Command("ffmpeg",
		"-f", "lavfi", "-i", "color=c=black:size=64x64:rate=30",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo",
		"-t", "1",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "51",
		"-c:a", "aac", "-b:a", "32k",
		"-y", out,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skip("ffmpeg could not create test mp4:", string(output))
	}
	return out
}
