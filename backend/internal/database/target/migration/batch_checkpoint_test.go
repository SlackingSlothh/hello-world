package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	servicemigration "hello-world/internal/services/migration"
)

func TestExtractTargetTableAndPlaceholders(t *testing.T) {
	cases := []struct {
		name       string
		sql        string
		wantSchema string
		wantTable  string
		wantParams int
	}{
		{
			name:       "cte update with three params",
			sql:        "WITH batch AS (SELECT id FROM items WHERE id > $1 AND id <= $2 ORDER BY id LIMIT $3) UPDATE items SET status = 'done' WHERE id IN (SELECT id FROM batch);",
			wantSchema: "",
			wantTable:  "items",
			wantParams: 3,
		},
		{
			name:       "schema qualified update with two params",
			sql:        `UPDATE "public"."orders" SET total = total + 1 WHERE id >= $1 AND id <= $2`,
			wantSchema: "public",
			wantTable:  "orders",
			wantParams: 2,
		},
		{
			name:       "delete with literal dollar",
			sql:        "DELETE FROM logs WHERE note <> '$9' AND id >= $1 AND id <= $2",
			wantSchema: "",
			wantTable:  "logs",
			wantParams: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema, table, err := ExtractTargetTable(tc.sql)
			if err != nil {
				t.Fatal(err)
			}
			if schema != tc.wantSchema || table != tc.wantTable {
				t.Fatalf("got %q.%q, want %q.%q", schema, table, tc.wantSchema, tc.wantTable)
			}
			if got := CountSQLPlaceholders(tc.sql); got != tc.wantParams {
				t.Fatalf("got %d placeholders, want %d", got, tc.wantParams)
			}
		})
	}
}

func TestCursorHelpers(t *testing.T) {
	if v, ok := CursorArg("42").(int64); !ok || v != 42 {
		t.Fatalf("expected int64(42), got %#v", CursorArg("42"))
	}
	if v, ok := CursorBefore("1").(int64); !ok || v != 0 {
		t.Fatalf("expected int64(0), got %#v", CursorBefore("1"))
	}
	if v, ok := CursorArg("abc-uuid").(string); !ok || v != "abc-uuid" {
		t.Fatalf("expected string cursor, got %#v", CursorArg("abc-uuid"))
	}
	if v, ok := CursorBefore("abc-uuid").(string); !ok || v != "" {
		t.Fatalf("expected empty string before text cursor, got %#v", CursorBefore("abc-uuid"))
	}
}

func TestAtomicBatchCheckpointAndCompensatingRevertIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_DSN to run PostgreSQL target batch integration tests")
	}
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	adminDB := stdlib.OpenDB(*cfg)
	defer adminDB.Close()

	schema := fmt.Sprintf("target_batch_test_%d", time.Now().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = adminDB.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	}()

	cfg.RuntimeParams["search_path"] = schema
	targetDB := stdlib.OpenDB(*cfg)
	defer targetDB.Close()

	if _, err := targetDB.ExecContext(ctx, `CREATE TABLE items (id BIGINT PRIMARY KEY, val INT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := targetDB.ExecContext(ctx, `INSERT INTO items(id, val) SELECT g, 1 FROM generate_series(1, 5) AS g`); err != nil {
		t.Fatal(err)
	}

	connector := NewSQLTargetConnectorWithResolver(func(context.Context, string) (*sql.DB, string, error) {
		return targetDB, "postgres", nil
	})
	if err := connector.EnsureCheckpointSchema(ctx, "target-1"); err != nil {
		t.Fatal(err)
	}

	sqlUp := `WITH b AS (SELECT id FROM items WHERE id > $1 AND id <= $2 ORDER BY id LIMIT $3) UPDATE items SET val = val + 10 WHERE id IN (SELECT id FROM b)`
	sqlDown := `UPDATE items SET val = val - 10 WHERE id >= $1 AND id <= $2`

	scopeMax, err := connector.ResolveTargetScopeMax(ctx, "target-1", sqlUp, "id")
	if err != nil || scopeMax != "5" {
		t.Fatalf("expected scopeMax 5, got %q err=%v", scopeMax, err)
	}

	execID := uuid.NewString()
	res1, err := connector.ExecuteBatchStep(ctx, "target-1", servicemigration.BatchStepRequest{
		ExecutionID: execID, BatchNumber: 1, SQLQuery: sqlUp, CursorColumn: "id",
		LastCursor: "", TargetScopeMax: scopeMax, BatchSize: 2,
	})
	if err != nil || res1.Done || res1.AlreadyCommitted || res1.Checkpoint.CursorEnd != "2" || res1.Checkpoint.RowsAffected != 2 {
		t.Fatalf("unexpected batch 1 result: %+v err=%v", res1, err)
	}

	dup1, err := connector.ExecuteBatchStep(ctx, "target-1", servicemigration.BatchStepRequest{
		ExecutionID: execID, BatchNumber: 1, SQLQuery: sqlUp, CursorColumn: "id",
		LastCursor: "", TargetScopeMax: scopeMax, BatchSize: 2,
	})
	if err != nil || !dup1.AlreadyCommitted {
		t.Fatalf("expected batch 1 idempotent skip, got %+v err=%v", dup1, err)
	}

	var val1 int
	if err := targetDB.QueryRowContext(ctx, `SELECT val FROM items WHERE id = 1`).Scan(&val1); err != nil || val1 != 11 {
		t.Fatalf("double commit detected or wrong value: val1=%d err=%v", val1, err)
	}

	res2, err := connector.ExecuteBatchStep(ctx, "target-1", servicemigration.BatchStepRequest{
		ExecutionID: execID, BatchNumber: 2, SQLQuery: sqlUp, CursorColumn: "id",
		LastCursor: res1.Checkpoint.CursorEnd, TargetScopeMax: scopeMax, BatchSize: 2,
	})
	if err != nil || res2.Checkpoint.CursorEnd != "4" || res2.Checkpoint.RowsAffected != 2 {
		t.Fatalf("unexpected batch 2 result: %+v err=%v", res2, err)
	}

	cps, err := connector.ListCheckpoints(ctx, "target-1", execID, servicemigration.DirectionUp)
	if err != nil || len(cps) != 2 {
		t.Fatalf("expected 2 up checkpoints, got %d err=%v", len(cps), err)
	}

	for i := len(cps) - 1; i >= 0; i-- {
		rev, err := connector.RevertBatchStep(ctx, "target-1", servicemigration.RevertStepRequest{
			ExecutionID: execID, Checkpoint: cps[i], SQLDown: sqlDown, CursorColumn: "id", BatchSize: 2,
		})
		if err != nil || rev.AlreadyCommitted || rev.Checkpoint.RowsAffected != 2 {
			t.Fatalf("unexpected revert step %d: %+v err=%v", cps[i].BatchNumber, rev, err)
		}
	}

	var sum int
	if err := targetDB.QueryRowContext(ctx, `SELECT SUM(val) FROM items`).Scan(&sum); err != nil || sum != 5 {
		t.Fatalf("expected all 5 items restored to val=1 (sum=5), got sum=%d err=%v", sum, err)
	}
}
