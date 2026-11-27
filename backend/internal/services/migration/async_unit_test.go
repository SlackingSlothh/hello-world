package migration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"hello-world/internal/observability"
)

type blockingTargetTx struct {
	databaseID string
	onExec     func(databaseID string)
}

func (t *blockingTargetTx) Dialect() TargetDialect            { return DialectPostgres }
func (t *blockingTargetTx) AcquireTargetLock(context.Context, string) error { return nil }
func (t *blockingTargetTx) ExecSQL(context.Context, string) error {
	if t.onExec != nil {
		t.onExec(t.databaseID)
	}
	return nil
}
func (t *blockingTargetTx) ExplainSQL(context.Context, string, bool) (string, error) { return "", nil }
func (t *blockingTargetTx) InspectSchema(context.Context, string) ([]TableSchema, error) {
	return nil, nil
}
func (t *blockingTargetTx) Commit(context.Context) error   { return nil }
func (t *blockingTargetTx) Rollback(context.Context) error { return nil }

type blockingTargetConnector struct {
	onExec func(databaseID string)
}

func (c *blockingTargetConnector) BeginTargetTx(_ context.Context, databaseID string) (TargetTx, error) {
	return &blockingTargetTx{databaseID: databaseID, onExec: c.onExec}, nil
}

func TestAsyncParallelTwoDatabasesVsSequentialOneDatabase(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	editor := Principal{UserID: "editor-1", Username: "andi"}
	authz.Grant(editor.UserID, "db-1", "VIEW_EDIT")
	authz.Grant(editor.UserID, "db-2", "VIEW_EDIT")

	store := newWorkerTestStore()
	locks := newFakeLockStore()
	jobs := newClaimableJobStore(locks)

	startedDB1 := make(chan struct{}, 2)
	startedDB2 := make(chan struct{}, 2)
	releaseDB1 := make(chan struct{})
	releaseDB2 := make(chan struct{})

	var mu sync.Mutex
	activeOnDB1 := 0
	maxConcurrentDB1 := 0
	overlapCrossDB := false
	activeDBs := map[string]bool{}

	connector := &blockingTargetConnector{
		onExec: func(databaseID string) {
			mu.Lock()
			activeDBs[databaseID] = true
			if activeDBs["db-1"] && activeDBs["db-2"] {
				overlapCrossDB = true
			}
			if databaseID == "db-1" {
				activeOnDB1++
				if activeOnDB1 > maxConcurrentDB1 {
					maxConcurrentDB1 = activeOnDB1
				}
			}
			mu.Unlock()

			if databaseID == "db-1" {
				startedDB1 <- struct{}{}
				<-releaseDB1
			} else {
				startedDB2 <- struct{}{}
				<-releaseDB2
			}

			mu.Lock()
			delete(activeDBs, databaseID)
			if databaseID == "db-1" {
				activeOnDB1--
			}
			mu.Unlock()
		},
	}

	svc := NewService(authz, store, connector, nil)
	svc.ConfigureAsync(jobs, locks, AllowAllApprovals{}, observability.NewMetrics())

	m1, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID: "db-1", Title: "DB1 First", SQLContent: "CREATE TABLE a1 (id INT);", IrreversibleReason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID: "db-1", Title: "DB1 Second", SQLContent: "CREATE TABLE a2 (id INT);", IrreversibleReason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	m3, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID: "db-2", Title: "DB2 First", SQLContent: "CREATE TABLE b1 (id INT);", IrreversibleReason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	j1, err := svc.EnqueueExecute(ctx, editor, "db-1", m1.ID)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := svc.EnqueueExecute(ctx, editor, "db-1", m2.ID)
	if err != nil {
		t.Fatal(err)
	}
	j3, err := svc.EnqueueExecute(ctx, editor, "db-2", m3.ID)
	if err != nil {
		t.Fatal(err)
	}

	pool := NewWorkerPool(svc, newFakeBatchConnector(1), DefaultWorkerConfig())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = pool.ProcessOne(ctx, "worker-1")
	}()
	<-startedDB1

	go func() {
		defer wg.Done()
		_, _ = pool.ProcessOne(ctx, "worker-2")
	}()
	<-startedDB2

	processedWhileLocked, err := pool.ProcessOne(ctx, "worker-3")
	if err != nil {
		t.Fatal(err)
	}
	if processedWhileLocked {
		t.Fatal("expected second db-1 job to wait while db-1 lock is held")
	}

	close(releaseDB1)
	close(releaseDB2)
	wg.Wait()

	processedNext, err := pool.ProcessOne(ctx, "worker-3")
	if err != nil || !processedNext {
		t.Fatalf("expected queued second db-1 job to run sequentially after release, processed=%v err=%v", processedNext, err)
	}

	if !overlapCrossDB {
		t.Fatal("expected db-1 and db-2 jobs to execute in parallel across workers")
	}
	if maxConcurrentDB1 != 1 {
		t.Fatalf("expected max concurrency 1 on db-1, got %d", maxConcurrentDB1)
	}

	for _, id := range []string{j1.JobID, j2.JobID, j3.JobID} {
		got, err := jobs.GetJob(ctx, id)
		if err != nil || got.Status != JobStatusSuccess {
			t.Fatalf("expected job %s success, got %+v err=%v", id, got, err)
		}
	}
}

func TestAsyncViewOnlyAccessDenied(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	editor := Principal{UserID: "editor-1", Username: "andi"}
	viewer := Principal{UserID: "viewer-1", Username: "budi"}
	authz.Grant(editor.UserID, "db-1", "VIEW_EDIT")
	authz.Grant(viewer.UserID, "db-1", "VIEW_ONLY")

	store := newWorkerTestStore()
	locks := newFakeLockStore()
	jobs := newClaimableJobStore(locks)
	svc := NewService(authz, store, &mockTargetDB{}, nil)
	svc.ConfigureAsync(jobs, locks, AllowAllApprovals{}, observability.NewMetrics())

	batchSize := 100
	draft, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID:       "db-1",
		Title:            "Batch RBAC",
		SQLContent:       "UPDATE items SET status = 'ok' WHERE id > $1 AND id <= $2",
		SQLDown:          "UPDATE items SET status = 'old' WHERE id >= $1 AND id <= $2",
		ExecutionMode:    ModeBatch,
		BatchByAttribute: "id",
		BatchSize:        &batchSize,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.EnqueueExecute(ctx, viewer, "db-1", draft.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for viewer EnqueueExecute, got %v", err)
	}

	enq, err := svc.EnqueueExecute(ctx, editor, "db-1", draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	batchConn := newFakeBatchConnector(3)
	batchConn.failAtBatch = 2
	pool := NewWorkerPool(svc, batchConn, DefaultWorkerConfig())
	if _, err := pool.ProcessOne(ctx, "worker-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.EnqueueResumeBatch(ctx, viewer, "db-1", enq.JobID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for viewer EnqueueResumeBatch, got %v", err)
	}
	if _, err := svc.EnqueueRevertBatch(ctx, viewer, "db-1", enq.JobID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for viewer EnqueueRevertBatch, got %v", err)
	}

	listed, err := svc.ListJobsByDatabase(ctx, viewer, "db-1", 20, 0)
	if err != nil || len(listed) != 1 {
		t.Fatalf("expected viewer to read job list, len=%d err=%v", len(listed), err)
	}
	got, err := svc.GetJob(ctx, viewer, enq.JobID)
	if err != nil || got.JobID != enq.JobID {
		t.Fatalf("expected viewer to read job detail, got=%+v err=%v", got, err)
	}
}

func TestAsyncNormalCrashRecoveryBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	editor := Principal{UserID: "editor-1", Username: "andi"}
	authz.Grant(editor.UserID, "db-crash", "VIEW_EDIT")

	store := newWorkerTestStore()
	locks := newFakeLockStore()
	jobs := newClaimableJobStore(locks)
	now := time.Date(2026, 11, 27, 8, 0, 0, 0, time.UTC)

	svc := NewService(authz, store, &mockTargetDB{}, nil)
	svc.SetClock(func() time.Time { return now })
	svc.ConfigureAsync(jobs, locks, AllowAllApprovals{}, observability.NewMetrics())

	draft, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID:         "db-crash",
		Title:              "Pre-dispatch crash",
		SQLContent:         "CREATE TABLE t_pre (id INT);",
		IrreversibleReason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	enq, err := svc.EnqueueExecute(ctx, editor, "db-crash", draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, _, err := jobs.ClaimNextJob(ctx, "dead-worker", 10*time.Second, now)
	if err != nil || claimed == nil {
		t.Fatalf("failed to claim job: %v", err)
	}

	dispatchStarted := false
	run := &MigrationRun{
		MigrationID:        draft.ID,
		DatabaseID:         "db-crash",
		JobID:              &enq.JobID,
		ExecutionUUID:      &enq.ExecutionID,
		Revision:           draft.Revision,
		Operation:          RunApply,
		SQLSnapshot:        draft.SQLContent,
		SQLDispatchStarted: &dispatchStarted,
		Status:             RunStatusRunning,
		ExecutedBy:         editor.UserID,
		StartedAt:          now,
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	pool := NewWorkerPool(svc, newFakeBatchConnector(1), DefaultWorkerConfig())
	now = now.Add(20 * time.Second)
	if _, err := pool.RunRecoverySweep(ctx); err != nil {
		t.Fatal(err)
	}

	recovered, err := jobs.GetJob(ctx, enq.JobID)
	if err != nil || recovered.Status != JobStatusPending {
		t.Fatalf("expected pre-dispatch crashed job requeued as pending, got %+v err=%v", recovered, err)
	}
	if _, err := locks.GetLock(ctx, "db-crash"); !errors.Is(err, ErrLockNotFound) {
		t.Fatalf("expected lock released after pre-dispatch recovery, got %v", err)
	}

	runs, err := store.ListRunsByMigration(ctx, draft.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != RunStatusFailed {
		t.Fatalf("expected pre-dispatch run closed as FAILED, got %+v err=%v", runs, err)
	}
}

func TestAsyncBatchPartiallyRevertedRecovery(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	editor := Principal{UserID: "editor-1", Username: "andi"}
	authz.Grant(editor.UserID, "db-rev", "VIEW_EDIT")

	store := newWorkerTestStore()
	locks := newFakeLockStore()
	jobs := newClaimableJobStore(locks)
	svc := NewService(authz, store, &mockTargetDB{}, nil)
	svc.ConfigureAsync(jobs, locks, AllowAllApprovals{}, observability.NewMetrics())

	batchSize := 100
	draft, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID:       "db-rev",
		Title:            "Partial revert retry",
		SQLContent:       "UPDATE items SET v = 2 WHERE id > $1 AND id <= $2",
		SQLDown:          "UPDATE items SET v = 1 WHERE id >= $1 AND id <= $2",
		ExecutionMode:    ModeBatch,
		BatchByAttribute: "id",
		BatchSize:        &batchSize,
	})
	if err != nil {
		t.Fatal(err)
	}

	batchConn := newFakeBatchConnector(3)
	batchConn.failAtBatch = 3
	pool := NewWorkerPool(svc, batchConn, DefaultWorkerConfig())

	enq, err := svc.EnqueueExecute(ctx, editor, "db-rev", draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ProcessOne(ctx, "worker-1"); err != nil {
		t.Fatal(err)
	}

	batchConn.failRevertAt = 1
	rev1, err := svc.EnqueueRevertBatch(ctx, editor, "db-rev", enq.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ProcessOne(ctx, "worker-1"); err != nil {
		t.Fatal(err)
	}

	gotRev1, err := jobs.GetJob(ctx, rev1.JobID)
	if err != nil || gotRev1.Status != JobStatusPartiallyReverted {
		t.Fatalf("expected partially_reverted after mid-revert failure, got %+v err=%v", gotRev1, err)
	}
	lock, err := locks.GetLock(ctx, "db-rev")
	if err != nil || lock.LockState != LockStateHeldPartial {
		t.Fatalf("expected lock held_partial after partially_reverted, got %+v err=%v", lock, err)
	}

	batchConn.failRevertAt = 0
	rev2, err := svc.EnqueueRevertBatch(ctx, editor, "db-rev", rev1.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ProcessOne(ctx, "worker-1"); err != nil {
		t.Fatal(err)
	}

	gotRev2, err := jobs.GetJob(ctx, rev2.JobID)
	if err != nil || gotRev2.Status != JobStatusSuccess {
		t.Fatalf("expected second revert_batch to succeed, got %+v err=%v", gotRev2, err)
	}
	if _, err := locks.GetLock(ctx, "db-rev"); !errors.Is(err, ErrLockNotFound) {
		t.Fatalf("expected lock released after full revert completion, got %v", err)
	}
}
