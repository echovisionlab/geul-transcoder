package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type recordingSink struct {
	entries        []recordingEntry
	withAttrsCalls int
	withGroupCalls int
}

type recordingEntry struct {
	message string
	attrs   []slog.Attr
	group   string
}

type recordingHandler struct {
	sink     *recordingSink
	minLevel slog.Level
	attrs    []slog.Attr
	group    string
	err      error
}

func (h recordingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

func (h recordingHandler) Handle(_ context.Context, record slog.Record) error {
	if h.err != nil {
		return h.err
	}
	entry := recordingEntry{
		message: record.Message,
		attrs:   append([]slog.Attr(nil), h.attrs...),
		group:   h.group,
	}
	record.Attrs(func(attr slog.Attr) bool {
		entry.attrs = append(entry.attrs, attr)
		return true
	})
	h.sink.entries = append(h.sink.entries, entry)
	return nil
}

func (h recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.sink.withAttrsCalls++
	h.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return h
}

func (h recordingHandler) WithGroup(name string) slog.Handler {
	h.sink.withGroupCalls++
	h.group = name
	return h
}

func TestFanoutHandlerRoutesOnlyEnabledHandlers(t *testing.T) {
	t.Parallel()

	infoSink := &recordingSink{}
	errorSink := &recordingSink{}
	handler := NewFanoutHandler(
		recordingHandler{sink: errorSink, minLevel: slog.LevelError},
		recordingHandler{sink: infoSink, minLevel: slog.LevelInfo},
	)

	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("expected fanout to be enabled")
	}
	if err := handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "processed", 0)); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if len(errorSink.entries) != 0 {
		t.Fatalf("error sink should not receive info records: %#v", errorSink.entries)
	}
	if len(infoSink.entries) != 1 || infoSink.entries[0].message != "processed" {
		t.Fatalf("info sink entries = %#v", infoSink.entries)
	}
}

func TestFanoutHandlerDisabledWhenNoChildAcceptsLevel(t *testing.T) {
	t.Parallel()
	handler := NewFanoutHandler(recordingHandler{sink: &recordingSink{}, minLevel: slog.LevelError})
	if handler.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("fanout should be disabled")
	}
}

func TestFanoutHandlerReturnsFirstChildError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("handler failed")
	first := &recordingSink{}
	second := &recordingSink{}
	handler := NewFanoutHandler(
		recordingHandler{sink: first, minLevel: slog.LevelDebug, err: wantErr},
		recordingHandler{sink: second, minLevel: slog.LevelDebug},
	)

	err := handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "processed", 0))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle error = %v, want %v", err, wantErr)
	}
	if len(second.entries) != 0 {
		t.Fatalf("handler after error should not be called: %#v", second.entries)
	}
}

func TestFanoutHandlerAppliesAttrsAndGroupsToChildren(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	handler := NewFanoutHandler(recordingHandler{sink: sink, minLevel: slog.LevelDebug}).
		WithAttrs([]slog.Attr{slog.String("component", "transcoder")}).
		WithGroup("job")

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "processed", 0)
	record.AddAttrs(slog.String("job_id", "job-1"))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	if sink.withAttrsCalls != 1 || sink.withGroupCalls != 1 {
		t.Fatalf("expected WithAttrs/WithGroup calls, got attrs=%d groups=%d", sink.withAttrsCalls, sink.withGroupCalls)
	}
	if len(sink.entries) != 1 {
		t.Fatalf("entries = %#v", sink.entries)
	}
	entry := sink.entries[0]
	if entry.group != "job" {
		t.Fatalf("group = %q", entry.group)
	}
	if len(entry.attrs) != 2 ||
		entry.attrs[0].Key != "component" ||
		entry.attrs[1].Key != "job_id" {
		t.Fatalf("attrs = %#v", entry.attrs)
	}
}
