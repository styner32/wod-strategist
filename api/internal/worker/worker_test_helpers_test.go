package worker

// worker_test_helpers_test.go — shared test infrastructure for the worker package.
//
// Migration status:
//   - GeminiClient: uses real gemini.Client + testhelpers.MockTransport ✓
//   - QueueClient:  uses real asynq.Client + testhelpers.NewQueueClient (Redis) ✓
//   - StorageClient: fakeStorage remains for analysis handler tests where the
//     download is incidental (not the subject under test). Replace with
//     testhelpers.NewStorageClient + MockTransport when GCS interactions need
//     explicit verification.

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/gemini"
)

// ---------------------------------------------------------------------------
// fakeStorage — minimal storage stub. DownloadFile writes a 1-byte sentinel
// so handlers' defer os.Remove succeeds. ListObjects returns nil.
// Replace with testhelpers.NewStorageClient(bucketName, transport) when you
// need to verify exact GCS HTTP interactions.
// ---------------------------------------------------------------------------

type fakeStorage struct {
	downloadErr error
}

func (f *fakeStorage) DownloadFile(_ context.Context, _, destPath string) error {
	if f.downloadErr != nil {
		return f.downloadErr
	}
	return os.WriteFile(destPath, []byte{0x00}, 0o600)
}

func (f *fakeStorage) UploadFromFile(_ context.Context, _, objectName string) (string, error) {
	return "gs://test-bucket/" + objectName, nil
}

func (f *fakeStorage) ListObjects(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// listableStorage — controls what ListObjects returns and serves a real local
// file (or a 1-byte sentinel) on DownloadFile. Used by merge/highlight tests.
// ---------------------------------------------------------------------------

type listableStorage struct {
	objects      []string
	downloadPath string // local file to copy; 1-byte sentinel if empty
	uploadErr    error
}

func (s *listableStorage) ListObjects(_ context.Context, _ string) ([]string, error) {
	return s.objects, nil
}

func (s *listableStorage) DownloadFile(_ context.Context, _, destPath string) error {
	if s.downloadPath == "" {
		return os.WriteFile(destPath, []byte{0x00}, 0o600)
	}
	return copyFile(s.downloadPath, destPath)
}

func (s *listableStorage) UploadFromFile(_ context.Context, _, objectName string) (string, error) {
	if s.uploadErr != nil {
		return "", s.uploadErr
	}
	return "gs://test-bucket/" + objectName, nil
}

// ---------------------------------------------------------------------------
// fakeGemini — used when the test is NOT about Gemini (e.g. merge_chunks).
// Tests that verify Gemini parameters use the real client + MockTransport.
// ---------------------------------------------------------------------------

type fakeGemini struct {
	analysis   string
	geminiFile string
	analyzeErr error
}

func (f *fakeGemini) AnalyzeVideo(_ context.Context, _, _ string) (string, string, error) {
	return f.analysis, f.geminiFile, f.analyzeErr
}

func (f *fakeGemini) DeleteFile(_ context.Context, _ string) error { return nil }

func (f *fakeGemini) GenerateWorkoutMusic(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeGemini) UploadVideo(_ context.Context, _ string) (*gemini.UploadResult, error) {
	return &gemini.UploadResult{
		FileName: "files/mock-file",
		FileURI:  "https://example.test/files/mock-file",
		MIMEType: "video/mp4",
	}, nil
}

func (f *fakeGemini) IndexVideo(_ context.Context, _, _, _ string) (string, error) {
	return f.analysis, f.analyzeErr
}

func (f *fakeGemini) AnalyzeSegment(_ context.Context, _ string, _ string, _, _ time.Duration, _ string) (string, error) {
	return f.analysis, f.analyzeErr
}

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

// ---------------------------------------------------------------------------
// File system helpers
// ---------------------------------------------------------------------------

// writeFile writes data to path, creating parent directories as needed.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
