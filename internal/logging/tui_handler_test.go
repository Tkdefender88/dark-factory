package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func newWarnRecord(msg string) slog.Record {
	return slog.NewRecord(time.Now(), slog.LevelWarn, msg, 0)
}

func TestTUIHandlerWarnDelivered(t *testing.T) {
	ch := make(chan LogLine, 4)
	h := NewTUIHandler(ch, slog.LevelWarn)

	if err := h.Handle(context.Background(), newWarnRecord("warn-msg-text")); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	select {
	case line := <-ch:
		if line.Formatted == "" {
			t.Error("expected non-empty Formatted")
		}
		if !strings.Contains(line.Formatted, "warn-msg-text") {
			t.Errorf("expected Formatted to contain message, got %q", line.Formatted)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log line")
	}
}

func TestTUIHandlerInfoFiltered(t *testing.T) {
	ch := make(chan LogLine, 4)
	h := NewTUIHandler(ch, slog.LevelWarn)

	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled returned true for Info with minLevel=Warn")
	}

	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "info-msg", 0)
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	select {
	case line := <-ch:
		t.Errorf("expected no line for filtered Info, got %q", line.Formatted)
	default:
	}
}

func TestTUIHandlerChannelFullDrops(t *testing.T) {
	ch := make(chan LogLine, 1)
	h := NewTUIHandler(ch, slog.LevelWarn)

	done := make(chan struct{})
	go func() {
		_ = h.Handle(context.Background(), newWarnRecord("first"))
		_ = h.Handle(context.Background(), newWarnRecord("second"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Handle blocked when channel was full")
	}

	if got := len(ch); got != 1 {
		t.Errorf("expected exactly 1 line in channel, got %d", got)
	}
}

func TestTUIHandlerFormattedContainsLevel(t *testing.T) {
	ch := make(chan LogLine, 4)
	h := NewTUIHandler(ch, slog.LevelWarn)

	if err := h.Handle(context.Background(), newWarnRecord("hello")); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	select {
	case line := <-ch:
		if !strings.Contains(line.Formatted, "WARN") {
			t.Errorf("expected Formatted to contain %q, got %q", "WARN", line.Formatted)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log line")
	}
}

func TestMultiHandlerFanOut(t *testing.T) {
	var bufA, bufB bytes.Buffer
	a := slog.NewTextHandler(&bufA, &slog.HandlerOptions{Level: slog.LevelDebug})
	b := slog.NewTextHandler(&bufB, &slog.HandlerOptions{Level: slog.LevelDebug})
	mh := &multiHandler{handlers: []slog.Handler{a, b}}

	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "fanout-msg", 0)
	if err := mh.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	if !strings.Contains(bufA.String(), "fanout-msg") {
		t.Errorf("buffer A missing message: %q", bufA.String())
	}
	if !strings.Contains(bufB.String(), "fanout-msg") {
		t.Errorf("buffer B missing message: %q", bufB.String())
	}
	if bufA.String() != bufB.String() {
		t.Errorf("buffers diverged:\nA: %q\nB: %q", bufA.String(), bufB.String())
	}
}

func TestMultiHandlerEnabledShortCircuits(t *testing.T) {
	var bufA, bufB bytes.Buffer
	disabled := slog.NewTextHandler(&bufA, &slog.HandlerOptions{Level: slog.LevelError})
	enabled := slog.NewTextHandler(&bufB, &slog.HandlerOptions{Level: slog.LevelDebug})
	mh := &multiHandler{handlers: []slog.Handler{disabled, enabled}}

	if !mh.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled returned false when at least one handler accepts Info")
	}
}

func TestWithHandlerComposes(t *testing.T) {
	var bufA, bufB bytes.Buffer
	a := slog.NewTextHandler(&bufA, &slog.HandlerOptions{Level: slog.LevelDebug})
	b := slog.NewTextHandler(&bufB, &slog.HandlerOptions{Level: slog.LevelDebug})

	h := WithHandler(a, b)
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "compose-msg", 0)
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	if !strings.Contains(bufA.String(), "compose-msg") {
		t.Errorf("base handler missing message: %q", bufA.String())
	}
	if !strings.Contains(bufB.String(), "compose-msg") {
		t.Errorf("extra handler missing message: %q", bufB.String())
	}
}
