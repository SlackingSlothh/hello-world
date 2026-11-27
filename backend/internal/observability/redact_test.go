package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestRedactDSN(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "postgres uri",
			in:   "postgres://admin:supersecret@db:5432/hello_world?sslmode=disable",
			want: "postgres://admin:[REDACTED]@db:5432/hello_world?sslmode=disable",
		},
		{
			name: "mysql tcp dsn",
			in:   "root:my_secret_pw@tcp(127.0.0.1:3306)/appdb",
			want: "root:[REDACTED]@tcp(127.0.0.1:3306)/appdb",
		},
		{
			name: "libpq kv dsn",
			in:   "host=localhost user=hello password=topsecret dbname=app",
			want: "host=localhost user=hello password=[REDACTED] dbname=app",
		},
		{
			name: "no password",
			in:   "postgres://admin@db:5432/hello_world",
			want: "postgres://admin@db:5432/hello_world",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactDSN(tc.in); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRedactStringAndHeaders(t *testing.T) {
	in := "Authorization: Bearer abc.def.ghi and session_id=opaque-secret-123"
	got := RedactString(in)
	if strings.Contains(got, "abc.def.ghi") || strings.Contains(got, "opaque-secret-123") {
		t.Fatalf("sensitive values leaked: %q", got)
	}

	headers := http.Header{}
	headers.Set("Cookie", "session_id=xyz123")
	headers.Set("Authorization", "Bearer tok_999")
	headers.Set("Content-Type", "application/json")
	redacted := RedactHeaders(headers)
	if redacted["Cookie"] != RedactedValue || redacted["Authorization"] != RedactedValue {
		t.Fatalf("expected sensitive headers redacted: %+v", redacted)
	}
	if redacted["Content-Type"] != "application/json" {
		t.Fatalf("expected safe header preserved: %+v", redacted)
	}
}

func TestRedactSQLAndParams(t *testing.T) {
	sqlIn := "UPDATE platform_users SET password_hash = 'argon2_secret_value' WHERE id = $1"
	gotSQL := RedactSQL(sqlIn)
	if strings.Contains(gotSQL, "argon2_secret_value") {
		t.Fatalf("SQL literal leaked: %q", gotSQL)
	}
	if !strings.Contains(gotSQL, "UPDATE platform_users SET password_hash = '[REDACTED]'") {
		t.Fatalf("unexpected redacted SQL: %q", gotSQL)
	}

	safeSQL := "SELECT id, title FROM movies WHERE id > $1 ORDER BY id LIMIT $2"
	if got := RedactSQL(safeSQL); got != safeSQL {
		t.Fatalf("safe SQL modified: got %q, want %q", got, safeSQL)
	}

	named := RedactNamedParams(map[string]any{
		"password": "plain",
		"dsn":      "postgres://u:p@host/db",
		"limit":    50,
	})
	if named["password"] != RedactedValue || named["dsn"] != RedactedValue || named["limit"] != 50 {
		t.Fatalf("unexpected named params redaction: %+v", named)
	}

	positional := RedactParams([]any{"postgres://u:p@host/db", 42})
	if strings.Contains(positional[0].(string), ":p@") || positional[1] != 42 {
		t.Fatalf("unexpected positional params redaction: %+v", positional)
	}
}

type captureHandler struct {
	records []slog.Record
}

func (c *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (c *captureHandler) Handle(_ context.Context, r slog.Record) error {
	c.records = append(c.records, r.Clone())
	return nil
}
func (c *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *captureHandler) WithGroup(string) slog.Handler      { return c }

func TestNewLoggerRedactionTraceCorrelationAndFanout(t *testing.T) {
	var buf bytes.Buffer
	secondary := &captureHandler{}
	logger := NewLogger(LoggerOptions{
		Writer:       &buf,
		Level:        slog.LevelDebug,
		ExtraHandler: secondary,
		ServiceName:  "datadock-test",
	})

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	logger.InfoContext(ctx, "db.query.start",
		slog.String("event", "db.query"),
		slog.String("actor", "alice"),
		slog.String("password", "secret123"),
		slog.String("session_id", "sess_opaque_999"),
		slog.Any("error", errors.New("connect postgres://u:pw@db:5432/app failed")),
		slog.Group("meta", slog.String("dsn", "postgres://a:b@c/d"), slog.String("database", "bioskop")),
	)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid json log: %v (%s)", err, buf.String())
	}
	if entry["trace_id"] != traceID.String() || entry["span_id"] != spanID.String() {
		t.Fatalf("missing trace correlation: %+v", entry)
	}
	if entry["password"] != RedactedValue || entry["session_id"] != RedactedValue {
		t.Fatalf("sensitive fields not redacted: %+v", entry)
	}
	if strings.Contains(buf.String(), "secret123") || strings.Contains(buf.String(), "sess_opaque_999") || strings.Contains(buf.String(), ":pw@") || strings.Contains(buf.String(), ":b@") {
		t.Fatalf("sensitive string leaked into JSON log: %s", buf.String())
	}
	meta, ok := entry["meta"].(map[string]any)
	if !ok || meta["dsn"] != RedactedValue || meta["database"] != "bioskop" {
		t.Fatalf("unexpected group redaction: %+v", entry["meta"])
	}
	if len(secondary.records) != 1 {
		t.Fatalf("expected fanout handler to receive 1 record, got %d", len(secondary.records))
	}
}
