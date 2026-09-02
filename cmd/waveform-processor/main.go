// Command waveform-processor consumes waveform generation commands.
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
	"github.com/echovisionlab/geul-transcoder/internal/jobs"
	"github.com/echovisionlab/geul-transcoder/internal/mq"
	"github.com/echovisionlab/geul-transcoder/internal/waveform"
)

const serviceName = sharedtelemetry.ServiceWaveformProcessor

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
		slog.Error("Waveform processor stopped with an error", "error", err)
		os.Exit(1)
	}
}

func buildService(cfg *config.Config) (_ app.Service, err error) {
	infrastructure, err := bootstrap.New(cfg, serviceName)
	if err != nil {
		return nil, err
	}
	defer infrastructure.CloseIfError(&err)

	processor, err := waveform.NewProcessor(waveform.Options{
		WorkDirs:  infrastructure.FFmpeg,
		Generator: infrastructure.FFmpeg,
		Storage:   infrastructure.Storage,
		Publisher: infrastructure.Publisher,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize waveform processor: %w", err)
	}
	cancelSubscriber, err := mq.NewWaveformCancelSubscriber(infrastructure.Queue, processor.CancelJob)
	if err != nil {
		return nil, fmt.Errorf("initialize cancel subscriber: %w", err)
	}
	infrastructure.AddCloser(cancelSubscriber)

	queueConfig := jobs.DefaultWaveformQueueConfig()
	workers := cfg.WaveformWorkerCount
	if workers <= 0 {
		workers = cfg.WorkerCount
	}
	queueConfig.Workers = workers
	queueConfig.Timeout = time.Duration(cfg.JobTimeoutMins) * time.Minute

	consumer, err := mq.NewConsumer(
		infrastructure.Queue,
		queueConfig,
		mq.DecodeWaveformJob(processor.HandleGenerateJob),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize waveform consumer: %w", err)
	}
	infrastructure.AddCloser(consumer)

	service, err := app.NewGroup(infrastructure.Queue, []app.Starter{consumer}, infrastructure.Closers())
	if err != nil {
		return nil, fmt.Errorf("assemble service: %w", err)
	}
	service.AddHealthSource(cancelSubscriber)
	slog.Info("Waveform processor configured",
		"port", cfg.Port,
		"workers", queueConfig.Workers,
		"instance_id", cfg.InstanceID,
	)
	return service, nil
}
