package catalog

import (
	"context"
	"database/sql"
	"fmt"

	"hello-world/dbops"
	"hello-world/manageddb"
)

const DefaultLimit = 50
const MaxLimit = 500

type ReaderFactory func(*sql.DB) dbops.CatalogReader

type Service struct {
	Registry *manageddb.Registry
	Readers  map[string]ReaderFactory
}

func (s Service) reader(id string) (dbops.CatalogReader, error) {
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
func (s Service) Schemas(ctx context.Context, id string) ([]dbops.Schema, error) {
	r, e := s.reader(id)
	if e != nil {
		return nil, e
	}
	return r.Schemas(ctx)
}
func (s Service) Tables(ctx context.Context, id, schema string) ([]dbops.Table, error) {
	r, e := s.reader(id)
	if e != nil {
		return nil, e
	}
	return r.Tables(ctx, schema)
}
func (s Service) tableReader(ctx context.Context, id, schema, table string) (dbops.CatalogReader, error) {
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
	r, e := s.tableReader(ctx, id, schema, table)
	if e != nil {
		return nil, e
	}
	return r.Columns(ctx, schema, table)
}
func (s Service) Constraints(ctx context.Context, id, schema, table string) ([]dbops.Constraint, error) {
	r, e := s.tableReader(ctx, id, schema, table)
	if e != nil {
		return nil, e
	}
	return r.Constraints(ctx, schema, table)
}
func (s Service) Indexes(ctx context.Context, id, schema, table string) ([]dbops.Index, error) {
	r, e := s.tableReader(ctx, id, schema, table)
	if e != nil {
		return nil, e
	}
	return r.Indexes(ctx, schema, table)
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
	r, e := s.tableReader(ctx, id, schema, table)
	if e != nil {
		return dbops.RowsPage{}, e
	}
	return r.Rows(ctx, schema, table, p)
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
	return r.Select(ctx, q)
}
