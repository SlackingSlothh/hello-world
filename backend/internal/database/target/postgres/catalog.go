package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	dbops "hello-world/internal/models/catalog"
	"hello-world/internal/observability"
)

type Reader struct {
	DB       *sql.DB
	Logger   *slog.Logger
	Database string
}

func (r Reader) queryContext(ctx context.Context, op, query string, args ...any) (*sql.Rows, error) {
	ctx, span := observability.Tracer("datadock/postgres").Start(
		ctx,
		"db.query",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", op),
			attribute.String("database", r.Database),
		),
	)
	started := time.Now()
	if r.Logger != nil {
		r.Logger.LogAttrs(ctx, slog.LevelInfo, "db.query.start",
			slog.String("event", "db.query.start"),
			slog.String("database", r.Database),
			slog.String("query", observability.RedactSQL(query)),
			slog.Any("parameters", observability.RedactParams(args)),
			slog.String("status", "running"),
		)
	}
	rows, err := r.DB.QueryContext(ctx, query, args...)
	r.logQueryResult(ctx, span, started, query, args, -1, err)
	span.End()
	return rows, err
}

func (r Reader) logQueryResult(ctx context.Context, span trace.Span, started time.Time, query string, args []any, rowCount int, err error) {
	durationMs := time.Since(started).Milliseconds()
	attrs := []slog.Attr{
		slog.String("event", "db.query.result"),
		slog.String("database", r.Database),
		slog.String("query", observability.RedactSQL(query)),
		slog.Any("parameters", observability.RedactParams(args)),
		slog.Int64("duration_ms", durationMs),
	}
	if rowCount >= 0 {
		attrs = append(attrs, slog.Int("rows_affected", rowCount))
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		attrs = append(attrs, slog.String("status", "failed"), slog.String("error", err.Error()))
		var sqlErr interface{ SQLState() string }
		if errors.As(err, &sqlErr) {
			attrs = append(attrs, slog.String("sqlstate", sqlErr.SQLState()))
		}
		if r.Logger != nil {
			r.Logger.LogAttrs(ctx, slog.LevelError, "db.query.result", attrs...)
		}
		return
	}
	attrs = append(attrs, slog.String("status", "success"))
	if r.Logger != nil {
		r.Logger.LogAttrs(ctx, slog.LevelInfo, "db.query.result", attrs...)
	}
}

const internalSchemaName = "_platform_internal"

func isInternalSchema(schema string) bool {
	return strings.EqualFold(strings.TrimSpace(schema), internalSchemaName)
}

func (r Reader) Schemas(ctx context.Context) ([]dbops.Schema, error) {
	rows, err := r.queryContext(ctx, "schemas", `SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog','information_schema','_platform_internal') ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbops.Schema
	for rows.Next() {
		var v dbops.Schema
		if err := rows.Scan(&v.Name); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r Reader) Tables(ctx context.Context, schema string) ([]dbops.Table, error) {
	if isInternalSchema(schema) {
		return nil, dbops.ErrObjectNotFound
	}
	rows, err := r.queryContext(ctx, "tables", `SELECT table_schema,table_name,table_type FROM information_schema.tables WHERE table_schema=$1 ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbops.Table
	for rows.Next() {
		var v dbops.Table
		if err := rows.Scan(&v.Schema, &v.Name, &v.Type); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r Reader) Columns(ctx context.Context, schema, table string) ([]dbops.Column, error) {
	if isInternalSchema(schema) {
		return nil, dbops.ErrObjectNotFound
	}
	rows, err := r.queryContext(ctx, "columns", `SELECT column_name,ordinal_position,data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbops.Column
	for rows.Next() {
		var v dbops.Column
		var nullable string
		if err := rows.Scan(&v.Name, &v.Ordinal, &v.Type, &nullable, &v.Default); err != nil {
			return nil, err
		}
		v.Position = v.Ordinal
		v.Nullable = nullable == "YES"
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r Reader) Constraints(ctx context.Context, schema, table string) ([]dbops.Constraint, error) {
	if isInternalSchema(schema) {
		return nil, dbops.ErrObjectNotFound
	}
	rows, err := r.queryContext(ctx, "constraints", `SELECT tc.constraint_name,tc.constraint_type,COALESCE(string_agg(kcu.column_name, ',' ORDER BY kcu.ordinal_position), ''),COALESCE(MAX(pg_get_constraintdef(pc.oid)), '') FROM information_schema.table_constraints tc LEFT JOIN information_schema.key_column_usage kcu ON tc.constraint_catalog=kcu.constraint_catalog AND tc.constraint_schema=kcu.constraint_schema AND tc.constraint_name=kcu.constraint_name AND tc.table_name=kcu.table_name LEFT JOIN pg_constraint pc ON pc.conname=tc.constraint_name AND pc.conrelid=(SELECT c.oid FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=tc.table_schema AND c.relname=tc.table_name) WHERE tc.table_schema=$1 AND tc.table_name=$2 GROUP BY tc.constraint_name,tc.constraint_type ORDER BY tc.constraint_name`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbops.Constraint
	for rows.Next() {
		var v dbops.Constraint
		var cols string
		if err := rows.Scan(&v.Name, &v.Type, &cols, &v.Definition); err != nil {
			return nil, err
		}
		if cols != "" {
			v.Columns = strings.Split(cols, ",")
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r Reader) Indexes(ctx context.Context, schema, table string) ([]dbops.Index, error) {
	if isInternalSchema(schema) {
		return nil, dbops.ErrObjectNotFound
	}
	rows, err := r.queryContext(ctx, "indexes", `SELECT indexname,indexdef FROM pg_indexes WHERE schemaname=$1 AND tablename=$2 ORDER BY indexname`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbops.Index
	for rows.Next() {
		var v dbops.Index
		var def string
		if err := rows.Scan(&v.Name, &def); err != nil {
			return nil, err
		}
		v.Method = "btree"
		if i := strings.Index(strings.ToLower(def), " using "); i >= 0 {
			v.Method = strings.Fields(def[i+7:])[0]
		}
		v.Unique = strings.Contains(strings.ToUpper(def), "CREATE UNIQUE INDEX")
		v.Columns = parseIndexColumns(def)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r Reader) Rows(ctx context.Context, schema, table string, page dbops.PageRequest) (dbops.RowsPage, error) {
	if isInternalSchema(schema) {
		return dbops.RowsPage{}, dbops.ErrObjectNotFound
	}
	cols, err := r.Columns(ctx, schema, table)
	if err != nil {
		return dbops.RowsPage{}, err
	}
	if len(cols) == 0 {
		return dbops.RowsPage{}, sql.ErrNoRows
	}
	selected, err := selectColumns(cols, page.Columns)
	if err != nil {
		return dbops.RowsPage{}, err
	}
	q := fmt.Sprintf("SELECT %s FROM %s.%s", quoteList(selected), quote(schema), quote(table))
	filterNames := make([]string, 0, len(page.Filters))
	for name := range page.Filters {
		filterNames = append(filterNames, name)
	}
	sort.Strings(filterNames)
	args := make([]any, 0, len(filterNames)+2)
	for _, name := range filterNames {
		column, ok := columnName(cols, name)
		if !ok {
			return dbops.RowsPage{}, fmt.Errorf("%w: unknown filter column", dbops.ErrInvalidRequest)
		}
		args = append(args, page.Filters[name])
		if len(args) == 1 {
			q += " WHERE "
		} else {
			q += " AND "
		}
		q += fmt.Sprintf("%s = $%d", quote(column), len(args))
	}
	if page.Sort != "" {
		column, ok := columnName(cols, page.Sort)
		if !ok {
			return dbops.RowsPage{}, fmt.Errorf("%w: invalid sort column", dbops.ErrInvalidRequest)
		}
		q += " ORDER BY " + quote(column)
		if strings.EqualFold(page.Direction, "desc") {
			q += " DESC"
		} else {
			q += " ASC"
		}
	}
	args = append(args, page.Limit, page.Offset)
	q += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := r.queryContext(ctx, "rows", q, args...)
	if err != nil {
		return dbops.RowsPage{}, err
	}
	defer rows.Close()
	values, err := scanRows(rows)
	return dbops.RowsPage{Columns: selected, Rows: values, Limit: page.Limit, Offset: page.Offset}, err
}

func (r Reader) Select(ctx context.Context, req dbops.QueryRequest) (dbops.QueryResult, error) {
	ctx, span := observability.Tracer("datadock/postgres").Start(
		ctx,
		"db.query",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", "select"),
			attribute.String("database", r.Database),
		),
	)
	defer span.End()

	started := time.Now()
	if r.Logger != nil {
		r.Logger.LogAttrs(ctx, slog.LevelInfo, "db.query.start",
			slog.String("event", "db.query.start"),
			slog.String("database", r.Database),
			slog.String("query", observability.RedactSQL(req.SQL)),
			slog.Any("parameters", observability.RedactParams(req.Args)),
			slog.String("status", "running"),
		)
	}
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		r.logQueryResult(ctx, span, started, req.SQL, req.Args, 0, err)
		return dbops.QueryResult{}, err
	}
	defer func() {
		rbErr := tx.Rollback()
		if r.Logger != nil {
			status := "rolled_back"
			level := slog.LevelInfo
			attrs := []slog.Attr{
				slog.String("event", "db.tx.rollback"),
				slog.String("database", r.Database),
			}
			if rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				status = "rollback_error"
				level = slog.LevelError
				attrs = append(attrs, slog.String("error", rbErr.Error()))
			}
			attrs = append(attrs, slog.String("status", status))
			r.Logger.LogAttrs(ctx, level, "db.tx.rollback", attrs...)
		}
	}()
	rows, err := tx.QueryContext(ctx, req.SQL, req.Args...)
	if err != nil {
		r.logQueryResult(ctx, span, started, req.SQL, req.Args, 0, err)
		return dbops.QueryResult{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		r.logQueryResult(ctx, span, started, req.SQL, req.Args, 0, err)
		return dbops.QueryResult{}, err
	}
	values, err := scanLimited(rows, req.Limit)
	r.logQueryResult(ctx, span, started, req.SQL, req.Args, len(values), err)
	return dbops.QueryResult{Columns: cols, Rows: values}, err
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func quoteList(in []string) string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = quote(s)
	}
	return strings.Join(out, ",")
}
func columnName(cols []dbops.Column, name string) (string, bool) {
	for _, c := range cols {
		if c.Name == name {
			return c.Name, true
		}
	}
	return "", false
}
func selectColumns(cols []dbops.Column, requested []string) ([]string, error) {
	if len(requested) == 0 {
		out := make([]string, len(cols))
		for i, c := range cols {
			out[i] = c.Name
		}
		return out, nil
	}
	out := make([]string, len(requested))
	for i, name := range requested {
		column, ok := columnName(cols, name)
		if !ok {
			return nil, fmt.Errorf("%w: unknown column", dbops.ErrInvalidRequest)
		}
		out[i] = column
	}
	return out, nil
}
func scanRows(rows *sql.Rows) ([][]any, error) {
	return scanLimited(rows, 0)
}
func scanLimited(rows *sql.Rows, limit int) ([][]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	var out [][]any
	for rows.Next() {
		if limit > 0 && len(out) >= limit {
			break
		}
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		for i, v := range values {
			if b, ok := v.([]byte); ok && !binaryType(types[i].DatabaseTypeName()) {
				values[i] = string(b)
			}
		}
		out = append(out, values)
	}
	return out, rows.Err()
}

func binaryType(name string) bool {
	switch strings.ToUpper(name) {
	case "BYTEA", "BINARY", "VARBINARY", "BIT", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB":
		return true
	default:
		return false
	}
}
func parseIndexColumns(def string) []string {
	i := strings.Index(def, "(")
	j := strings.LastIndex(def, ")")
	if i < 0 || j <= i {
		return nil
	}
	raw := strings.Split(def[i+1:j], ",")
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		s = strings.Trim(s, `"`)
		if strings.ContainsAny(s, "() ") {
			continue
		}
		out = append(out, s)
	}
	return out
}
