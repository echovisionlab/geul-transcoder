// Package bootstrap assembles shared service infrastructure.
package bootstrap

import (
	"fmt"
	"log/slog"

	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
	"github.com/echovisionlab/geul-transcoder/internal/app"
	"github.com/echovisionlab/geul-transcoder/internal/config"
	"github.com/echovisionlab/geul-transcoder/internal/ffmpeg"
	"github.com/echovisionlab/geul-transcoder/internal/mq"
	"github.com/echovisionlab/geul-transcoder/internal/storage"
)

// Infrastructure contains the shared resources used by either worker binary.
type Infrastructure struct {
	FFmpeg    *ffmpeg.Executor
	Storage   *storage.S3Client
	Queue     *mq.Connection
	Publisher *mq.Publisher

	closers []app.Closer
}

// New initializes FFmpeg, object storage, PGMQ, and the signal publisher.
func New(cfg *config.Config, component sharedtelemetry.ServiceName) (_ *Infrastructure, err error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is required")
	}
	executor, err := ffmpeg.NewExecutor(cfg.FFmpegPath, cfg.FFprobePath, cfg.FFmpegTempDir)
	if err != nil {
		return nil, fmt.Errorf("initialize FFmpeg: %w", err)
	}
	removedEntries, err := executor.CleanupStaleWorkDirs()
	if err != nil {
		return nil, fmt.Errorf("clean stale work directories: %w", err)
	}
	slog.Info("Cleaned stale work directories",
		"component", component,
		"temp_dir", cfg.FFmpegTempDir,
		"removed_entries", removedEntries,
	)

	objectStorage, err := storage.NewS3Client(storage.Options{
		Bucket:          cfg.S3Bucket,
		Region:          cfg.S3Region,
		Endpoint:        cfg.S3Endpoint,
		AccessKeyID:     cfg.S3AccessKeyID,
		SecretAccessKey: cfg.S3SecretAccessKey,
		ForcePathStyle:  cfg.S3ForcePathStyle,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize object storage: %w", err)
	}
	connection, err := mq.NewConnection(cfg.DatabaseDSN)
	if err != nil {
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	infrastructure := &Infrastructure{FFmpeg: executor, Storage: objectStorage, Queue: connection}
	infrastructure.AddCloser(connection)
	defer infrastructure.CloseIfError(&err)
	publisher, err := mq.NewPublisher(connection)
	if err != nil {
		return nil, fmt.Errorf("initialize publisher: %w", err)
	}
	infrastructure.Publisher = publisher
	infrastructure.AddCloser(publisher)
	return infrastructure, nil
}

// AddCloser registers a resource for reverse-order shutdown.
func (i *Infrastructure) AddCloser(closer app.Closer) {
	i.closers = append(i.closers, closer)
}

// Closers returns a snapshot of registered resources.
func (i *Infrastructure) Closers() []app.Closer {
	return append([]app.Closer(nil), i.closers...)
}

// CloseIfError releases resources when the surrounding constructor failed.
func (i *Infrastructure) CloseIfError(err *error) {
	if *err != nil {
		_ = app.CloseAll(i.closers)
	}
}
