package logging

import (
	"context"
	"log/slog"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/muesli/termenv"
)

// LogLine is a single formatted log entry destined for the TUI panel.
type LogLine struct {
	Formatted string
}

// NewTUIHandler returns a slog.Handler that formats warn+ records with
// charmbracelet/log and non-blocking-sends each line onto ch. Records below
// minLevel are dropped at Enabled() so attribute closures are not evaluated
// for filtered records.
func NewTUIHandler(ch chan<- LogLine, minLevel slog.Level) slog.Handler {
	l := log.NewWithOptions(chanWriter{ch: ch}, log.Options{
		ReportTimestamp: true,
		TimeFormat:      "15:04:05",
		Level:           log.WarnLevel,
	})
	l.SetColorProfile(termenv.TrueColor)
	return filteredHandler{min: minLevel, inner: l}
}

// filteredHandler short-circuits at Enabled so slog skips attribute evaluation
// for records below min. Handle/WithAttrs/WithGroup delegate to inner.
type filteredHandler struct {
	min   slog.Level
	inner slog.Handler
}

func (h filteredHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.min
}

func (h filteredHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.inner.Handle(ctx, r)
}

func (h filteredHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return filteredHandler{min: h.min, inner: h.inner.WithAttrs(attrs)}
}

func (h filteredHandler) WithGroup(name string) slog.Handler {
	return filteredHandler{min: h.min, inner: h.inner.WithGroup(name)}
}

// chanWriter sends each Write as a LogLine on ch with a non-blocking send.
// When the channel is full the line is dropped silently — returning an error
// would loop back through the upstream logger as a write failure.
type chanWriter struct {
	ch chan<- LogLine
}

func (w chanWriter) Write(p []byte) (int, error) {
	line := LogLine{Formatted: strings.TrimRight(string(p), "\n")}
	select {
	case w.ch <- line:
	default:
	}
	return len(p), nil
}
