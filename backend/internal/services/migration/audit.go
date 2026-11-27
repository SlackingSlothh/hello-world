package migration

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type SlogAuditLogger struct {
	Logger *slog.Logger
}

func (l SlogAuditLogger) Log(ctx context.Context, event AuditEvent) {
	attrs := []slog.Attr{
		slog.String("actor", event.ActorUserID),
		slog.String("database_id", event.DatabaseID),
		slog.Int64("migration_id", event.ObjectID),
		slog.String("status", event.Status),
	}
	if event.ExecutionID != 0 {
		attrs = append(attrs,
			slog.Int64("execution_id", event.ExecutionID),
			slog.Int("attempt", event.AttemptNumber),
			slog.Int64("revision", event.Revision),
			slog.String("operation", string(event.Operation)),
			slog.Int64("duration_ms", event.DurationMs),
		)
	}
	if event.Version != nil {
		attrs = append(attrs, slog.Int64("version", *event.Version))
	}
	if event.SQL != "" {
		attrs = append(attrs, slog.String("sql", event.SQL))
	}
	if event.SQLState != "" {
		attrs = append(attrs, slog.String("sqlstate", event.SQLState))
	}
	level := slog.LevelInfo
	switch event.Status {
	case "FAILED", "ROLLBACK_ERROR":
		level = slog.LevelError
	case "UNCONFIRMED":
		level = slog.LevelWarn
	}
	l.Logger.LogAttrs(ctx, level, event.Action, attrs...)
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
	var sqlError interface{ SQLState() string }
	if errors.As(cause, &sqlError) {
		event.SQLState = sqlError.SQLState()
	}
	s.logAudit(ctx, event)
}
