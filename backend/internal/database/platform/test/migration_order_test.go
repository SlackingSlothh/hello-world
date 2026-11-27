package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	persistence "hello-world/internal/database/platform"
	target "hello-world/internal/database/target/migration"
	"hello-world/internal/services/migration"
)

func TestMigrationOrder(t *testing.T) {
	platformDB, targetDB := migrationTestDatabases(t)
	execSQL(t, targetDB, `CREATE TABLE samples (id INT PRIMARY KEY)`)
	ctx := context.Background()
	actor := migration.Principal{UserID: "editor"}
	store := persistence.NewPostgresStore(platformDB)
	connector := target.NewSQLTargetConnector(func(context.Context, string) (*sql.DB, error) { return targetDB, nil })
	authorizer := migration.AuthorizerFunc(func(context.Context, migration.Principal, string, migration.Action) error { return nil })
	service := migration.NewService(authorizer, store, connector, nil)

	create := func(id int) *migration.Migration {
		t.Helper()
		draft, err := service.CreateDraft(ctx, actor, migration.CreateDraftInput{
			DatabaseID: "db", Title: fmt.Sprint(id),
			SQLContent: fmt.Sprintf("INSERT INTO samples VALUES (%d);", id),
			SQLDown:    fmt.Sprintf("DELETE FROM samples WHERE id = %d;", id),
		})
		if err != nil {
			t.Fatal(err)
		}
		return draft
	}
	apply := func(id int64) *migration.ExecuteResult {
		t.Helper()
		result, err := service.ExecuteMigration(ctx, actor, "db", id)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	rollback := func(id, targetRunID int64) *migration.ExecuteResult {
		t.Helper()
		result, err := service.RollbackMigration(ctx, actor, "db", id)
		if err != nil || result.Run.Operation != migration.RunRollback ||
			result.Run.RollbackTargetRunID == nil || *result.Run.RollbackTargetRunID != targetRunID {
			t.Fatalf("rollback should reference execution %d: %+v, %v", targetRunID, result, err)
		}
		return result
	}
	forward := func(id, version int64, expectedSQL string) *migration.ExecuteResult {
		t.Helper()
		result, err := service.ForwardMigration(ctx, actor, "db", id)
		if err != nil || result.Run.Operation != migration.RunForward || result.Run.SQLSnapshot != expectedSQL ||
			result.Migration.Version == nil || *result.Migration.Version != version {
			t.Fatalf("forward should replay version %d: %+v, %v", version, result, err)
		}
		return result
	}

	a, b, c := create(1), create(2), create(3)
	apply(a.ID)
	bUp, cUp := apply(b.ID), apply(c.ID)
	if _, err := service.RollbackMigration(ctx, actor, "db", b.ID); !errors.Is(err, migration.ErrMigrationOrder) {
		t.Fatalf("only the active tip can be rolled back: %v", err)
	}
	execSQL(t, targetDB, `ALTER TABLE samples RENAME TO moved_samples`)
	failed, err := service.RollbackMigration(ctx, actor, "db", c.ID)
	if !errors.Is(err, migration.ErrExecutionFailed) || failed == nil ||
		failed.Migration.Status != migration.StatusApplied || failed.Run.Status != migration.RunStatusFailed {
		t.Fatalf("failed rollback should leave the migration applied: %+v, %v", failed, err)
	}
	if _, err := service.RollbackMigration(ctx, actor, "db", b.ID); !errors.Is(err, migration.ErrMigrationOrder) {
		t.Fatalf("failed rollback should not move the active tip: %v", err)
	}
	execSQL(t, targetDB, `ALTER TABLE moved_samples RENAME TO samples`)
	cDown := rollback(c.ID, cUp.Run.ID)
	rollback(b.ID, bUp.Run.ID)
	if _, err := service.ForwardMigration(ctx, actor, "db", c.ID); !errors.Is(err, migration.ErrMigrationOrder) {
		t.Fatalf("forward cannot skip the next version: %v", err)
	}
	d := create(4)
	if _, err := service.ExecuteMigration(ctx, actor, "db", d.ID); !errors.Is(err, migration.ErrMigrationOrder) {
		t.Fatalf("apply should not append over a rolled-back version: %v", err)
	}

	forward(b.ID, 2, b.SQLContent)
	var count int
	if err := targetDB.QueryRow(`SELECT count(*) FROM samples`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("forward must replay one migration: count %d, error %v", count, err)
	}
	execSQL(t, targetDB, `ALTER TABLE samples RENAME TO moved_samples`)
	if _, err := service.ForwardMigration(ctx, actor, "db", c.ID); !errors.Is(err, migration.ErrExecutionFailed) {
		t.Fatalf("failed forward should remain retryable: %v", err)
	}
	execSQL(t, targetDB, `ALTER TABLE moved_samples RENAME TO samples`)
	cForward := forward(c.ID, 3, c.SQLContent)
	if cForward.Run.ID == cDown.Run.ID || cForward.Run.ID == cUp.Run.ID {
		t.Fatal("each action should have a new execution ID")
	}
	nextRollback := rollback(c.ID, cForward.Run.ID)
	if nextRollback.Run.SQLSnapshot != c.SQLDown {
		t.Fatal("rollback must execute the original SQL down")
	}
	detail, err := service.GetMigrationDetail(ctx, actor, "db", c.ID)
	if err != nil || len(detail.Runs) != 6 ||
		detail.Runs[2].Status != migration.RunStatusFailed || detail.Runs[2].Operation != migration.RunForward ||
		detail.Runs[4].Status != migration.RunStatusFailed || detail.Runs[4].Operation != migration.RunRollback {
		t.Fatalf("history should keep successful and failed runs: %+v, %v", detail, err)
	}

	history, err := service.ListMigrationHistory(ctx, actor, "db")
	if err != nil || len(history) != 10 {
		t.Fatalf("history should include all attempts across migrations: %+v, %v", history, err)
	}
	latest := history[0]
	if latest.ID != strconv.FormatInt(nextRollback.Run.ID, 10) ||
		latest.MigrationID != strconv.FormatInt(c.ID, 10) || latest.Title != c.Title ||
		latest.RollbackTargetRunID == nil || *latest.RollbackTargetRunID != strconv.FormatInt(cForward.Run.ID, 10) ||
		latest.Status != migration.RunStatusSucceeded || latest.Username != actor.UserID || latest.SQLSnapshot != c.SQLDown {
		t.Fatalf("unexpected rollback history entry: %+v", latest)
	}
	execSQL(t, platformDB, `INSERT INTO managed_db_connections(id, name, driver, dsn_ciphertext) VALUES ('other', 'Test target', 'postgres', 'unused')`)
	other, err := service.ListMigrationHistory(ctx, actor, "other")
	if err != nil || len(other) != 0 {
		t.Fatalf("history should be scoped by connection ID: %+v, %v", other, err)
	}
}

type pausedMigrationStore struct {
	migration.Store
	registered chan int64
	resume     chan struct{}
}

func (s pausedMigrationStore) CreateRun(ctx context.Context, run *migration.MigrationRun) error {
	if err := s.Store.CreateRun(ctx, run); err != nil {
		return err
	}
	s.registered <- run.ID
	<-s.resume
	return nil
}

type unavailableMigrationHistory struct{ migration.Store }

func (s unavailableMigrationHistory) CompleteRunSucceeded(context.Context, int64, int64, string, int64, time.Time) (int64, error) {
	return 0, errors.New("platform history unavailable")
}

type pausedFailedMigrationHistory struct {
	migration.Store
	dispatched chan int64
	resume     chan struct{}
}

func (s pausedFailedMigrationHistory) MarkSQLDispatchStarted(ctx context.Context, runID int64, evidence migration.TransactionEvidence) error {
	if err := s.Store.MarkSQLDispatchStarted(ctx, runID, evidence); err != nil {
		return err
	}
	s.dispatched <- runID
	<-s.resume
	return nil
}

func (s pausedFailedMigrationHistory) CompleteRunFailed(context.Context, int64, int64, string, int64, time.Time) error {
	return errors.New("platform history unavailable")
}

func TestMigrationDatabaseReservation(t *testing.T) {
	platformDB, targetDB := migrationTestDatabases(t)
	execSQL(t, targetDB, `CREATE TABLE samples (id INT PRIMARY KEY)`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	actor := migration.Principal{UserID: "editor"}
	store := persistence.NewPostgresStore(platformDB)
	connector := target.NewSQLTargetConnector(func(context.Context, string) (*sql.DB, error) { return targetDB, nil })
	authorizer := migration.AuthorizerFunc(func(context.Context, migration.Principal, string, migration.Action) error { return nil })
	service := migration.NewService(authorizer, store, connector, nil)
	create := func(id int) *migration.Migration {
		t.Helper()
		draft, err := service.CreateDraft(ctx, actor, migration.CreateDraftInput{
			DatabaseID: "db", Title: fmt.Sprint(id), SQLContent: fmt.Sprintf("INSERT INTO samples VALUES (%d);", id),
			IrreversibleReason: "No safe reversal",
		})
		if err != nil {
			t.Fatal(err)
		}
		return draft
	}

	a, b, c := create(1), create(2), create(3)
	paused := pausedMigrationStore{Store: store, registered: make(chan int64, 1), resume: make(chan struct{})}
	otherService := migration.NewService(authorizer, paused, connector, nil)
	first := make(chan error, 1)
	go func() {
		_, err := otherService.ExecuteMigration(ctx, actor, "db", a.ID)
		first <- err
	}()
	select {
	case <-paused.registered:
	case err := <-first:
		t.Fatalf("first attempt stopped before it reserved the database: %v", err)
	case <-ctx.Done():
		close(paused.resume)
		<-first
		t.Fatal(ctx.Err())
	}
	_, concurrentErr := service.ExecuteMigration(ctx, actor, "db", b.ID)
	close(paused.resume)
	if err := <-first; err != nil || !errors.Is(concurrentErr, migration.ErrMigrationLocked) {
		t.Fatalf("only the first service should execute SQL: first %v, concurrent %v", err, concurrentErr)
	}
	if _, err := service.RollbackMigration(ctx, actor, "db", a.ID); !errors.Is(err, migration.ErrIrreversible) {
		t.Fatalf("irreversible active version should reject rollback: %v", err)
	}

	historyUnavailable := migration.NewService(authorizer, unavailableMigrationHistory{store}, connector, nil)
	if _, err := historyUnavailable.ExecuteMigration(ctx, actor, "db", b.ID); !errors.Is(err, migration.ErrOutcomeUnconfirmed) {
		t.Fatalf("history failure after target commit should require verification: %v", err)
	}
	var count int
	if err := targetDB.QueryRow(`SELECT count(*) FROM samples WHERE id = 2`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("the target committed exactly once: count %d, error %v", count, err)
	}
	if _, err := service.ExecuteMigration(ctx, actor, "db", c.ID); !errors.Is(err, migration.ErrMigrationLocked) {
		t.Fatalf("the unfinished run should block other migrations on this database: %v", err)
	}
	detail, err := service.GetMigrationDetail(ctx, actor, "db", b.ID)
	if err != nil || len(detail.Runs) != 1 || detail.Runs[0].Status != migration.RunStatusRunning {
		t.Fatalf("the committed target attempt should remain reserved until verified: %+v, %v", detail, err)
	}
	runID := detail.Runs[0].ID
	if _, err := service.ReconcileRun(ctx, actor, "other-db", runID); !errors.Is(err, migration.ErrNotFound) {
		t.Fatalf("a connection cannot reconcile a run belonging to a different connection: %v", err)
	}
	reconciled := make(chan error, 2)
	for range 2 {
		go func() {
			restarted := migration.NewService(authorizer, store, connector, nil)
			result, err := restarted.ReconcileRun(ctx, actor, "db", runID)
			if err == nil && (result.ExecutionID != strconv.FormatInt(runID, 10) || result.Status != migration.RunStatusSucceeded) {
				err = fmt.Errorf("unexpected reconciliation result: %+v", result)
			}
			reconciled <- err
		}()
	}
	for range 2 {
		if err := <-reconciled; err != nil {
			t.Fatal(err)
		}
	}
	detail, err = service.GetMigrationDetail(ctx, actor, "db", b.ID)
	if err != nil || len(detail.Runs) != 1 || detail.Runs[0].ID != runID ||
		detail.Runs[0].Status != migration.RunStatusSucceeded || detail.Migration.Version == nil || *detail.Migration.Version != 2 {
		t.Fatalf("reconciliation should finalize the original attempt and assign version 2 once: %+v, %v", detail, err)
	}
	if err := targetDB.QueryRow(`SELECT count(*) FROM samples WHERE id = 2`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("reconciliation should never execute the target SQL: count %d, error %v", count, err)
	}
	if _, err := service.ExecuteMigration(ctx, actor, "db", c.ID); err != nil {
		t.Fatalf("reconciliation should release the database reservation: %v", err)
	}
}

func TestMigrationReconciliationOfAbortedTransaction(t *testing.T) {
	platformDB, targetDB := migrationTestDatabases(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	actor := migration.Principal{UserID: "editor"}
	store := persistence.NewPostgresStore(platformDB)
	connector := target.NewSQLTargetConnector(func(context.Context, string) (*sql.DB, error) { return targetDB, nil })
	authorizer := migration.AuthorizerFunc(func(context.Context, migration.Principal, string, migration.Action) error { return nil })
	service := migration.NewService(authorizer, store, connector, nil)
	draft, err := service.CreateDraft(ctx, actor, migration.CreateDraftInput{
		DatabaseID: "db", Title: "Insert sample", SQLContent: "INSERT INTO samples VALUES (1);",
		SQLDown: "DELETE FROM samples WHERE id = 1;",
	})
	if err != nil {
		t.Fatal(err)
	}
	paused := pausedFailedMigrationHistory{store, make(chan int64, 1), make(chan struct{})}
	executor := migration.NewService(authorizer, paused, connector, nil)
	done := make(chan error, 1)
	go func() {
		_, err := executor.ExecuteMigration(ctx, actor, "db", draft.ID)
		done <- err
	}()
	var runID int64
	select {
	case runID = <-paused.dispatched:
	case err := <-done:
		t.Fatalf("execution stopped before recording evidence: %v", err)
	case <-ctx.Done():
		close(paused.resume)
		<-done
		t.Fatal(ctx.Err())
	}
	_, pendingErr := service.ReconcileRun(ctx, actor, "db", runID)
	close(paused.resume)
	if err := <-done; !errors.Is(err, migration.ErrOutcomeUnconfirmed) {
		t.Fatalf("failure recording the aborted attempt should leave it unconfirmed: %v", err)
	}
	if !errors.Is(pendingErr, migration.ErrOutcomeUnconfirmed) {
		t.Fatalf("reconciliation should leave an in-progress PostgreSQL transaction alone: %v", pendingErr)
	}

	_, evidence, err := store.GetRunForReconciliation(ctx, "db", runID)
	if err != nil {
		t.Fatal(err)
	}
	evidence.TargetIdentity += "-different-timeline"
	if outcome, err := connector.VerifyTransaction(ctx, "db", evidence); err != nil || outcome != "" {
		t.Fatalf("evidence from another target identity should give no outcome: %q, %v", outcome, err)
	}
	result, err := service.ReconcileRun(ctx, actor, "db", runID)
	if err != nil || result.Status != migration.RunStatusFailed || result.ExecutionID != strconv.FormatInt(runID, 10) {
		t.Fatalf("reconciliation should mark the same run failed: %+v, %v", result, err)
	}
	detail, err := service.GetMigrationDetail(ctx, actor, "db", draft.ID)
	if err != nil || detail.Migration.Status != migration.StatusFailed || detail.Migration.Version != nil ||
		len(detail.Runs) != 1 || detail.Runs[0].Status != migration.RunStatusFailed {
		t.Fatalf("an aborted initial attempt should have no version: %+v, %v", detail, err)
	}
	execSQL(t, targetDB, `CREATE TABLE samples (id INT PRIMARY KEY)`)
	if retried, err := service.ExecuteMigration(ctx, actor, "db", draft.ID); err != nil || retried.Run.AttemptNumber != 2 {
		t.Fatalf("a confirmed abort should allow an explicit retry: %+v, %v", retried, err)
	}
}

func TestMigrationKnownCommitRollback(t *testing.T) {
	platformDB, targetDB := migrationTestDatabases(t)
	execSQL(t, targetDB, `CREATE TABLE parents (id INT PRIMARY KEY)`)
	execSQL(t, targetDB, `CREATE TABLE children (parent_id INT REFERENCES parents(id) DEFERRABLE INITIALLY DEFERRED)`)
	ctx := context.Background()
	actor := migration.Principal{UserID: "editor"}
	connector := target.NewSQLTargetConnector(func(context.Context, string) (*sql.DB, error) { return targetDB, nil })
	authorizer := migration.AuthorizerFunc(func(context.Context, migration.Principal, string, migration.Action) error { return nil })
	service := migration.NewService(authorizer, persistence.NewPostgresStore(platformDB), connector, nil)
	draft, err := service.CreateDraft(ctx, actor, migration.CreateDraftInput{
		DatabaseID: "db", Title: "Insert child", SQLContent: "INSERT INTO children VALUES (1);", SQLDown: "DELETE FROM children WHERE parent_id = 1;",
	})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := service.ExecuteMigration(ctx, actor, "db", draft.ID)
	if !errors.Is(err, migration.ErrExecutionFailed) || failed == nil || failed.Run.Status != migration.RunStatusFailed {
		t.Fatalf("confirmed database rollback should close the attempt as failed: %+v, %v", failed, err)
	}
	execSQL(t, targetDB, `INSERT INTO parents VALUES (1)`)
	if _, err := service.ExecuteMigration(ctx, actor, "db", draft.ID); err != nil {
		t.Fatalf("a known rollback should release the reservation for retry: %v", err)
	}
}
