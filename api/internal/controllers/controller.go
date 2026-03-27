package controllers

import (
	"context"
	"mime/multipart"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/worker"
)

const defaultGitCommit = "dev"

type QueueClient interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

type ObjectStorage interface {
	GenerateSignedURL(objectName string, method string, expires time.Duration) (string, error)
	UploadFile(ctx context.Context, file multipart.File, filename string) (string, error)
	ListObjects(ctx context.Context, prefix string) ([]string, error)
}

type AnalysisResultRepository interface {
	FindBySessionID(ctx context.Context, sessionID string) ([]db.AnalysisResult, error)
	ListRecent(ctx context.Context, limit int, profileID uint) ([]db.AnalysisResult, error)
	FindChunksBySessionID(ctx context.Context, sessionID string) ([]db.ChunkAnalysisResult, error)
}

type ProfileRepository interface {
	Create(ctx context.Context, profile *db.Profile) error
	FindByID(ctx context.Context, id uint) (*db.Profile, error)
}

type VideoAnalysisTaskFactory func(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint) (*asynq.Task, error)

type ChunkAnalysisTaskFactory func(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint, startSecs, endSecs float64) (*asynq.Task, error)

type Config struct {
	QueueClient          QueueClient
	AnalysisResults      AnalysisResultRepository
	Profiles             ProfileRepository
	StorageClient        ObjectStorage
	BucketName           string
	GitCommit            string
	NewVideoAnalysisTask VideoAnalysisTaskFactory
	NewChunkAnalysisTask ChunkAnalysisTaskFactory
	NewMergeChunksTask   VideoAnalysisTaskFactory
}

type Controller struct {
	queueClient          QueueClient
	analysisResults      AnalysisResultRepository
	profiles             ProfileRepository
	storageClient        ObjectStorage
	bucketName           string
	gitCommit            string
	newVideoAnalysisTask VideoAnalysisTaskFactory
	newChunkAnalysisTask ChunkAnalysisTaskFactory
	newMergeChunksTask   VideoAnalysisTaskFactory
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

	return &Controller{
		queueClient:          config.QueueClient,
		analysisResults:      config.AnalysisResults,
		profiles:             config.Profiles,
		storageClient:        config.StorageClient,
		bucketName:           config.BucketName,
		gitCommit:            commit,
		newVideoAnalysisTask: taskFactory,
		newChunkAnalysisTask: chunkTaskFactory,
		newMergeChunksTask:   mergeTaskFactory,
	}
}
