package observability

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

type LoggerOptions struct {
	Writer       io.Writer
	Level        slog.Leveler
	OTelProvider otellog.LoggerProvider
	ServiceName  string
	ExtraHandler slog.Handler
}

func NewLogger(opts LoggerOptions) *slog.Logger {
	writer := opts.Writer
	if writer == nil {
		writer = os.Stdout
	}
	level := opts.Level
	if level == nil {
		level = slog.LevelInfo
	}
	serviceName := opts.ServiceName
	if serviceName == "" {
		serviceName = "datadock"
	}

	jsonHandler := slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			return RedactAttr(a)
		},
	})

	handlers := []slog.Handler{jsonHandler}
	if opts.OTelProvider != nil {
		handlers = append(handlers, otelslog.NewHandler(
			serviceName,
			otelslog.WithLoggerProvider(opts.OTelProvider),
		))
	}
	if opts.ExtraHandler != nil {
		handlers = append(handlers, opts.ExtraHandler)
	}

	return slog.New(&contextHandler{
		next: NewFanoutHandler(handlers...),
	})
}

func NewFanoutHandler(handlers ...slog.Handler) slog.Handler {
	filtered := make([]slog.Handler, 0, len(handlers))
	for _, h := range handlers {
		if h != nil {
			filtered = append(filtered, h)
		}
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return &fanoutHandler{handlers: filtered}
}

type fanoutHandler struct {
	handlers []slog.Handler
}

func (f *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f *fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, record.Level) {
			if err := h.Handle(ctx, record.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: next}
}

func (f *fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithGroup(name)
	}
	return &fanoutHandler{handlers: next}
}

type contextHandler struct {
	next slog.Handler
}

func (c *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return c.next.Enabled(ctx, level)
}

func (c *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	sanitized := slog.NewRecord(record.Time, record.Level, RedactString(record.Message), record.PC)
	hasTraceID := false
	hasSpanID := false
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "trace_id" {
			hasTraceID = true
		}
		if a.Key == "span_id" {
			hasSpanID = true
		}
		sanitized.AddAttrs(RedactAttr(a))
		return true
	})
	if ctx != nil {
		sc := trace.SpanFromContext(ctx).SpanContext()
		if sc.IsValid() {
			if !hasTraceID {
				sanitized.AddAttrs(slog.String("trace_id", sc.TraceID().String()))
			}
			if !hasSpanID {
				sanitized.AddAttrs(slog.String("span_id", sc.SpanID().String()))
			}
		}
	}
	return c.next.Handle(ctx, sanitized)
}

func (c *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sanitized := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		sanitized[i] = RedactAttr(a)
	}
	return &contextHandler{next: c.next.WithAttrs(sanitized)}
}

func (c *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: c.next.WithGroup(name)}
}
