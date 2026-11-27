package server

import (
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"hello-world/internal/observability"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func WithObservability(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/metrics" {
			next.ServeHTTP(writer, request)
			return
		}
		ctx := otel.GetTextMapPropagator().Extract(request.Context(), propagation.HeaderCarrier(request.Header))
		spanName := request.Method + " " + request.URL.Path
		ctx, span := observability.Tracer("datadock/http").Start(
			ctx,
			spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", request.Method),
				attribute.String("http.path", request.URL.Path),
			),
		)
		defer span.End()

		recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
		started := time.Now()
		next.ServeHTTP(recorder, request.WithContext(ctx))
		durationMs := time.Since(started).Milliseconds()

		span.SetAttributes(attribute.Int("http.status_code", recorder.status))
		statusLabel := "success"
		level := slog.LevelInfo
		if recorder.status >= http.StatusInternalServerError {
			statusLabel = "error"
			level = slog.LevelError
			span.SetStatus(codes.Error, http.StatusText(recorder.status))
		} else if recorder.status >= http.StatusBadRequest {
			statusLabel = "failed"
			level = slog.LevelWarn
		}

		if logger != nil {
			logger.LogAttrs(ctx, level, "http.request",
				slog.String("event", "http.request"),
				slog.String("method", request.Method),
				slog.String("path", request.URL.Path),
				slog.Int("status_code", recorder.status),
				slog.String("status", statusLabel),
				slog.Int64("duration_ms", durationMs),
			)
		}
	})
}
