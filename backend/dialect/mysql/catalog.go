package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"hello-world/dbops"
)

type Reader struct{ DB *sql.DB }

func (r Reader) Schemas(ctx context.Context) ([]dbops.Schema, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('information_schema','mysql','performance_schema','sys') ORDER BY schema_name`)
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
	rows, err := r.DB.QueryContext(ctx, `SELECT table_schema,table_name,table_type FROM information_schema.tables WHERE table_schema=? ORDER BY table_name`, schema)
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
	rows, err := r.DB.QueryContext(ctx, `SELECT column_name,ordinal_position,column_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`, schema, table)
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
	rows, err := r.DB.QueryContext(ctx, `SELECT tc.constraint_name,tc.constraint_type,GROUP_CONCAT(kcu.column_name ORDER BY kcu.ordinal_position SEPARATOR ',') FROM information_schema.table_constraints tc LEFT JOIN information_schema.key_column_usage kcu ON tc.constraint_schema=kcu.constraint_schema AND tc.constraint_name=kcu.constraint_name AND tc.table_name=kcu.table_name WHERE tc.table_schema=? AND tc.table_name=? GROUP BY tc.constraint_name,tc.constraint_type ORDER BY tc.constraint_name`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbops.Constraint
	for rows.Next() {
		var v dbops.Constraint
		var cols sql.NullString
		if err := rows.Scan(&v.Name, &v.Type, &cols); err != nil {
			return nil, err
		}
		if cols.Valid && cols.String != "" {
			v.Columns = strings.Split(cols.String, ",")
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r Reader) Indexes(ctx context.Context, schema, table string) ([]dbops.Index, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT index_name,non_unique,GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ','),index_type FROM information_schema.statistics WHERE table_schema=? AND table_name=? GROUP BY index_name,non_unique,index_type ORDER BY index_name`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbops.Index
	for rows.Next() {
		var v dbops.Index
		var nonUnique int
		var cols sql.NullString
		if err := rows.Scan(&v.Name, &nonUnique, &cols, &v.Method); err != nil {
			return nil, err
		}
		v.Unique = nonUnique == 0
		if cols.Valid {
			v.Columns = strings.Split(cols.String, ",")
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r Reader) Rows(ctx context.Context, schema, table string, page dbops.PageRequest) (dbops.RowsPage, error) {
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
		if !containsColumn(cols, name) {
			return dbops.RowsPage{}, fmt.Errorf("%w: unknown filter column", dbops.ErrInvalidRequest)
		}
		if len(args) == 0 {
			q += " WHERE "
		} else {
			q += " AND "
		}
		q += quote(name) + " = ?"
		args = append(args, page.Filters[name])
	}
	if page.Sort != "" {
		if !containsColumn(cols, page.Sort) {
			return dbops.RowsPage{}, fmt.Errorf("%w: invalid sort column", dbops.ErrInvalidRequest)
		}
		q += " ORDER BY " + quote(page.Sort)
		if strings.EqualFold(page.Direction, "desc") {
			q += " DESC"
		} else {
			q += " ASC"
		}
	}
	q += " LIMIT ? OFFSET ?"
	args = append(args, page.Limit, page.Offset)
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return dbops.RowsPage{}, err
	}
	defer rows.Close()
	values, err := scanRows(rows)
	return dbops.RowsPage{Columns: selected, Rows: values, Limit: page.Limit, Offset: page.Offset}, err
}
func (r Reader) Select(ctx context.Context, req dbops.QueryRequest) (dbops.QueryResult, error) {
	if err := dbops.ValidateSelect(req.SQL); err != nil {
		return dbops.QueryResult{}, err
	}
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return dbops.QueryResult{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, req.SQL, req.Args...)
	if err != nil {
		return dbops.QueryResult{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return dbops.QueryResult{}, err
	}
	values, err := scanLimited(rows, req.Limit)
	return dbops.QueryResult{Columns: cols, Rows: values}, err
}
func quote(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
func quoteList(in []string) string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = quote(s)
	}
	return strings.Join(out, ",")
}
func containsColumn(cols []dbops.Column, name string) bool {
	for _, c := range cols {
		if c.Name == name {
			return true
		}
	}
	return false
}
func selectColumns(cols []dbops.Column, requested []string) ([]string, error) {
	if len(requested) == 0 {
		out := make([]string, len(cols))
		for i, c := range cols {
			out[i] = c.Name
		}
		return out, nil
	}
	for _, name := range requested {
		if !containsColumn(cols, name) {
			return nil, fmt.Errorf("%w: unknown column", dbops.ErrInvalidRequest)
		}
	}
	return requested, nil
}
func scanRows(rows *sql.Rows) ([][]any, error) { return scanLimited(rows, 0) }
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
