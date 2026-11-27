package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	persistence "hello-world/internal/database/platform"
	target "hello-world/internal/database/target/migration"
	"hello-world/internal/services/migration"
	"hello-world/migrations"
)

func migrationTestDatabases(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_DSN to run PostgreSQL integration tests")
	}
	ctx := context.Background()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	openSchema := func(schema string) *sql.DB {
		t.Helper()
		execSQL(t, admin, "CREATE SCHEMA "+schema)
		t.Cleanup(func() {
			conn := stdlib.OpenDB(*config)
			defer conn.Close()
			if _, err := conn.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
				t.Error(err)
			}
		})
		scoped := config.Copy()
		scoped.RuntimeParams["search_path"] = schema
		db := stdlib.OpenDB(*scoped)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	schema := fmt.Sprintf("migration_definition_test_%d", time.Now().UnixNano())
	platformDB := openSchema(schema + "_platform")
	targetDB := openSchema(schema + "_target")
	if err := migrations.Apply(ctx, platformDB); err != nil {
		t.Fatal(err)
	}
	execSQL(t, platformDB, `INSERT INTO platform_users(id, username, password_hash) VALUES ('editor', 'editor', 'unused')`)
	execSQL(t, platformDB, `INSERT INTO managed_db_connections(id, name, driver, dsn_ciphertext) VALUES ('db', 'Test target', 'postgres', 'unused')`)
	return platformDB, targetDB
}

func TestMigrationDefinitionsAndAttemptHistory(t *testing.T) {
	ctx := context.Background()
	platformDB, targetDB := migrationTestDatabases(t)
	actor := migration.Principal{UserID: "editor"}
	store := persistence.NewPostgresStore(platformDB)
	targetUnavailable := false
	connector := target.NewSQLTargetConnector(func(context.Context, string) (*sql.DB, error) {
		if targetUnavailable {
			return nil, errors.New("target unreachable")
		}
		return targetDB, nil
	})
	service := migration.NewService(migration.AuthorizerFunc(func(context.Context, migration.Principal, string, migration.Action) error {
		return nil
	}), store, connector, nil)

	const originalSQL = "INSERT INTO products VALUES (0);"
	const revisedSQL = "INSERT INTO products VALUES (1);"
	const sqlDown = "DELETE FROM products WHERE id = 1;"
	draft, err := service.CreateDraft(ctx, actor, migration.CreateDraftInput{
		DatabaseID: "db", Title: "Insert product", SQLContent: originalSQL, SQLDown: "DELETE FROM products WHERE id = 0;",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateDraft(ctx, actor, migration.UpdateDraftInput{
		DatabaseID: "db", MigrationID: draft.ID, SQLContent: revisedSQL, SQLDown: sqlDown,
	})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("expected an updated definition at revision 2: %+v, %v", updated, err)
	}

	stale := &migration.MigrationRun{
		MigrationID: draft.ID, DatabaseID: "db", Revision: draft.Revision, SQLSnapshot: originalSQL,
		Status: migration.RunStatusRunning, Operation: migration.RunApply, ExecutedBy: actor.UserID, StartedAt: time.Now(),
	}
	if err := store.CreateRun(ctx, stale); !errors.Is(err, migration.ErrRevisionChanged) {
		t.Fatalf("stale execution should be rejected: %v", err)
	}

	failed, err := service.ExecuteMigration(ctx, actor, "db", draft.ID)
	if !errors.Is(err, migration.ErrExecutionFailed) || failed == nil || failed.Run.Status != migration.RunStatusFailed {
		t.Fatalf("insert into missing target table should produce a failed attempt: %+v, %v", failed, err)
	}
	_, err = service.UpdateDraft(ctx, actor, migration.UpdateDraftInput{
		DatabaseID: "db", MigrationID: draft.ID, SQLContent: originalSQL, SQLDown: sqlDown,
	})
	if !errors.Is(err, migration.ErrDefinitionFrozen) {
		t.Fatalf("editing an attempted revision should be rejected: %v", err)
	}

	execSQL(t, targetDB, `CREATE TABLE products (id INT PRIMARY KEY)`)
	succeeded, err := service.ExecuteMigration(ctx, actor, "db", draft.ID)
	if err != nil || succeeded.Run.Status != migration.RunStatusSucceeded {
		t.Fatalf("retry should succeed after the target prerequisite is restored: %+v, %v", succeeded, err)
	}
	if err := store.CompleteRunFailed(ctx, succeeded.Run.ID, draft.ID, "overwrite", 0, time.Now()); err == nil {
		t.Fatal("a finished attempt must not accept a second outcome")
	}

	detail, err := service.GetMigrationDetail(ctx, actor, "db", draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Migration.SQLDown != sqlDown || detail.Migration.FrozenAt == nil || detail.Migration.Version == nil ||
		*detail.Migration.Version != 1 || len(detail.Runs) != 2 {
		t.Fatalf("unexpected saved migration detail: %+v", detail)
	}
	successRun, failedRun := detail.Runs[0], detail.Runs[1]
	if successRun.ID == failedRun.ID || successRun.Status != migration.RunStatusSucceeded || failedRun.Status != migration.RunStatusFailed ||
		successRun.AttemptNumber != 2 || failedRun.AttemptNumber != 1 {
		t.Fatalf("expected distinct successful retry and failed first attempt: %+v", detail.Runs)
	}
	for _, run := range detail.Runs {
		if run.Revision != 2 || run.SQLSnapshot != revisedSQL || run.FinishedAt == nil ||
			run.SQLDispatchStarted == nil || !*run.SQLDispatchStarted {
			t.Fatalf("unexpected saved attempt: %+v", run)
		}
	}

	irreversible, err := service.CreateDraft(ctx, actor, migration.CreateDraftInput{
		DatabaseID: "db", Title: "Irreversible definition", SQLContent: revisedSQL, IrreversibleReason: "No safe reversal",
	})
	if err != nil {
		t.Fatal(err)
	}
	targetUnavailable = true
	failed, err = service.ExecuteMigration(ctx, actor, "db", irreversible.ID)
	if !errors.Is(err, migration.ErrExecutionFailed) || failed == nil {
		t.Fatalf("an unavailable target should produce a failed attempt: %+v, %v", failed, err)
	}
	detail, err = service.GetMigrationDetail(ctx, actor, "db", irreversible.ID)
	if err != nil || len(detail.Runs) != 1 || detail.Runs[0].Status != migration.RunStatusFailed ||
		detail.Migration.Version != nil || detail.Migration.IrreversibleReason != "No safe reversal" {
		t.Fatalf("the target connection failure should persist in history: %+v, %v", detail, err)
	}
	history, err := service.ListMigrationHistory(ctx, actor, "db")
	if err != nil || len(history) != 3 {
		t.Fatalf("expected all three attempts in connection history: %+v, %v", history, err)
	}
	if entry := history[0]; entry.Status != migration.RunStatusFailed || entry.SQLSnapshot != revisedSQL ||
		entry.SQLDispatchStarted == nil || *entry.SQLDispatchStarted {
		t.Fatalf("connection failure should retain planned SQL without dispatch: %+v", entry)
	}
	for _, entry := range history[1:] {
		if entry.SQLDispatchStarted == nil || !*entry.SQLDispatchStarted {
			t.Fatalf("SQL attempt must be marked as dispatched: %+v", entry)
		}
	}
}
