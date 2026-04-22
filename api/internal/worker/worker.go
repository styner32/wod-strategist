package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	TypeVideoAnalysis     = "video:analysis"
	TypeChunkAnalysis     = "chunk:analysis"
	TypeMergeChunks       = "merge:chunks"
	TypeInjuryAnalysis    = "injury:analysis"
	TypeGenerateHighlight = "highlight:generate"
	TypeVerifyHighlights  = "highlight:verify"
	TypeGenerateHardSub   = "hardsub:generate"
	WorkoutTypeWOD        = "wod"
)

// VideoAnalysisPayload is reused by video analysis, chunk analysis, and merge chunks tasks.
type VideoAnalysisPayload struct {
	SessionID   string
	FilePath    string
	WorkoutType string
	Movements   []string
	Injuries    []string
	ProfileID   uint
	StartSecs   float64
	EndSecs     float64
}

// HighlightSegment is shared between video analysis (parsing) and highlight generation (processing).
type HighlightSegment struct {
	Start    string `json:"start"`              // e.g. "0:15" or "1:30"
	End      string `json:"end"`                // e.g. "0:28" or "1:45"
	Type     string `json:"type"`               // best_form, worst_form, fatigue_point, key_moment
	Movement string `json:"movement,omitempty"` // which exercise (e.g. "Snatch", "Pull-up")
	Reason   string `json:"reason"`             // human-readable reason
}

// StorageClient is the minimal interface over storage.Client used by handlers.
type StorageClient interface {
	DownloadFile(ctx context.Context, gcsURI, destPath string) error
	UploadFromFile(ctx context.Context, localPath, objectName string) (string, error)
	ListObjects(ctx context.Context, prefix string) ([]string, error)
}

// GeminiClient is the minimal interface over gemini.Client used by handlers.
type GeminiClient interface {
	// File-upload based analysis (used by chunk analysis, legacy path)
	AnalyzeVideo(ctx context.Context, filePath, prompt string) (string, string, error)
	DeleteFile(ctx context.Context, name string) error
	GenerateWorkoutMusic(ctx context.Context, model, prompt, outputPath string) error

	// Two-pass analysis: upload → index (Flash) → per-segment analysis (Pro)
	UploadVideo(ctx context.Context, filePath string) (*gemini.UploadResult, error)
	IndexVideo(ctx context.Context, fileURI, mimeType, prompt string) (string, error)
	AnalyzeSegment(ctx context.Context, fileURI, mimeType string, start, end time.Duration, prompt string) (string, error)

	// Lightweight Flash model query (e.g. verification)
	QueryVideoFlash(ctx context.Context, fileURI, mimeType, prompt string) (string, error)
}

// QueueClient is the minimal interface over asynq.Client used by handlers.
type QueueClient interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// Worker holds all dependencies shared across task handlers.
type Worker struct {
	DB            *gorm.DB
	StorageClient StorageClient
	BucketName    string
	GeminiClient  GeminiClient
	QueueClient   QueueClient
	UseCache      bool // enable context caching for long video analysis
	logger        *zap.Logger
}

func NewWorker(db *gorm.DB, storageClient StorageClient, bucketName string, geminiClient GeminiClient, queueClient QueueClient, log *zap.Logger) *Worker {
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{
		DB:            db,
		StorageClient: storageClient,
		BucketName:    bucketName,
		GeminiClient:  geminiClient,
		QueueClient:   queueClient,
		logger:        log,
	}
}

func NormalizeWorkoutType(_ string) string {
	return WorkoutTypeWOD
}

func IsValidWorkoutType(_ string) bool {
	return true // All values normalize to "wod"
}

// validateSessionID checks that a session ID is safe to use in file paths.
// Returns a non-retryable error if the session ID contains path separators
// or other dangerous characters.
func validateSessionID(sessionID string) error {
	if strings.ContainsRune(sessionID, filepath.Separator) {
		return fmt.Errorf("invalid session ID: contains path separator: %w", asynq.SkipRetry)
	}
	return nil
}

// createTempFile creates a temporary file with a kernel-generated random name
// in os.TempDir(). The prefix is used for human readability in debug logs.
// The caller is responsible for removing the file when done.
func createTempFile(prefix, ext string) (string, error) {
	f, err := os.CreateTemp("", prefix+"-*"+ext)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	path := f.Name()
	f.Close()
	return path, nil
}

// createTempDir creates a temporary directory with a kernel-generated random name
// in os.TempDir(). The prefix is used for human readability in debug logs.
// The caller is responsible for removing the directory when done.
func createTempDir(prefix string) (string, error) {
	dir, err := os.MkdirTemp("", prefix+"-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	return dir, nil
}

// lookupProfileString returns a human-readable profile string for the given profile ID.
func (w *Worker) lookupProfileString(profileID uint) string {
	personalProfile := "생년월일: 1984년 10월 17일, 성별: 남, 키: 164cm, 몸무게: 72kg"
	if profileID > 0 && w.DB != nil {
		var profile db.Profile
		if err := w.DB.First(&profile, profileID).Error; err == nil {
			genderKo := "기타"
			switch profile.Gender {
			case "male":
				genderKo = "남"
			case "female":
				genderKo = "여"
			}
			personalProfile = fmt.Sprintf("생년월일: %d년 %d월 %d일, 성별: %s, 키: %dcm, 몸무게: %.1fkg",
				profile.BirthYear, profile.BirthMonth, profile.BirthDay,
				genderKo, profile.HeightCm, profile.WeightKg)
		} else {
			w.logger.Warn("Profile not found, using default", zap.Uint("profile_id", profileID), zap.Error(err))
		}
	}
	return personalProfile
}

// parseTimestampToSeconds converts a "M:SS" or "MM:SS" timestamp string to seconds.
func parseTimestampToSeconds(ts string) (float64, error) {
	parts := strings.SplitN(ts, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid timestamp format: %s", ts)
	}
	var minutes, seconds float64
	if _, err := fmt.Sscanf(parts[0], "%f", &minutes); err != nil {
		return 0, fmt.Errorf("invalid minutes in timestamp %s: %w", ts, err)
	}
	if _, err := fmt.Sscanf(parts[1], "%f", &seconds); err != nil {
		return 0, fmt.Errorf("invalid seconds in timestamp %s: %w", ts, err)
	}
	return minutes*60 + seconds, nil
}

// randomHex returns a cryptographically random hex string of n bytes (2n hex chars).
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// probeVideoDuration uses ffprobe to determine the duration of a video file in seconds.
// Returns 0 if ffprobe is unavailable or the probe fails. Best-effort only.
func probeVideoDuration(ctx context.Context, filePath string) float64 {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath)
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	var duration float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(output)), "%f", &duration); err != nil {
		return 0
	}
	return duration
}

// maxAnalysisVideoSizeBytes is the threshold above which the merge worker
// creates an analysis-grade re-encode before sending to Gemini.
// Set lower than maxAnalysisFileSizeBytes to give the re-encode room to shrink.
const maxAnalysisVideoSizeBytes = 1000 << 20 // 500 MB

// formatFileSizeWorker returns a human-readable file size string.
func formatFileSizeWorker(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// runFFmpegAnalysisEncode creates a smaller analysis-grade video from the input.
// Uses CRF 28 (vs 23 for user-facing) and scales to 720p to reduce file size
// while preserving enough visual quality for Gemini pose analysis.
func runFFmpegAnalysisEncode(ctx context.Context, log *zap.Logger, inputPath, outputPath string) error {
	args := []string{
		"-i", inputPath,
		"-vf", "scale=720:-2,fps=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28",
		"-c:a", "aac", "-b:a", "64k",
		"-movflags", "+faststart",
		"-y",
		outputPath,
	}

	log.Info("Running FFmpeg analysis re-encode", zap.Strings("args", args))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("FFmpeg analysis re-encode failed", zap.Error(err), zap.String("output", string(output)))
		return fmt.Errorf("ffmpeg analysis encode: %s: %w", string(output), err)
	}

	log.Info("FFmpeg analysis re-encode completed", zap.String("output_path", outputPath))
	return nil
}
