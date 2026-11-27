package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"hello-world/internal/observability"
)

func TestSlogAuditLoggerStructuredAttributesAndTraceCorrelation(t *testing.T) {
	telemetry, err := observability.InitTelemetry(context.Background(), observability.TelemetryConfig{
		Exporter:    "none",
		ServiceName: "datadock-audit-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = telemetry.Shutdown(context.Background()) }()

	var buf bytes.Buffer
	logger := observability.NewLogger(observability.LoggerOptions{
		Writer:      &buf,
		ServiceName: "datadock-audit-test",
	})
	audit := SlogAuditLogger{Logger: logger}

	ctx, span := observability.Tracer("audit-test").Start(context.Background(), "batch.execute")
	defer span.End()

	rows := int64(500)
	audit.Log(ctx, AuditEvent{
		ActorUserID:   "user-1",
		Action:        "batch.execute",
		ObjectID:      12,
		DatabaseID:    "bioskop",
		Status:        "FAILED",
		JobID:         "job-uuid-1",
		ExecutionUUID: "exec-uuid-1",
		AttemptNumber: 2,
		Batch:         3,
		DurationMs:    45,
		RowsAffected:  &rows,
		SQL:           "UPDATE users SET password = 'plain-secret' WHERE id > $1",
		Parameters:    []any{"postgres://admin:pw@db:5432/app", 100},
		Error:         "connection postgres://admin:pw@db:5432/app reset",
		SQLState:      "40P01",
	})

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid json: %v (%s)", err, buf.String())
	}
	if entry["event"] != "batch.execute" || entry["actor"] != "user-1" || entry["database"] != "bioskop" || entry["sqlstate"] != "40P01" {
		t.Fatalf("missing required attributes: %+v", entry)
	}
	if entry["job_id"] != "job-uuid-1" || entry["execution_id"] != "exec-uuid-1" {
		t.Fatalf("missing job/execution correlation: %+v", entry)
	}
	if entry["trace_id"] == "" || entry["span_id"] == "" {
		t.Fatalf("missing trace_id/span_id: %+v", entry)
	}
	if strings.Contains(buf.String(), "plain-secret") || strings.Contains(buf.String(), ":pw@") {
		t.Fatalf("sensitive value leaked in audit log: %s", buf.String())
	}
}

func TestValidateTransactionalSQLRejectsInternalSchema(t *testing.T) {
	blocked := []string{
		"DELETE FROM _platform_internal.migration_checkpoints;",
		`DROP SCHEMA "_platform_internal" CASCADE;`,
		"UPDATE _PLATFORM_INTERNAL.migration_checkpoints SET rows_affected = 0;",
		"ALTER TABLE _platform_internal.migration_checkpoints ADD COLUMN x INT;",
	}
	for _, query := range blocked {
		if err := ValidateTransactionalSQL(query); err == nil {
			t.Fatalf("expected internal schema SQL to be rejected: %s", query)
		}
	}
	if err := ValidateTransactionalSQL("UPDATE notes SET label = '_platform_internal' WHERE id = 1;"); err != nil {
		t.Fatalf("string literal should be allowed, got: %v", err)
	}
}
