package catalog

import (
	"errors"
)

var (
	ErrInvalidRequest = errors.New("invalid database request")
	ErrObjectNotFound = errors.New("database object not found")
)

type Schema struct {
	Name string `json:"name"`
}
type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Type   string `json:"type"`
}
type Column struct {
	Name     string `json:"name"`
	Position int    `json:"position"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  any    `json:"default,omitempty"`
	Ordinal  int    `json:"ordinal"`
}
type Constraint struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Columns    []string `json:"columns"`
	Definition string   `json:"definition,omitempty"`
}
type Index struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Method  string   `json:"method,omitempty"`
	Columns []string `json:"columns"`
}
type PageRequest struct {
	Limit, Offset   int
	Columns         []string
	Sort, Direction string
	Filters         map[string]string
}
type RowsPage struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}
type QueryRequest struct {
	SQL   string `json:"sql"`
	Args  []any  `json:"args"`
	Limit int    `json:"limit"`
}
type QueryResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}
