package migration

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"hello-world/internal/observability"
)

type SlogAuditLogger struct {
	Logger *slog.Logger
}

func (l SlogAuditLogger) Log(ctx context.Context, event AuditEvent) {
	if l.Logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("event", event.Action),
		slog.String("actor", event.ActorUserID),
		slog.String("database", event.DatabaseID),
		slog.String("status", event.Status),
	}
	if event.ObjectID != 0 {
		attrs = append(attrs, slog.Int64("migration_id", event.ObjectID))
	}
	if event.JobID != "" {
		attrs = append(attrs, slog.String("job_id", event.JobID))
	}
	if event.ExecutionUUID != "" {
		attrs = append(attrs, slog.String("execution_id", event.ExecutionUUID))
	} else if event.ExecutionID != 0 {
		attrs = append(attrs, slog.Int64("execution_id", event.ExecutionID))
	}
	if event.AttemptNumber != 0 {
		attrs = append(attrs, slog.Int("attempt", event.AttemptNumber))
	}
	if event.Batch != 0 {
		attrs = append(attrs, slog.Int("batch", event.Batch))
	}
	if event.Revision != 0 {
		attrs = append(attrs, slog.Int64("revision", event.Revision))
	}
	if event.Operation != "" {
		attrs = append(attrs, slog.String("operation", string(event.Operation)))
	}
	if event.DurationMs > 0 || event.ExecutionID != 0 || event.ExecutionUUID != "" {
		attrs = append(attrs, slog.Int64("duration_ms", event.DurationMs))
	}
	if event.RowsAffected != nil {
		attrs = append(attrs, slog.Int64("rows_affected", *event.RowsAffected))
	}
	if event.Version != nil {
		attrs = append(attrs, slog.Int64("version", *event.Version))
	}
	if event.SQL != "" {
		attrs = append(attrs, slog.String("query", observability.RedactSQL(event.SQL)))
	}
	if len(event.Parameters) > 0 {
		attrs = append(attrs, slog.Any("parameters", observability.RedactParams(event.Parameters)))
	}
	if event.Error != "" {
		attrs = append(attrs, slog.String("error", observability.RedactString(event.Error)))
	}
	if event.SQLState != "" {
		attrs = append(attrs, slog.String("sqlstate", event.SQLState))
	}
	traceID, spanID := observability.TraceIDs(ctx)
	if traceID != "" {
		attrs = append(attrs, slog.String("trace_id", traceID))
	}
	if spanID != "" {
		attrs = append(attrs, slog.String("span_id", spanID))
	}
	level := slog.LevelInfo
	switch strings.ToUpper(event.Status) {
	case "FAILED", "ROLLBACK_ERROR", "ERROR":
		level = slog.LevelError
	case "UNCONFIRMED", "PARTIAL", "PARTIALLY_REVERTED", "UNCERTAIN", "DENIED":
		level = slog.LevelWarn
	}
	l.Logger.LogAttrs(ctx, level, event.Action, attrs...)
}

var auditSQLToken = regexp.MustCompile(`(?i)[a-z_][a-z0-9_$]*|[0-9][a-z0-9_.$]*|[^[:space:]]`)

func redactAuditSQL(query string, dialect TargetDialect) string {
	text := stripSQLCommentsAndStringsForDialect(query, dialect)
	return auditSQLToken.ReplaceAllStringFunc(text, func(token string) string {
		upper := strings.ToUpper(token)
		switch upper {
		case "SELECT", "INSERT", "UPDATE", "DELETE", "REPLACE", "WITH", "CREATE", "ALTER", "DROP", "TRUNCATE",
			"TABLE", "INDEX", "VIEW", "COLUMN", "TYPE", "SEQUENCE", "SCHEMA", "ADD", "RENAME", "CONSTRAINT",
			"INTO", "VALUES", "SET", "FROM", "WHERE", "JOIN", "ON", "AND", "OR", "NOT", "AS", "TO",
			"IS", "NULL", "DEFAULT", "PRIMARY", "KEY", "FOREIGN", "REFERENCES", "UNIQUE", "RETURNING", "IF", "EXISTS",
			"(", ")", ",", ";", ".", "=", "<", ">", "+", "-", "*", "/", "%":
			return upper
		default:
			return "?"
		}
	})
}

func (s *Service) logRun(ctx context.Context, m *Migration, run *MigrationRun, event AuditEvent, since time.Time, cause error) {
	if s.audit == nil {
		return
	}
	event.CreatedAt = s.nowFn()
	event.ActorUserID = run.ExecutedBy
	event.ObjectID = run.MigrationID
	event.ObjectType = "migration"
	event.DatabaseID = run.DatabaseID
	event.ExecutionID = run.ID
	event.Operation = run.Operation
	event.AttemptNumber = run.AttemptNumber
	event.Revision = run.Revision
	event.Version = m.Version
	event.DurationMs = max(0, event.CreatedAt.Sub(since).Milliseconds())
	if cause != nil {
		event.Error = cause.Error()
		var sqlError interface{ SQLState() string }
		if errors.As(cause, &sqlError) {
			event.SQLState = sqlError.SQLState()
		}
	}
	s.logAudit(ctx, event)
}
