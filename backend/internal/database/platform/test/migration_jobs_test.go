package persistence_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	persistence "hello-world/internal/database/platform"
	"hello-world/internal/database/target/manageddb"
	"hello-world/internal/services/authn"
	"hello-world/internal/services/migration"
	"hello-world/migrations"
)

func TestJobQueueAndDatabaseLocksIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_DSN to run PostgreSQL integration tests")
	}
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	adminDB := stdlib.OpenDB(*cfg)
	defer adminDB.Close()

	schema := fmt.Sprintf("jobs_lock_test_%d", time.Now().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = adminDB.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	}()

	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()

	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}

	authStore, err := persistence.NewAuthStore(db)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := authn.HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hash.Encoded()
	if err != nil {
		t.Fatal(err)
	}
	if err := authStore.SeedAccount(ctx, persistence.SeedAccount{
		ID: "actor-1", Username: "actor1", PasswordHash: encoded, GroupID: "grp-1", GroupName: "Team 1",
	}); err != nil {
		t.Fatal(err)
	}

	connStore, err := persistence.NewConnectionStore(db, "01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	for _, dbID := range []string{"db-a", "db-b"} {
		if err := connStore.Save(ctx, manageddb.Config{
			ID: dbID, Name: dbID, Driver: "postgres", DSN: dsn, Pool: manageddb.PoolConfig{MaxOpen: 2, MaxIdle: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}

	migStore := persistence.NewPostgresStore(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	batchSize := 500
	mA := &migration.Migration{
		DatabaseID:       "db-a",
		Title:            "Batch A",
		SQLContent:       "UPDATE items SET active = true WHERE id > $1 AND id <= $2;",
		SQLDown:          "UPDATE items SET active = false WHERE id > $1 AND id <= $2;",
		ExecutionMode:    migration.ModeBatch,
		BatchByAttribute: "id",
		BatchSize:        &batchSize,
		Status:           migration.StatusDraft,
		CreatedBy:        "actor-1",
		UpdatedBy:        "actor-1",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := migStore.CreateMigration(ctx, mA); err != nil {
		t.Fatal(err)
	}
	mB := &migration.Migration{
		DatabaseID:         "db-b",
		Title:              "Normal B",
		SQLContent:         "CREATE TABLE t_b (id INT);",
		IrreversibleReason: "test",
		ExecutionMode:      migration.ModeNormal,
		Status:             migration.StatusDraft,
		CreatedBy:          "actor-1",
		UpdatedBy:          "actor-1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := migStore.CreateMigration(ctx, mB); err != nil {
		t.Fatal(err)
	}

	jobStore := persistence.NewJobStore(db)
	lockStore := persistence.NewLockStore(db)

	attr := "id"
	jobA1 := &migration.Job{
		JobID: uuid.NewString(), MigrationID: mA.ID, ExecutionID: uuid.NewString(),
		DatabaseID: "db-a", ActorID: "actor-1", ActorUsername: "actor1",
		Action: migration.JobActionExecute, Mode: migration.ModeBatch, Status: migration.JobStatusPending,
		BatchByAttribute: &attr, BatchSize: &batchSize, MaxRetries: 3,
		TraceContext: []byte(`{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}`),
		EnqueuedAt:   now, UpdatedAt: now,
	}
	jobA2 := &migration.Job{
		JobID: uuid.NewString(), MigrationID: mA.ID, ExecutionID: uuid.NewString(),
		DatabaseID: "db-a", ActorID: "actor-1", ActorUsername: "actor1",
		Action: migration.JobActionExecute, Mode: migration.ModeBatch, Status: migration.JobStatusPending,
		BatchByAttribute: &attr, BatchSize: &batchSize, MaxRetries: 3,
		EnqueuedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	}
	jobB1 := &migration.Job{
		JobID: uuid.NewString(), MigrationID: mB.ID, ExecutionID: uuid.NewString(),
		DatabaseID: "db-b", ActorID: "actor-1", ActorUsername: "actor1",
		Action: migration.JobActionExecute, Mode: migration.ModeNormal, Status: migration.JobStatusPending,
		MaxRetries: 3, EnqueuedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
	}
	for _, j := range []*migration.Job{jobA1, jobA2, jobB1} {
		if err := jobStore.EnqueueJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}

	claimed1, lock1, err := jobStore.ClaimNextJob(ctx, "worker-1", 15*time.Second, now.Add(3*time.Second))
	if err != nil || claimed1 == nil || claimed1.JobID != jobA1.JobID || lock1.DatabaseID != "db-a" {
		t.Fatalf("expected worker-1 to claim jobA1, got job=%v lock=%v err=%v", claimed1, lock1, err)
	}

	claimed2, lock2, err := jobStore.ClaimNextJob(ctx, "worker-2", 15*time.Second, now.Add(3*time.Second))
	if err != nil || claimed2 == nil || claimed2.JobID != jobB1.JobID || lock2.DatabaseID != "db-b" {
		t.Fatalf("expected worker-2 to claim jobB1 in parallel, got job=%v lock=%v err=%v", claimed2, lock2, err)
	}

	claimed3, _, err := jobStore.ClaimNextJob(ctx, "worker-3", 15*time.Second, now.Add(3*time.Second))
	if err != nil || claimed3 != nil {
		t.Fatalf("expected jobA2 to remain blocked while db-a lock is active, got %v err=%v", claimed3, err)
	}

	if err := lockStore.TransitionLockState(ctx, "db-a", jobA1.JobID, migration.LockStateHeldPartial, jobA1.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if err := jobStore.FinishJob(ctx, jobA1.JobID, migration.JobStatusPartial, nil, nil, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}

	claimed4, _, err := jobStore.ClaimNextJob(ctx, "worker-3", 15*time.Second, now.Add(5*time.Second))
	if err != nil || claimed4 != nil {
		t.Fatalf("expected regular jobA2 to remain blocked during held_partial, got %v err=%v", claimed4, err)
	}

	resumeJob := &migration.Job{
		JobID: uuid.NewString(), MigrationID: mA.ID, ExecutionID: uuid.NewString(),
		ParentExecutionID: &jobA1.ExecutionID, DatabaseID: "db-a", ActorID: "actor-1", ActorUsername: "actor1",
		Action: migration.JobActionResumeBatch, Mode: migration.ModeBatch, Status: migration.JobStatusPending,
		BatchByAttribute: &attr, BatchSize: &batchSize, MaxRetries: 3,
		EnqueuedAt: now.Add(6 * time.Second), UpdatedAt: now.Add(6 * time.Second),
	}
	if err := jobStore.EnqueueJob(ctx, resumeJob); err != nil {
		t.Fatal(err)
	}
	claimedResume, _, err := jobStore.ClaimNextJob(ctx, "worker-3", 15*time.Second, now.Add(7*time.Second))
	if err != nil || claimedResume == nil || claimedResume.JobID != resumeJob.JobID {
		t.Fatalf("expected worker-3 to take over held_partial lock for resumeJob, got %v err=%v", claimedResume, err)
	}

	expired, err := lockStore.SweepExpiredLeases(ctx, now.Add(60*time.Second))
	if err != nil || len(expired) != 2 {
		t.Fatalf("expected 2 expired active locks swept to held_uncertain, got %d err=%v", len(expired), err)
	}
	uncertainJob, err := jobStore.GetJob(ctx, resumeJob.JobID)
	if err != nil || uncertainJob.Status != migration.JobStatusUncertain {
		t.Fatalf("expected resumeJob status uncertain after lease expiry, got %v err=%v", uncertainJob, err)
	}
	if err := lockStore.ReleaseLock(ctx, "db-a", resumeJob.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := lockStore.GetLock(ctx, "db-a"); !errors.Is(err, migration.ErrLockNotFound) {
		t.Fatalf("expected db-a lock removed, got %v", err)
	}
}
