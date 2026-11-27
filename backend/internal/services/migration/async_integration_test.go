package migration_test

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
	"hello-world/internal/database/target/manageddb"
	targetmigration "hello-world/internal/database/target/migration"
	"hello-world/internal/observability"
	"hello-world/internal/services/authn"
	"hello-world/internal/services/migration"
	"hello-world/migrations"
)

func TestAsyncMigrationEndToEndPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_DSN to run PostgreSQL async migration E2E tests")
	}

	ctx := context.Background()
	baseCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	adminDB := stdlib.OpenDB(*baseCfg)
	defer adminDB.Close()

	suffix := time.Now().UnixNano()
	platformSchema := fmt.Sprintf("e2e_platform_%d", suffix)
	targetSchemaA := fmt.Sprintf("e2e_target_a_%d", suffix)
	targetSchemaB := fmt.Sprintf("e2e_target_b_%d", suffix)

	for _, sc := range []string{platformSchema, targetSchemaA, targetSchemaB} {
		if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+sc); err != nil {
			t.Fatal(err)
		}
		schemaName := sc
		defer func() {
			_, _ = adminDB.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+schemaName+` CASCADE`)
		}()
	}

	openSchemaDB := func(schema string) *sql.DB {
		cfgCopy := baseCfg.Copy()
		cfgCopy.RuntimeParams["search_path"] = schema
		db := stdlib.OpenDB(*cfgCopy)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	platformDB := openSchemaDB(platformSchema)
	targetDBA := openSchemaDB(targetSchemaA)
	targetDBB := openSchemaDB(targetSchemaB)

	if err := migrations.Apply(ctx, platformDB); err != nil {
		t.Fatal(err)
	}

	authStore, err := persistence.NewAuthStore(platformDB)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := authn.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hash.Encoded()
	if err != nil {
		t.Fatal(err)
	}
	if err := authStore.SeedAccount(ctx, persistence.SeedAccount{
		ID: "editor-1", Username: "editor", PasswordHash: encoded, GroupID: "grp-edit", GroupName: "Editors",
	}); err != nil {
		t.Fatal(err)
	}
	if err := authStore.SeedAccount(ctx, persistence.SeedAccount{
		ID: "viewer-1", Username: "viewer", PasswordHash: encoded, GroupID: "grp-view", GroupName: "Viewers",
	}); err != nil {
		t.Fatal(err)
	}

	connStore, err := persistence.NewConnectionStore(platformDB, "01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"db-a", "db-b"} {
		if err := connStore.Save(ctx, manageddb.Config{
			ID: id, Name: id, Driver: "postgres", DSN: dsn, Pool: manageddb.PoolConfig{MaxOpen: 4, MaxIdle: 2},
		}); err != nil {
			t.Fatal(err)
		}
	}

	targetConnector := targetmigration.NewSQLTargetConnectorWithResolver(func(_ context.Context, databaseID string) (*sql.DB, string, error) {
		switch databaseID {
		case "db-a":
			return targetDBA, "postgres", nil
		case "db-b":
			return targetDBB, "postgres", nil
		default:
			return nil, "", migration.ErrNotFound
		}
	})

	authorizer := migration.AuthorizerFunc(func(_ context.Context, principal migration.Principal, _ string, action migration.Action) error {
		if principal.UserID == "editor-1" {
			return nil
		}
		if principal.UserID == "viewer-1" && action == migration.ActionReadMigration {
			return nil
		}
		return migration.ErrForbidden
	})

	migStore := persistence.NewPostgresStore(platformDB)
	jobStore := persistence.NewJobStore(platformDB)
	lockStore := persistence.NewLockStore(platformDB)

	svc := migration.NewService(authorizer, migStore, targetConnector, nil)
	svc.ConfigureAsync(jobStore, lockStore, migration.AllowAllApprovals{}, observability.NewMetrics())
	pool := migration.NewWorkerPool(svc, targetConnector, migration.DefaultWorkerConfig())

	editor := migration.Principal{UserID: "editor-1", Username: "editor"}
	viewer := migration.Principal{UserID: "viewer-1", Username: "viewer"}

	normalDraft, err := svc.CreateDraft(ctx, editor, migration.CreateDraftInput{
		DatabaseID: "db-a",
		Title:      "Create orders table",
		SQLContent: `CREATE TABLE orders (id BIGINT PRIMARY KEY, amount INT NOT NULL CHECK (amount >= 0), status TEXT NOT NULL DEFAULT 'pending');`,
		SQLDown:    `DROP TABLE orders;`,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.EnqueueExecute(ctx, viewer, "db-a", normalDraft.ID); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for viewer enqueue, got %v", err)
	}

	enqNormal, err := svc.EnqueueExecute(ctx, editor, "db-a", normalDraft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := pool.ProcessOne(ctx, "worker-1"); err != nil || !processed {
		t.Fatalf("expected normal job processed, processed=%v err=%v", processed, err)
	}
	gotNormal, err := jobStore.GetJob(ctx, enqNormal.JobID)
	if err != nil || gotNormal.Status != migration.JobStatusSuccess {
		t.Fatalf("expected normal job success, got %+v err=%v", gotNormal, err)
	}
	if _, err := lockStore.GetLock(ctx, "db-a"); !errors.Is(err, migration.ErrLockNotFound) {
		t.Fatalf("expected db-a lock released, got %v", err)
	}

	if _, err := targetDBA.ExecContext(ctx, `INSERT INTO orders(id, amount, status) VALUES (1, 100, 'pending'), (2, 100, 'pending'), (3, 100, 'pending'), (4, 100, 'pending'), (5, 10, 'pending'), (6, 100, 'pending')`); err != nil {
		t.Fatal(err)
	}

	batchSize := 2
	batchDraft, err := svc.CreateDraft(ctx, editor, migration.CreateDraftInput{
		DatabaseID:       "db-a",
		Title:            "Batch deduct fee",
		SQLContent:       `WITH batch AS (SELECT id FROM orders WHERE id > $1 AND id <= $2 ORDER BY id LIMIT $3) UPDATE orders SET amount = amount - 50, status = 'charged' WHERE id IN (SELECT id FROM batch);`,
		SQLDown:          `UPDATE orders SET amount = amount + 50, status = 'pending' WHERE id >= $1 AND id <= $2;`,
		ExecutionMode:    migration.ModeBatch,
		BatchByAttribute: "id",
		BatchSize:        &batchSize,
	})
	if err != nil {
		t.Fatal(err)
	}

	enqBatch, err := svc.EnqueueExecute(ctx, editor, "db-a", batchDraft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := pool.ProcessOne(ctx, "worker-1"); err != nil || !processed {
		t.Fatalf("expected batch job processed to partial, processed=%v err=%v", processed, err)
	}
	partialJob, err := jobStore.GetJob(ctx, enqBatch.JobID)
	if err != nil || partialJob.Status != migration.JobStatusPartial || partialJob.LastCommittedBatch != 2 || partialJob.CommittedRows != 4 {
		t.Fatalf("expected partial job at batch 2 with 4 rows, got %+v err=%v", partialJob, err)
	}
	heldLock, err := lockStore.GetLock(ctx, "db-a")
	if err != nil || heldLock.LockState != migration.LockStateHeldPartial {
		t.Fatalf("expected lock held_partial on db-a, got %+v err=%v", heldLock, err)
	}

	otherDraftA, err := svc.CreateDraft(ctx, editor, migration.CreateDraftInput{
		DatabaseID:         "db-a",
		Title:              "Blocked while partial",
		SQLContent:         `CREATE TABLE blocked_tbl (id INT);`,
		IrreversibleReason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnqueueExecute(ctx, editor, "db-a", otherDraftA.ID); err != nil {
		t.Fatal(err)
	}

	draftB, err := svc.CreateDraft(ctx, editor, migration.CreateDraftInput{
		DatabaseID:         "db-b",
		Title:              "Independent DB B migration",
		SQLContent:         `CREATE TABLE db_b_table (id INT);`,
		IrreversibleReason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	enqB, err := svc.EnqueueExecute(ctx, editor, "db-b", draftB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := pool.ProcessOne(ctx, "worker-2"); err != nil || !processed {
		t.Fatalf("expected db-b job to execute independently while db-a is locked, processed=%v err=%v", processed, err)
	}
	gotB, err := jobStore.GetJob(ctx, enqB.JobID)
	if err != nil || gotB.Status != migration.JobStatusSuccess {
		t.Fatalf("expected db-b job success, got %+v err=%v", gotB, err)
	}

	enqRevert, err := svc.EnqueueRevertBatch(ctx, editor, "db-a", partialJob.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := pool.ProcessOne(ctx, "worker-1"); err != nil || !processed {
		t.Fatalf("expected revert_batch processed, processed=%v err=%v", processed, err)
	}
	gotRevert, err := jobStore.GetJob(ctx, enqRevert.JobID)
	if err != nil || gotRevert.Status != migration.JobStatusSuccess {
		t.Fatalf("expected revert_batch success, got %+v err=%v", gotRevert, err)
	}

	var chargedAfterRevert int
	if err := targetDBA.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE status = 'charged'`).Scan(&chargedAfterRevert); err != nil {
		t.Fatal(err)
	}
	if chargedAfterRevert != 0 {
		t.Fatalf("expected 0 charged rows after compensating revert, got %d", chargedAfterRevert)
	}

	if processed, err := pool.ProcessOne(ctx, "worker-1"); err != nil || !processed {
		t.Fatalf("expected previously blocked db-a job to run after lock release, processed=%v err=%v", processed, err)
	}

	enqBatch2, err := svc.EnqueueExecute(ctx, editor, "db-a", batchDraft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ProcessOne(ctx, "worker-1"); err != nil {
		t.Fatal(err)
	}
	partialJob2, err := jobStore.GetJob(ctx, enqBatch2.JobID)
	if err != nil || partialJob2.Status != migration.JobStatusPartial {
		t.Fatalf("expected second batch run partial before data fix, got %+v err=%v", partialJob2, err)
	}

	if _, err := targetDBA.ExecContext(ctx, `UPDATE orders SET amount = 100 WHERE id = 5`); err != nil {
		t.Fatal(err)
	}
	enqResume, err := svc.EnqueueResumeBatch(ctx, editor, "db-a", partialJob2.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ProcessOne(ctx, "worker-1"); err != nil {
		t.Fatal(err)
	}
	gotResume, err := jobStore.GetJob(ctx, enqResume.JobID)
	if err != nil || gotResume.Status != migration.JobStatusSuccess {
		t.Fatalf("expected resume_batch success after data fix, got %+v err=%v", gotResume, err)
	}

	var chargedAfterResume int
	if err := targetDBA.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE status = 'charged'`).Scan(&chargedAfterResume); err != nil {
		t.Fatal(err)
	}
	if chargedAfterResume != 6 {
		t.Fatalf("expected all 6 rows charged after resume_batch, got %d", chargedAfterResume)
	}
}
