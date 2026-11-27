package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

type TelemetryConfig struct {
	Exporter      string
	OTLPEndpoint  string
	ServiceName   string
	ConsoleWriter io.Writer
}

type Telemetry struct {
	TracerProvider *sdktrace.TracerProvider
	LoggerProvider *sdklog.LoggerProvider
	Shutdown       func(context.Context) error
}

func InitTelemetry(ctx context.Context, cfg TelemetryConfig) (*Telemetry, error) {
	serviceName := strings.TrimSpace(cfg.ServiceName)
	if serviceName == "" {
		serviceName = "datadock"
	}
	exporterMode := strings.ToLower(strings.TrimSpace(cfg.Exporter))
	if exporterMode == "" {
		exporterMode = "none"
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
	)

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	var tp *sdktrace.TracerProvider
	var lp *sdklog.LoggerProvider

	switch exporterMode {
	case "none":
		tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
	case "console":
		writer := cfg.ConsoleWriter
		if writer == nil {
			writer = os.Stdout
		}
		traceExp, err := stdouttrace.New(stdouttrace.WithWriter(writer))
		if err != nil {
			return nil, fmt.Errorf("create console trace exporter: %w", err)
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithSyncer(traceExp),
			sdktrace.WithResource(res),
		)
	case "otlp":
		endpoint := strings.TrimSpace(cfg.OTLPEndpoint)
		if endpoint == "" {
			endpoint = "otel-collector:4317"
		}
		traceExp, err := otlptracegrpc.New(
			ctx,
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("create otlp trace exporter: %w", err)
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExp),
			sdktrace.WithResource(res),
		)

		logExp, err := otlploggrpc.New(
			ctx,
			otlploggrpc.WithEndpoint(endpoint),
			otlploggrpc.WithInsecure(),
		)
		if err != nil {
			_ = tp.Shutdown(ctx)
			return nil, fmt.Errorf("create otlp log exporter: %w", err)
		}
		lp = sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
			sdklog.WithResource(res),
		)
		global.SetLoggerProvider(lp)
	default:
		return nil, fmt.Errorf("unsupported otel exporter %q", cfg.Exporter)
	}

	otel.SetTracerProvider(tp)

	shutdown := func(shutdownCtx context.Context) error {
		var errs []error
		if tp != nil {
			if err := tp.Shutdown(shutdownCtx); err != nil {
				errs = append(errs, err)
			}
		}
		if lp != nil {
			if err := lp.Shutdown(shutdownCtx); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	return &Telemetry{
		TracerProvider: tp,
		LoggerProvider: lp,
		Shutdown:       shutdown,
	}, nil
}

func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
