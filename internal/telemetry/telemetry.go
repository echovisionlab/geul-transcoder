// Package telemetry configures tracing, metrics, logs, and their normalization.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"

	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitResult holds telemetry initialization results.
type InitResult struct {
	Shutdown   func(context.Context) error
	LogHandler slog.Handler
}

type telemetryFactories struct {
	resource func(context.Context, string) (*resource.Resource, error)
	trace    func(context.Context) (sdktrace.SpanExporter, error)
	metric   func(context.Context) (sdkmetric.Exporter, error)
	log      func(context.Context) (sdklog.Exporter, error)
}

// Init initializes OTel providers with OTLP HTTP exporters.
func Init(ctx context.Context, serviceName sharedtelemetry.ServiceName) (*InitResult, error) {
	return initWithFactories(ctx, serviceName, defaultTelemetryFactories())
}

func defaultTelemetryFactories() telemetryFactories {
	return telemetryFactories{
		resource: func(ctx context.Context, serviceName string) (*resource.Resource, error) {
			return resource.New(ctx,
				resource.WithFromEnv(),
				resource.WithHost(),
				resource.WithAttributes(semconv.ServiceName(serviceName)),
			)
		},
		trace: func(ctx context.Context) (sdktrace.SpanExporter, error) {
			return otlptracehttp.New(ctx)
		},
		metric: func(ctx context.Context) (sdkmetric.Exporter, error) {
			return otlpmetrichttp.New(ctx)
		},
		log: func(ctx context.Context) (sdklog.Exporter, error) {
			return otlploghttp.New(ctx)
		},
	}
}

func initWithFactories(ctx context.Context, serviceName sharedtelemetry.ServiceName, factories telemetryFactories) (*InitResult, error) {
	canonicalServiceName, err := sharedtelemetry.ParseServiceName(serviceName.String())
	if err != nil {
		return nil, err
	}
	serviceNameValue := canonicalServiceName.String()
	res, err := factories.resource(ctx, serviceNameValue)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	traceExporter, err := factories.trace(ctx)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	metricExporter, err := factories.metric(ctx)
	if err != nil {
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("create metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)

	logExporter, err := factories.log(ctx)
	if err != nil {
		_ = mp.Shutdown(ctx)
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("create log exporter: %w", err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	global.SetLoggerProvider(lp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logHandler := otelslog.NewHandler(serviceNameValue, otelslog.WithLoggerProvider(lp))

	return newInitResult(logHandler,
		namedShutdown{name: "trace", shutdown: tp.Shutdown},
		namedShutdown{name: "metric", shutdown: mp.Shutdown},
		namedShutdown{name: "log", shutdown: lp.Shutdown},
	), nil
}

type namedShutdown struct {
	name     string
	shutdown func(context.Context) error
}

func newInitResult(logHandler slog.Handler, shutdowns ...namedShutdown) *InitResult {
	return &InitResult{
		LogHandler: logHandler,
		Shutdown: func(ctx context.Context) error {
			var firstErr error
			for _, item := range shutdowns {
				if err := item.shutdown(ctx); err != nil && firstErr == nil {
					firstErr = fmt.Errorf("%s shutdown: %w", item.name, err)
				}
			}
			return firstErr
		},
	}
}
