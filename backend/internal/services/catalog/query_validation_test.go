package catalog_test

import (
	"errors"
	"testing"

	"hello-world/internal/services/catalog"
)

func TestValidateSelect(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{name: "select", query: "SELECT id FROM users"},
		{name: "quoted separator", query: `SELECT ';' AS value`},
		{name: "comment before select", query: "/* read */ SELECT 1"},
		{name: "empty", query: "", wantErr: true},
		{name: "multiple statements", query: "SELECT 1; DROP TABLE users", wantErr: true},
		{name: "common table expression", query: "WITH rows AS (SELECT 1) SELECT * FROM rows", wantErr: true},
		{name: "select into", query: "SELECT id INTO copied_users FROM users", wantErr: true},
		{name: "unclosed quote", query: "SELECT 'unfinished", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := catalog.ValidateSelect(test.query)
			if test.wantErr != errors.Is(err, catalog.ErrUnsafeQuery) {
				t.Fatalf("ValidateSelect(%q) error = %v, wantErr %t", test.query, err, test.wantErr)
			}
		})
	}
}
