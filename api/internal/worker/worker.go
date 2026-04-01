package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
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
	Start  string `json:"start"`  // e.g. "0:15" or "1:30"
	End    string `json:"end"`    // e.g. "0:28" or "1:45"
	Type   string `json:"type"`   // best_form, worst_form, fatigue_point, key_moment
	Reason string `json:"reason"` // human-readable reason
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
