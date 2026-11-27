package observability

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var w3cPropagator = propagation.TraceContext{}

func InjectTraceContext(ctx context.Context) ([]byte, error) {
	carrier := propagation.MapCarrier{}
	if ctx != nil {
		w3cPropagator.Inject(ctx, carrier)
	}
	return json.Marshal(carrier)
}

func ExtractTraceContext(ctx context.Context, raw []byte) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(raw) == 0 {
		return ctx
	}
	var carrier map[string]string
	if err := json.Unmarshal(raw, &carrier); err != nil || len(carrier) == 0 {
		return ctx
	}
	return w3cPropagator.Extract(ctx, propagation.MapCarrier(carrier))
}

func TraceIDs(ctx context.Context) (string, string) {
	if ctx == nil {
		return "", ""
	}
	sc := trace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return "", ""
	}
	return sc.TraceID().String(), sc.SpanID().String()
}
