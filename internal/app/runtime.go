package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
	"github.com/echovisionlab/geul-transcoder/internal/telemetry"
)

// HTTPServer is the subset of http.Server used by the runtime.
type HTTPServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

// SignalSource provides termination notifications and a cleanup function.
type SignalSource func() (<-chan os.Signal, func())

// BuildService constructs the worker service.
type BuildService func(context.Context, context.CancelFunc) (Service, error)

// InitTelemetry initializes the service telemetry providers.
type InitTelemetry func(context.Context, sharedtelemetry.ServiceName) (*telemetry.InitResult, error)

// NewServer constructs the health HTTP server.
type NewServer func(string, http.Handler) HTTPServer

// Options controls runtime startup and shutdown behavior.
type Options struct {
	ServiceName     sharedtelemetry.ServiceName
	Port            int
	LogLevel        string
	Build           BuildService
	Telemetry       InitTelemetry
	Server          NewServer
	Signals         SignalSource
	ShutdownTimeout time.Duration
}

// ProductionOptions returns the production runtime defaults.
func ProductionOptions(serviceName sharedtelemetry.ServiceName, port int, logLevel string, build BuildService) Options {
	return Options{
		ServiceName:     serviceName,
		Port:            port,
		LogLevel:        logLevel,
		Build:           build,
		Telemetry:       telemetry.Init,
		Server:          NewHTTPServer,
		Signals:         OSSignals,
		ShutdownTimeout: 30 * time.Second,
	}
}

// Run starts a service and blocks until a termination event is received.
func Run(parent context.Context, options Options) error {
	if err := validateOptions(options); err != nil {
		return err
	}

	stdoutHandler := newJSONHandler(options.LogLevel)
	slog.SetDefault(slog.New(telemetry.NewNormalizingHandler(stdoutHandler)))
	telemetryResult := initializeTelemetry(parent, options, stdoutHandler)
	if telemetryResult != nil {
		defer shutdownTelemetry(telemetryResult, options.ShutdownTimeout)
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	service, err := options.Build(ctx, cancel)
	if err != nil {
		emitServiceFailed(ctx, err)
		return fmt.Errorf("build service: %w", err)
	}
	defer closeService(ctx, service)
	if err := service.Start(ctx); err != nil {
		emitServiceFailed(ctx, err)
		return fmt.Errorf("start service: %w", err)
	}
	emitServiceReady(ctx)

	server := options.Server(fmt.Sprintf(":%d", options.Port), HealthHandler(service))
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()

	signals, stopSignals := options.Signals()
	defer stopSignals()

	runErr := waitForTermination(ctx, signals, serverErrors)
	emitServiceStopping(ctx)
	cancel()
	result := shutdownServer(server, options.ShutdownTimeout, runErr)
	if result != nil {
		emitServiceFailed(ctx, result)
	}
	return result
}

func validateOptions(options Options) error {
	if _, err := sharedtelemetry.ParseServiceName(options.ServiceName.String()); err != nil {
		return err
	}
	if options.Port <= 0 {
		return fmt.Errorf("port must be positive")
	}
	if options.Build == nil {
		return fmt.Errorf("service builder is required")
	}
	if options.Telemetry == nil {
		return fmt.Errorf("telemetry initializer is required")
	}
	if options.Server == nil {
		return fmt.Errorf("HTTP server factory is required")
	}
	if options.Signals == nil {
		return fmt.Errorf("signal source is required")
	}
	if options.ShutdownTimeout <= 0 {
		return fmt.Errorf("shutdown timeout must be positive")
	}
	return nil
}

func initializeTelemetry(parent context.Context, options Options, stdout slog.Handler) *telemetry.InitResult {
	result, err := options.Telemetry(parent, options.ServiceName)
	if err != nil {
		emitTelemetryPipelineDegraded(parent, err)
		return nil
	}
	slog.SetDefault(slog.New(telemetry.NewNormalizingHandler(telemetry.NewFanoutHandler(stdout, result.LogHandler))))
	return result
}

func shutdownTelemetry(result *telemetry.InitResult, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := result.Shutdown(ctx); err != nil {
		emitTelemetryPipelineDegraded(ctx, err)
	}
}

func closeService(ctx context.Context, service Service) {
	if err := service.Close(); err != nil {
		slog.WarnContext(ctx, "Service close error", "error", err)
	}
}

func waitForTermination(
	ctx context.Context,
	signals <-chan os.Signal,
	serverErrors <-chan error,
) error {
	select {
	case <-signals:
		return nil
	case <-ctx.Done():
		return nil
	case err := <-serverErrors:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("HTTP server: %w", err)
	}
}

func shutdownServer(server HTTPServer, timeout time.Duration, runErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil && runErr == nil {
		return fmt.Errorf("HTTP server shutdown: %w", err)
	}
	return runErr
}
