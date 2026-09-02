package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func newJSONHandler(level string) slog.Handler {
	logLevel := slog.LevelInfo
	if level == "debug" {
		logLevel = slog.LevelDebug
	}
	return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
}

// HealthHandler reports service health as a JSON response.
func HealthHandler(service Service) http.HandlerFunc {
	return func(writer http.ResponseWriter, _ *http.Request) {
		status := http.StatusOK
		body := map[string]any{"status": "ok", "postgresql": true}
		if !service.Healthy() {
			status = http.StatusServiceUnavailable
			body["status"] = "degraded"
			body["postgresql"] = false
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(body)
	}
}

// NewHTTPServer creates the instrumented health HTTP server.
func NewHTTPServer(address string, handler http.Handler) HTTPServer {
	return &http.Server{Addr: address, Handler: otelhttp.NewHandler(handler, "health-http")}
}

// OSSignals subscribes to the operating-system termination signals.
func OSSignals() (<-chan os.Signal, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	return signals, func() {
		signal.Stop(signals)
		close(signals)
	}
}
