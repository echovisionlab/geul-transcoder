package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
)

func TestSharedNormalizingHandlerRedactsAndNormalizes(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	handler := NewNormalizingHandler(slog.NewJSONHandler(&output, nil))
	record := slog.NewRecord(time.Now(), slog.LevelError, "transcode failed", 0)
	record.AddAttrs(
		slog.String("jobId", "job-1"),
		slog.String("sourceKey", "private/key"),
		slog.String("displayName", "private name"),
		slog.Any("error", errors.New("private detail")),
		slog.Any("details", map[string]any{"fileName": "private.wav"}),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["job_id"] != "job-1" || entry["error_type"] != "error_string" {
		t.Fatalf("normalized entry = %#v", entry)
	}
	for _, forbidden := range []string{"source_key", "display_name", "details"} {
		if _, ok := entry[forbidden]; ok {
			t.Fatalf("sensitive or untyped field %q was retained: %#v", forbidden, entry)
		}
	}
}

func TestSharedNormalizingHandlerAddsRequestCorrelation(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	requestContext, err := sharedtelemetry.NewPropagatedRequestContext(
		"11111111-1111-4111-8111-111111111111",
		sharedtelemetry.SystemActor{ServiceName: sharedtelemetry.ServiceTranscoder},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := sharedtelemetry.WithRequestContext(context.Background(), requestContext)
	slog.New(NewNormalizingHandler(slog.NewJSONHandler(&output, nil))).InfoContext(ctx, "correlated")
	if !bytes.Contains(output.Bytes(), []byte(`"request_id":"11111111-1111-4111-8111-111111111111"`)) {
		t.Fatalf("correlated entry = %s", output.String())
	}
}
