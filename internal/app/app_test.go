package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
	"github.com/echovisionlab/geul-transcoder/internal/telemetry"
	"github.com/stretchr/testify/require"
)

type fakeComponent struct {
	startErr error
	closeErr error
	started  int
	closed   int
	healthy  bool
}

func (f *fakeComponent) Start(context.Context) error { f.started++; return f.startErr }
func (f *fakeComponent) Close() error                { f.closed++; return f.closeErr }
func (f *fakeComponent) IsClosed() bool              { return !f.healthy }
func (f *fakeComponent) Healthy() bool               { return f.healthy }

type fakeServer struct {
	listenErr   error
	shutdownErr error
	shutdown    int
}

func (f *fakeServer) ListenAndServe() error { return f.listenErr }
func (f *fakeServer) Shutdown(context.Context) error {
	f.shutdown++
	return f.shutdownErr
}

func TestGroupLifecycle(t *testing.T) {
	t.Parallel()
	health := &fakeComponent{healthy: true}
	first := &fakeComponent{}
	second := &fakeComponent{closeErr: errors.New("second close")}
	group, err := NewGroup(health, []Starter{first, second}, []Closer{first, nil, second})
	if err != nil {
		t.Fatal(err)
	}
	if !group.Healthy() {
		t.Fatal("group should be healthy")
	}
	if err := group.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := group.Close(); err == nil || !strings.Contains(err.Error(), "second close") {
		t.Fatalf("close error = %v", err)
	}
	if first.started != 1 || second.started != 1 || first.closed != 1 || second.closed != 1 {
		t.Fatalf("lifecycle counts = %#v %#v", first, second)
	}
}

func TestGroupHealthReflectsSource(t *testing.T) {
	t.Parallel()
	health := &fakeComponent{healthy: true}
	group, err := NewGroup(health, nil, nil)
	if err != nil || !group.Healthy() {
		t.Fatalf("healthy group = %v, %v", group, err)
	}
	health.healthy = false
	if group.Healthy() {
		t.Fatal("group should be unhealthy")
	}
}

func TestGroupAddHealthSource(t *testing.T) {
	t.Parallel()
	primary := &fakeComponent{healthy: true}
	group, err := NewGroup(primary, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	group.AddHealthSource(nil)
	if !group.Healthy() {
		t.Fatal("nil health source should be ignored")
	}

	dependency := &fakeComponent{healthy: false}
	group.AddHealthSource(dependency)
	if group.Healthy() {
		t.Fatal("group should be unhealthy when an added dependency is unhealthy")
	}

	dependency.healthy = true
	if !group.Healthy() {
		t.Fatal("group should recover when every health source is healthy")
	}
}

func TestGroupRejectsInvalidLifecycleInputs(t *testing.T) {
	t.Parallel()
	health := &fakeComponent{healthy: true}
	if _, err := NewGroup(nil, nil, nil); err == nil {
		t.Fatal("expected missing health error")
	}
	group, _ := NewGroup(health, []Starter{nil}, nil)
	if err := group.Start(context.Background()); err == nil {
		t.Fatal("expected nil starter error")
	}
	failed := &fakeComponent{startErr: errors.New("start")}
	group, _ = NewGroup(health, []Starter{failed}, nil)
	if err := group.Start(context.Background()); err == nil {
		t.Fatal("expected start error")
	}
}

func TestRunSignalDisconnectAndServerBoundaries(t *testing.T) {
	for _, tc := range runScenarios() {
		t.Run(tc.name, func(t *testing.T) { runScenario(t, tc) })
	}
}

type runScenarioConfig struct {
	name          string
	telemetryErr  error
	telemetryStop error
	buildErr      error
	startErr      error
	closeErr      error
	listenErr     error
	shutdownErr   error
	trigger       string
	want          string
}

func runScenarios() []runScenarioConfig {
	return []runScenarioConfig{
		{name: "signal", trigger: "signal"},
		{name: "disconnect", trigger: "disconnect", telemetryErr: errors.New("telemetry")},
		{name: "closed server", trigger: "server", listenErr: http.ErrServerClosed, telemetryStop: errors.New("telemetry shutdown"), closeErr: errors.New("close")},
		{name: "server error", trigger: "server", listenErr: errors.New("listen"), want: "HTTP server"},
		{name: "shutdown error", trigger: "signal", shutdownErr: errors.New("shutdown"), want: "HTTP server shutdown"},
		{name: "build error", buildErr: errors.New("build"), want: "build service"},
		{name: "start error", startErr: errors.New("start"), want: "start service"},
	}
}

func runScenario(t *testing.T, tc runScenarioConfig) {
	t.Helper()
	service := &fakeComponent{healthy: true, startErr: tc.startErr, closeErr: tc.closeErr}
	server := &fakeServer{listenErr: tc.listenErr, shutdownErr: tc.shutdownErr}
	signalChannel := make(chan os.Signal, 1)
	ctx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	options := scenarioOptions(tc, service, server, signalChannel)
	if tc.buildErr == nil && tc.startErr == nil {
		switch tc.trigger {
		case "signal":
			signalChannel <- os.Interrupt
		case "disconnect":
			disconnect()
		}
	}
	err := Run(ctx, options)
	assertRunResult(t, err, tc.want)
	if tc.buildErr == nil && service.closed != 1 {
		t.Fatalf("service close count = %d", service.closed)
	}
}

func scenarioOptions(tc runScenarioConfig, service *fakeComponent, server *fakeServer, signals chan os.Signal) Options {
	return Options{
		ServiceName: sharedtelemetry.ServiceTranscoder,
		Port:        1234,
		LogLevel:    "debug",
		Build: func(context.Context, context.CancelFunc) (Service, error) {
			if tc.buildErr != nil {
				return nil, tc.buildErr
			}
			return service, nil
		},
		Telemetry: func(context.Context, sharedtelemetry.ServiceName) (*telemetry.InitResult, error) {
			if tc.telemetryErr != nil {
				return nil, tc.telemetryErr
			}
			return &telemetry.InitResult{
				LogHandler: slog.NewTextHandler(io.Discard, nil),
				Shutdown:   func(context.Context) error { return tc.telemetryStop },
			}, nil
		},
		Server:          func(string, http.Handler) HTTPServer { return server },
		Signals:         func() (<-chan os.Signal, func()) { return signals, func() { close(signals) } },
		ShutdownTimeout: time.Second,
	}
}

func assertRunResult(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" && err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
		t.Fatalf("Run error = %v, want %q", err, want)
	}
}

func TestRunRequiresCompleteOptions(t *testing.T) {
	require.Error(t, Run(context.Background(), Options{}))
	valid := Options{
		ServiceName: sharedtelemetry.ServiceTranscoder,
		Port:        8080,
		Build: func(context.Context, context.CancelFunc) (Service, error) {
			return &fakeComponent{}, nil
		},
		Telemetry: func(context.Context, sharedtelemetry.ServiceName) (*telemetry.InitResult, error) {
			return nil, errors.New("unused")
		},
		Server:          func(string, http.Handler) HTTPServer { return &fakeServer{} },
		Signals:         func() (<-chan os.Signal, func()) { return make(chan os.Signal), func() {} },
		ShutdownTimeout: time.Second,
	}
	invalid := []Options{
		{},
		func() Options { options := valid; options.Port = 0; return options }(),
		func() Options { options := valid; options.Build = nil; return options }(),
		func() Options { options := valid; options.Telemetry = nil; return options }(),
		func() Options { options := valid; options.Server = nil; return options }(),
		func() Options { options := valid; options.Signals = nil; return options }(),
		func() Options { options := valid; options.ShutdownTimeout = 0; return options }(),
	}
	for _, options := range invalid {
		require.Error(t, validateOptions(options))
	}
	_ = newJSONHandler("info")
	_ = newJSONHandler("debug")
}

func TestProductionOptionsProvidesRuntimeDefaults(t *testing.T) {
	build := func(context.Context, context.CancelFunc) (Service, error) { return nil, nil }
	options := ProductionOptions(sharedtelemetry.ServiceTranscoder, 8080, "debug", build)

	require.Equal(t, sharedtelemetry.ServiceTranscoder, options.ServiceName)
	require.Equal(t, 8080, options.Port)
	require.Equal(t, "debug", options.LogLevel)
	require.NotNil(t, options.Build)
	require.NotNil(t, options.Telemetry)
	require.NotNil(t, options.Server)
	require.NotNil(t, options.Signals)
	require.Equal(t, 30*time.Second, options.ShutdownTimeout)
}

func TestHealthHandlerAndProductionHelpers(t *testing.T) {
	for _, healthy := range []bool{true, false} {
		service := &fakeComponent{healthy: healthy}
		response := httptest.NewRecorder()
		HealthHandler(service)(response, httptest.NewRequest(http.MethodGet, "/health", nil))
		wantStatus := http.StatusOK
		if !healthy {
			wantStatus = http.StatusServiceUnavailable
		}
		if response.Code != wantStatus || response.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("health response = %d %v", response.Code, response.Header())
		}
	}
	server := NewHTTPServer(":1234", http.NewServeMux())
	if server == nil {
		t.Fatal("expected HTTP server")
	}
	_, stop := OSSignals()
	stop()
}
