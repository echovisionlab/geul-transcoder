package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func TestCanonicalResourceServiceNameCannotBeOverriddenByEnvironment(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "wrong-service")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=also-wrong,deployment.environment=test")

	res, err := defaultTelemetryFactories().resource(t.Context(), sharedtelemetry.ServiceTranscoder.String())
	if err != nil {
		t.Fatal(err)
	}
	value, ok := res.Set().Value(semconv.ServiceNameKey)
	if !ok || value.AsString() != sharedtelemetry.ServiceTranscoder.String() {
		t.Fatalf("service.name = %q, want %q", value.AsString(), sharedtelemetry.ServiceTranscoder)
	}
}

func TestInitCreatesProvidersAndShutdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := Init(ctx, sharedtelemetry.ServiceTranscoder)
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if result.LogHandler == nil {
		t.Fatal("expected log handler")
	}
	if !result.LogHandler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("expected log handler to accept info records")
	}
	if err := result.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}

func TestInitReportsEveryFactoryFailure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*telemetryFactories)
		want   string
	}{
		{name: "resource", mutate: func(f *telemetryFactories) {
			f.resource = func(context.Context, string) (*resource.Resource, error) { return nil, errors.New("resource") }
		}, want: "create resource"},
		{name: "trace", mutate: func(f *telemetryFactories) {
			f.trace = func(context.Context) (sdktrace.SpanExporter, error) { return nil, errors.New("trace") }
		}, want: "create trace exporter"},
		{name: "metric", mutate: func(f *telemetryFactories) {
			f.metric = func(context.Context) (sdkmetric.Exporter, error) { return nil, errors.New("metric") }
		}, want: "create metric exporter"},
		{name: "log", mutate: func(f *telemetryFactories) {
			f.log = func(context.Context) (sdklog.Exporter, error) { return nil, errors.New("log") }
		}, want: "create log exporter"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			factories := defaultTelemetryFactories()
			tc.mutate(&factories)
			_, err := initWithFactories(context.Background(), sharedtelemetry.ServiceTranscoder, factories)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("initWithFactories error = %v", err)
			}
		})
	}
}

func TestInitRejectsUnknownServiceIdentity(t *testing.T) {
	t.Parallel()
	result, err := initWithFactories(context.Background(), sharedtelemetry.ServiceName("test"), telemetryFactories{})
	if result != nil || err == nil || !strings.Contains(err.Error(), "unknown canonical service name") {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestInitResultReturnsFirstShutdownErrorAndRunsAll(t *testing.T) {
	t.Parallel()
	var calls []string
	result := newInitResult(nil,
		namedShutdown{name: "trace", shutdown: func(context.Context) error {
			calls = append(calls, "trace")
			return errors.New("trace")
		}},
		namedShutdown{name: "metric", shutdown: func(context.Context) error {
			calls = append(calls, "metric")
			return errors.New("metric")
		}},
		namedShutdown{name: "log", shutdown: func(context.Context) error {
			calls = append(calls, "log")
			return nil
		}},
	)
	err := result.Shutdown(context.Background())
	if err == nil || !strings.Contains(err.Error(), "trace shutdown") {
		t.Fatalf("Shutdown error = %v", err)
	}
	if strings.Join(calls, ",") != "trace,metric,log" {
		t.Fatalf("shutdown calls = %v", calls)
	}
}
