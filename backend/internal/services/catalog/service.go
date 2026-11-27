package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"hello-world/internal/database/target/manageddb"
	dbops "hello-world/internal/models/catalog"
	"hello-world/internal/observability"
)

const InternalSchemaName = "_platform_internal"

func isInternalSchema(schema string) bool {
	return strings.EqualFold(strings.TrimSpace(schema), InternalSchemaName)
}

const DefaultLimit = 50
const MaxLimit = 500

type ReaderFactory func(*sql.DB) CatalogReader

type CatalogReader interface {
	Schemas(context.Context) ([]dbops.Schema, error)
	Tables(context.Context, string) ([]dbops.Table, error)
	Columns(context.Context, string, string) ([]dbops.Column, error)
	Constraints(context.Context, string, string) ([]dbops.Constraint, error)
	Indexes(context.Context, string, string) ([]dbops.Index, error)
	Rows(context.Context, string, string, dbops.PageRequest) (dbops.RowsPage, error)
	Select(context.Context, dbops.QueryRequest) (dbops.QueryResult, error)
}

type Service struct {
	Registry *manageddb.Registry
	Readers  map[string]ReaderFactory
	Logger   *slog.Logger
}

func (s Service) reader(id string) (CatalogReader, error) {
	meta, db, err := s.Registry.ConnectionDB(id)
	if err != nil {
		return nil, err
	}
	factory := s.Readers[meta.Driver]
	if factory == nil {
		return nil, fmt.Errorf("unsupported database driver %q", meta.Driver)
	}
	return factory(db), nil
}

func (s Service) startOp(ctx context.Context, databaseID, op string) (context.Context, trace.Span, time.Time) {
	ctx, span := observability.Tracer("datadock/catalog").Start(
		ctx,
		"db.query",
		trace.WithAttributes(
			attribute.String("db.operation", op),
			attribute.String("database", databaseID),
		),
	)
	return ctx, span, time.Now()
}

func (s Service) finishOp(ctx context.Context, span trace.Span, started time.Time, databaseID, op string, rowCount int, err error) {
	durationMs := time.Since(started).Milliseconds()
	status := "success"
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.String("event", "catalog."+op),
		slog.String("database", databaseID),
		slog.Int64("duration_ms", durationMs),
		slog.Int("rows_affected", rowCount),
	}
	if err != nil {
		status = "failed"
		level = slog.LevelError
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	attrs = append(attrs, slog.String("status", status))
	if s.Logger != nil {
		s.Logger.LogAttrs(ctx, level, "catalog."+op, attrs...)
	}
	span.End()
}

func (s Service) Schemas(ctx context.Context, id string) ([]dbops.Schema, error) {
	ctx, span, started := s.startOp(ctx, id, "schemas")
	r, e := s.reader(id)
	if e != nil {
		s.finishOp(ctx, span, started, id, "schemas", 0, e)
		return nil, e
	}
	out, e := r.Schemas(ctx)
	if e == nil {
		filtered := make([]dbops.Schema, 0, len(out))
		for _, item := range out {
			if !isInternalSchema(item.Name) {
				filtered = append(filtered, item)
			}
		}
		out = filtered
	}
	s.finishOp(ctx, span, started, id, "schemas", len(out), e)
	return out, e
}
func (s Service) Tables(ctx context.Context, id, schema string) ([]dbops.Table, error) {
	if isInternalSchema(schema) {
		return nil, dbops.ErrObjectNotFound
	}
	ctx, span, started := s.startOp(ctx, id, "tables")
	r, e := s.reader(id)
	if e != nil {
		s.finishOp(ctx, span, started, id, "tables", 0, e)
		return nil, e
	}
	out, e := r.Tables(ctx, schema)
	s.finishOp(ctx, span, started, id, "tables", len(out), e)
	return out, e
}
func (s Service) tableReader(ctx context.Context, id, schema, table string) (CatalogReader, error) {
	if isInternalSchema(schema) {
		return nil, dbops.ErrObjectNotFound
	}
	r, err := s.reader(id)
	if err != nil {
		return nil, err
	}
	tables, err := r.Tables(ctx, schema)
	if err != nil {
		return nil, err
	}
	for _, item := range tables {
		if item.Name == table {
			return r, nil
		}
	}
	return nil, dbops.ErrObjectNotFound
}
func (s Service) Columns(ctx context.Context, id, schema, table string) ([]dbops.Column, error) {
	ctx, span, started := s.startOp(ctx, id, "columns")
	r, e := s.tableReader(ctx, id, schema, table)
	if e != nil {
		s.finishOp(ctx, span, started, id, "columns", 0, e)
		return nil, e
	}
	out, e := r.Columns(ctx, schema, table)
	s.finishOp(ctx, span, started, id, "columns", len(out), e)
	return out, e
}
func (s Service) Constraints(ctx context.Context, id, schema, table string) ([]dbops.Constraint, error) {
	ctx, span, started := s.startOp(ctx, id, "constraints")
	r, e := s.tableReader(ctx, id, schema, table)
	if e != nil {
		s.finishOp(ctx, span, started, id, "constraints", 0, e)
		return nil, e
	}
	out, e := r.Constraints(ctx, schema, table)
	s.finishOp(ctx, span, started, id, "constraints", len(out), e)
	return out, e
}
func (s Service) Indexes(ctx context.Context, id, schema, table string) ([]dbops.Index, error) {
	ctx, span, started := s.startOp(ctx, id, "indexes")
	r, e := s.tableReader(ctx, id, schema, table)
	if e != nil {
		s.finishOp(ctx, span, started, id, "indexes", 0, e)
		return nil, e
	}
	out, e := r.Indexes(ctx, schema, table)
	s.finishOp(ctx, span, started, id, "indexes", len(out), e)
	return out, e
}
func (s Service) Rows(ctx context.Context, id, schema, table string, p dbops.PageRequest) (dbops.RowsPage, error) {
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	if p.Limit < 1 || p.Limit > MaxLimit {
		return dbops.RowsPage{}, fmt.Errorf("%w: limit must be between 1 and 500", dbops.ErrInvalidRequest)
	}
	if p.Offset < 0 {
		return dbops.RowsPage{}, fmt.Errorf("%w: offset cannot be negative", dbops.ErrInvalidRequest)
	}
	if p.Direction != "" && p.Direction != "asc" && p.Direction != "desc" {
		return dbops.RowsPage{}, fmt.Errorf("%w: direction must be asc or desc", dbops.ErrInvalidRequest)
	}
	ctx, span, started := s.startOp(ctx, id, "rows")
	r, e := s.tableReader(ctx, id, schema, table)
	if e != nil {
		s.finishOp(ctx, span, started, id, "rows", 0, e)
		return dbops.RowsPage{}, e
	}
	out, e := r.Rows(ctx, schema, table, p)
	s.finishOp(ctx, span, started, id, "rows", len(out.Rows), e)
	return out, e
}
func (s Service) Select(ctx context.Context, id string, q dbops.QueryRequest) (dbops.QueryResult, error) {
	if q.Limit == 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit < 1 || q.Limit > MaxLimit {
		return dbops.QueryResult{}, fmt.Errorf("%w: limit must be between 1 and 500", dbops.ErrInvalidRequest)
	}
	r, e := s.reader(id)
	if e != nil {
		return dbops.QueryResult{}, e
	}
	if err := ValidateSelect(q.SQL); err != nil {
		return dbops.QueryResult{}, err
	}
	ctx, span, started := s.startOp(ctx, id, "select")
	if s.Logger != nil {
		s.Logger.LogAttrs(ctx, slog.LevelInfo, "db.query.start",
			slog.String("event", "db.query.start"),
			slog.String("database", id),
			slog.String("query", observability.RedactSQL(q.SQL)),
			slog.Any("parameters", observability.RedactParams(q.Args)),
			slog.String("status", "running"),
		)
	}
	out, e := r.Select(ctx, q)
	s.finishOp(ctx, span, started, id, "select", len(out.Rows), e)
	return out, e
}
