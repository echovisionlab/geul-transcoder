// Command transcoder consumes audio and video transcode commands.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
	"github.com/echovisionlab/geul-transcoder/cmd/internal/bootstrap"
	"github.com/echovisionlab/geul-transcoder/internal/app"
	"github.com/echovisionlab/geul-transcoder/internal/config"
	"github.com/echovisionlab/geul-transcoder/internal/handler"
	"github.com/echovisionlab/geul-transcoder/internal/jobs"
	"github.com/echovisionlab/geul-transcoder/internal/mq"
)

const serviceName = sharedtelemetry.ServiceTranscoder

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	err = app.Run(context.Background(), app.ProductionOptions(
		serviceName,
		cfg.Port,
		cfg.LogLevel,
		func(_ context.Context, _ context.CancelFunc) (app.Service, error) {
			return buildService(cfg)
		},
	))
	if err != nil {
		slog.Error("Transcoder service stopped with an error", "error", err)
		os.Exit(1)
	}
}

func buildService(cfg *config.Config) (_ app.Service, err error) {
	infrastructure, err := bootstrap.New(cfg, serviceName)
	if err != nil {
		return nil, err
	}
	defer infrastructure.CloseIfError(&err)

	jobHandler, err := handler.NewHandler(handler.Options{
		JobTimeoutMinutes: cfg.JobTimeoutMins,
		AudioHLSBitrate:   cfg.AudioHLSBitrate,
		FFmpeg:            infrastructure.FFmpeg,
		Storage:           infrastructure.Storage,
		Publisher:         infrastructure.Publisher,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize transcode handler: %w", err)
	}
	cancelSubscriber, err := mq.NewCancelSubscriber(infrastructure.Queue, jobHandler.CancelJob)
	if err != nil {
		return nil, fmt.Errorf("initialize cancel subscriber: %w", err)
	}
	infrastructure.AddCloser(cancelSubscriber)

	audioConsumer, videoConsumer, audioConfig, videoConfig, err := configureConsumers(cfg, infrastructure, jobHandler)
	if err != nil {
		return nil, err
	}

	service, err := app.NewGroup(
		infrastructure.Queue,
		[]app.Starter{audioConsumer, videoConsumer},
		infrastructure.Closers(),
	)
	if err != nil {
		return nil, fmt.Errorf("assemble service: %w", err)
	}
	service.AddHealthSource(cancelSubscriber)
	slog.Info("Transcoder service configured",
		"port", cfg.Port,
		"audio_workers", audioConfig.Workers,
		"video_workers", videoConfig.Workers,
		"instance_id", cfg.InstanceID,
	)
	return service, nil
}

func configureConsumers(
	cfg *config.Config,
	infrastructure *bootstrap.Infrastructure,
	jobHandler *handler.Handler,
) (*mq.Consumer, *mq.Consumer, jobs.QueueConfig, jobs.QueueConfig, error) {
	audioConfig := jobs.DefaultAudioQueueConfig()
	audioConfig.Workers = cfg.WorkerCount
	audioConfig.Timeout = time.Duration(cfg.JobTimeoutMins) * time.Minute
	audioConsumer, err := mq.NewConsumer(
		infrastructure.Queue,
		audioConfig,
		mq.DecodeAudioJob(jobHandler.HandleAudioJob),
	)
	if err != nil {
		return nil, nil, jobs.QueueConfig{}, jobs.QueueConfig{}, fmt.Errorf("initialize audio consumer: %w", err)
	}
	infrastructure.AddCloser(audioConsumer)

	videoConfig := jobs.DefaultVideoQueueConfig()
	videoConfig.Workers = cfg.WorkerCount
	videoConfig.Timeout = time.Duration(cfg.JobTimeoutMins) * time.Minute
	videoConsumer, err := mq.NewConsumer(
		infrastructure.Queue,
		videoConfig,
		mq.DecodeVideoJob(jobHandler.HandleVideoJob),
	)
	if err != nil {
		return nil, nil, jobs.QueueConfig{}, jobs.QueueConfig{}, fmt.Errorf("initialize video consumer: %w", err)
	}
	infrastructure.AddCloser(videoConsumer)
	return audioConsumer, videoConsumer, audioConfig, videoConfig, nil
}
