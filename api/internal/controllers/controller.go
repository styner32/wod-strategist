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
}

type AnalysisResultRepository interface {
	FindBySessionID(ctx context.Context, sessionID string) ([]db.AnalysisResult, error)
	ListRecent(ctx context.Context, limit int) ([]db.AnalysisResult, error)
}

type VideoAnalysisTaskFactory func(sessionID, filePath, workoutType string, movements []string, injuries []string) (*asynq.Task, error)

type Config struct {
	QueueClient          QueueClient
	AnalysisResults      AnalysisResultRepository
	StorageClient        ObjectStorage
	BucketName           string
	GitCommit            string
	NewVideoAnalysisTask VideoAnalysisTaskFactory
}

type Controller struct {
	queueClient          QueueClient
	analysisResults      AnalysisResultRepository
	storageClient        ObjectStorage
	bucketName           string
	gitCommit            string
	newVideoAnalysisTask VideoAnalysisTaskFactory
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

	return &Controller{
		queueClient:          config.QueueClient,
		analysisResults:      config.AnalysisResults,
		storageClient:        config.StorageClient,
		bucketName:           config.BucketName,
		gitCommit:            commit,
		newVideoAnalysisTask: taskFactory,
	}
}
