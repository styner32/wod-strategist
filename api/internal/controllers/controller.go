package controllers

import (
	"context"
	"mime/multipart"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/worker"
	"gorm.io/gorm"
)

const defaultGitCommit = "dev"

type QueueClient interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

type ObjectStorage interface {
	GenerateSignedURL(objectName string, method string, expires time.Duration) (string, error)
	UploadFile(ctx context.Context, file multipart.File, filename string) (string, error)
	ListObjects(ctx context.Context, prefix string) ([]string, error)
	ListObjectInfos(ctx context.Context, prefix string) ([]storage.ObjectInfo, error)
}

type AnalysisResultRepository interface {
	FindBySessionID(ctx context.Context, sessionID string) ([]db.AnalysisResult, error)
	ListRecent(ctx context.Context, limit int, profileID uint) ([]db.AnalysisResult, error)
	FindChunksBySessionID(ctx context.Context, sessionID string) ([]db.ChunkAnalysisResult, error)
	Archive(ctx context.Context, id uint) error
	Unarchive(ctx context.Context, id uint) error
}

type ProfileRepository interface {
	Create(ctx context.Context, profile *db.Profile) error
	FindByID(ctx context.Context, id uint) (*db.Profile, error)
	ListByUser(ctx context.Context, userID uint, includeArchived bool) ([]db.Profile, error)
	Update(ctx context.Context, profile *db.Profile) error
	Archive(ctx context.Context, id uint) error
	Unarchive(ctx context.Context, id uint) error
}

type HighlightResultRepository interface {
	FindBySessionID(ctx context.Context, sessionID string) ([]db.HighlightResult, error)
	FindByID(ctx context.Context, id uint) (*db.HighlightResult, error)
}

type VideoAnalysisTaskFactory func(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint, enableTTS bool) (*asynq.Task, error)

type ChunkAnalysisTaskFactory func(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint, startSecs, endSecs float64, heartRateBPM int) (*asynq.Task, error)

type HighlightTaskFactory func(sessionID string, profileID uint, maxDuration int) (*asynq.Task, error)

type VerifyHighlightsTaskFactory func(sessionID string) (*asynq.Task, error)

type HardSubTaskFactory func(sessionID string, profileID uint, enableTTS bool) (*asynq.Task, error)

type Config struct {
	DB                      *gorm.DB
	QueueClient             QueueClient
	AnalysisResults         AnalysisResultRepository
	Profiles                ProfileRepository
	HighlightResults        HighlightResultRepository
	StorageClient           ObjectStorage
	BucketName              string
	GitCommit               string
	NewVideoAnalysisTask    VideoAnalysisTaskFactory
	NewChunkAnalysisTask    ChunkAnalysisTaskFactory
	NewMergeChunksTask      VideoAnalysisTaskFactory
	NewGenerateHighlight    HighlightTaskFactory
	NewVerifyHighlightsTask VerifyHighlightsTaskFactory
	NewGenerateHardSub      HardSubTaskFactory
}

type Controller struct {
	db                      *gorm.DB
	queueClient             QueueClient
	analysisResults         AnalysisResultRepository
	profiles                ProfileRepository
	highlightResults        HighlightResultRepository
	storageClient           ObjectStorage
	bucketName              string
	gitCommit               string
	newVideoAnalysisTask    VideoAnalysisTaskFactory
	newChunkAnalysisTask    ChunkAnalysisTaskFactory
	newMergeChunksTask      VideoAnalysisTaskFactory
	newGenerateHighlight    HighlightTaskFactory
	newVerifyHighlightsTask VerifyHighlightsTaskFactory
	newGenerateHardSub      HardSubTaskFactory
}

func New(config Config) *Controller {
	taskFactory := config.NewVideoAnalysisTask
	if taskFactory == nil {
		taskFactory = worker.NewVideoAnalysisTask
	}

	commit := config.GitCommit
	if commit == "" {
		commit = defaultGitCommit
	}

	chunkTaskFactory := config.NewChunkAnalysisTask
	if chunkTaskFactory == nil {
		chunkTaskFactory = worker.NewChunkAnalysisTask
	}

	mergeTaskFactory := config.NewMergeChunksTask
	if mergeTaskFactory == nil {
		mergeTaskFactory = worker.NewMergeChunksTask
	}

	highlightTaskFactory := config.NewGenerateHighlight
	if highlightTaskFactory == nil {
		highlightTaskFactory = worker.NewGenerateHighlightTask
	}

	verifyTaskFactory := config.NewVerifyHighlightsTask
	if verifyTaskFactory == nil {
		verifyTaskFactory = worker.NewVerifyHighlightsTask
	}

	hardSubFactory := config.NewGenerateHardSub
	if hardSubFactory == nil {
		hardSubFactory = func(sessionID string, profileID uint, enableTTS bool) (*asynq.Task, error) {
			return worker.NewGenerateHardSubTask(sessionID, profileID, enableTTS)
		}
	}

	return &Controller{
		db:                      config.DB,
		queueClient:             config.QueueClient,
		analysisResults:         config.AnalysisResults,
		profiles:                config.Profiles,
		highlightResults:        config.HighlightResults,
		storageClient:           config.StorageClient,
		bucketName:              config.BucketName,
		gitCommit:               commit,
		newVideoAnalysisTask:    taskFactory,
		newChunkAnalysisTask:    chunkTaskFactory,
		newMergeChunksTask:      mergeTaskFactory,
		newGenerateHighlight:    highlightTaskFactory,
		newVerifyHighlightsTask: verifyTaskFactory,
		newGenerateHardSub:      hardSubFactory,
	}
}
