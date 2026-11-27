package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestInjectAndExtractTraceContextRoundTrip(t *testing.T) {
	var out bytes.Buffer
	telemetry, err := InitTelemetry(context.Background(), TelemetryConfig{
		Exporter:      "console",
		ServiceName:   "datadock-test",
		ConsoleWriter: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := telemetry.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}()

	tracer := Tracer("queue-test")
	parentCtx, parentSpan := tracer.Start(context.Background(), "queue.publish")
	parentTraceID, parentSpanID := TraceIDs(parentCtx)
	if parentTraceID == "" || parentSpanID == "" {
		t.Fatal("expected valid parent trace and span IDs")
	}

	raw, err := InjectTraceContext(parentCtx)
	if err != nil {
		t.Fatal(err)
	}
	var carrier map[string]string
	if err := json.Unmarshal(raw, &carrier); err != nil {
		t.Fatal(err)
	}
	if carrier["traceparent"] == "" {
		t.Fatalf("expected traceparent in carrier, got %s", string(raw))
	}
	parentSpan.End()

	extractedCtx := ExtractTraceContext(context.Background(), raw)
	childCtx, childSpan := tracer.Start(extractedCtx, "worker.process_job")
	childTraceID, childSpanID := TraceIDs(childCtx)
	childSpan.End()

	if childTraceID != parentTraceID {
		t.Fatalf("expected matching trace_id %s, got %s", parentTraceID, childTraceID)
	}
	if childSpanID == "" || childSpanID == parentSpanID {
		t.Fatalf("expected distinct child span_id, parent=%s child=%s", parentSpanID, childSpanID)
	}
	if !strings.Contains(out.String(), "queue.publish") || !strings.Contains(out.String(), "worker.process_job") {
		t.Fatalf("expected exported spans in console output, got: %s", out.String())
	}
}

func TestExtractTraceContextHandlesEmptyAndInvalidJSON(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(""), []byte("{}"), []byte("not-json")} {
		ctx := ExtractTraceContext(context.Background(), raw)
		traceID, spanID := TraceIDs(ctx)
		if traceID != "" || spanID != "" {
			t.Fatalf("expected empty trace IDs for input %q, got %s/%s", string(raw), traceID, spanID)
		}
	}
}

func TestInitTelemetryModes(t *testing.T) {
	telemetry, err := InitTelemetry(context.Background(), TelemetryConfig{
		Exporter:    "none",
		ServiceName: "datadock-none",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := telemetry.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}()
	ctx, span := Tracer("none-test").Start(context.Background(), "noop.op")
	traceID, spanID := TraceIDs(ctx)
	span.End()
	if traceID == "" || spanID == "" {
		t.Fatal("expected valid trace and span IDs even in none exporter mode")
	}

	if _, err := InitTelemetry(context.Background(), TelemetryConfig{Exporter: "unknown"}); err == nil {
		t.Fatal("expected error for unsupported exporter")
	}
}
