package migration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"hello-world/internal/observability"
)

type fakeLockStore struct {
	mu    sync.Mutex
	locks map[string]*DatabaseLock
}

func newFakeLockStore() *fakeLockStore {
	return &fakeLockStore{locks: make(map[string]*DatabaseLock)}
}

func (s *fakeLockStore) AcquireLock(_ context.Context, lock DatabaseLock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.locks[lock.DatabaseID]; exists {
		return ErrLockConflict
	}
	copied := lock
	s.locks[lock.DatabaseID] = &copied
	return nil
}

func (s *fakeLockStore) TakeoverPartialLock(_ context.Context, databaseID, jobID, executionID, workerID string, acquiredAt, leaseExpiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[databaseID]
	if !ok || l.LockState != LockStateHeldPartial {
		return ErrLockConflict
	}
	l.JobID = jobID
	l.ExecutionID = executionID
	l.WorkerID = workerID
	l.LockState = LockStateActive
	l.AcquiredAt = acquiredAt
	l.LeaseExpiresAt = leaseExpiresAt
	return nil
}

func (s *fakeLockStore) GetLock(_ context.Context, databaseID string) (*DatabaseLock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[databaseID]
	if !ok {
		return nil, ErrLockNotFound
	}
	copied := *l
	return &copied, nil
}

func (s *fakeLockStore) RenewLease(_ context.Context, databaseID, jobID, workerID string, leaseExpiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[databaseID]
	if !ok || l.JobID != jobID || l.WorkerID != workerID || l.LockState != LockStateActive {
		return ErrLockNotFound
	}
	l.LeaseExpiresAt = leaseExpiresAt
	return nil
}

func (s *fakeLockStore) TransitionLockState(_ context.Context, databaseID, jobID string, newState LockState, executionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[databaseID]
	if !ok || l.JobID != jobID {
		return ErrLockNotFound
	}
	l.LockState = newState
	if executionID != "" {
		l.ExecutionID = executionID
	}
	return nil
}

func (s *fakeLockStore) ReleaseLock(_ context.Context, databaseID, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.locks[databaseID]; ok && l.JobID == jobID {
		delete(s.locks, databaseID)
	}
	return nil
}

func (s *fakeLockStore) SweepExpiredLeases(_ context.Context, now time.Time) ([]DatabaseLock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []DatabaseLock
	for _, l := range s.locks {
		if l.LockState == LockStateActive && l.LeaseExpiresAt.Before(now) {
			l.LockState = LockStateHeldUncertain
			expired = append(expired, *l)
		}
	}
	return expired, nil
}

func (s *fakeLockStore) ListUncertainLocks(_ context.Context) ([]DatabaseLock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []DatabaseLock
	for _, l := range s.locks {
		if l.LockState == LockStateHeldUncertain {
			out = append(out, *l)
		}
	}
	return out, nil
}

type claimableJobStore struct {
	*inMemoryJobStore
	locks *fakeLockStore
	order []string
}

func newClaimableJobStore(locks *fakeLockStore) *claimableJobStore {
	return &claimableJobStore{
		inMemoryJobStore: newInMemoryJobStore(),
		locks:            locks,
	}
}

func (s *claimableJobStore) EnqueueJob(ctx context.Context, job *Job) error {
	if err := s.inMemoryJobStore.EnqueueJob(ctx, job); err != nil {
		return err
	}
	s.mu.Lock()
	s.order = append(s.order, job.JobID)
	s.mu.Unlock()
	return nil
}

func (s *claimableJobStore) ClaimNextJob(ctx context.Context, workerID string, leaseDuration time.Duration, now time.Time) (*Job, *DatabaseLock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		j := s.jobs[id]
		if j == nil || j.Status != JobStatusPending {
			continue
		}
		if j.NextRetryAt != nil && j.NextRetryAt.After(now) {
			continue
		}
		var lock *DatabaseLock
		if j.Action == JobActionResumeBatch || j.Action == JobActionRevertBatch {
			execID := j.ExecutionID
			if j.ParentExecutionID != nil && *j.ParentExecutionID != "" {
				execID = *j.ParentExecutionID
			}
			if err := s.locks.TakeoverPartialLock(ctx, j.DatabaseID, j.JobID, execID, workerID, now, now.Add(leaseDuration)); err != nil {
				continue
			}
			lock, _ = s.locks.GetLock(ctx, j.DatabaseID)
		} else {
			newLock := DatabaseLock{
				DatabaseID:     j.DatabaseID,
				JobID:          j.JobID,
				ExecutionID:    j.ExecutionID,
				WorkerID:       workerID,
				LockState:      LockStateActive,
				AcquiredAt:     now,
				LeaseExpiresAt: now.Add(leaseDuration),
			}
			if err := s.locks.AcquireLock(ctx, newLock); err != nil {
				continue
			}
			lock = &newLock
		}
		j.Status = JobStatusRunning
		j.Attempt++
		if j.StartedAt == nil {
			t := now
			j.StartedAt = &t
		}
		j.NextRetryAt = nil
		j.UpdatedAt = now
		copied := *j
		return &copied, lock, nil
	}
	return nil, nil, nil
}

type fakeBatchConnector struct {
	mu           sync.Mutex
	totalBatches int
	failAtBatch  int
	failRevertAt int
	checkpoints  map[string][]BatchCheckpoint
	revertedSeq  []int
}

func newFakeBatchConnector(totalBatches int) *fakeBatchConnector {
	return &fakeBatchConnector{
		totalBatches: totalBatches,
		checkpoints:  make(map[string][]BatchCheckpoint),
	}
}

func (f *fakeBatchConnector) EnsureCheckpointSchema(context.Context, string) error { return nil }

func (f *fakeBatchConnector) ResolveTargetScopeMax(context.Context, string, string, string) (string, error) {
	return fmt.Sprintf("%d", f.totalBatches*100), nil
}

func (f *fakeBatchConnector) ExecuteBatchStep(_ context.Context, _ string, req BatchStepRequest) (BatchStepResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := req.ExecutionID + ":up"
	for _, cp := range f.checkpoints[key] {
		if cp.BatchNumber == req.BatchNumber {
			return BatchStepResult{Checkpoint: cp, AlreadyCommitted: true}, nil
		}
	}
	if req.BatchNumber > f.totalBatches {
		return BatchStepResult{Done: true}, nil
	}
	if f.failAtBatch > 0 && req.BatchNumber == f.failAtBatch {
		return BatchStepResult{}, errors.New("simulated batch execution failure")
	}
	cp := BatchCheckpoint{
		ExecutionID:  req.ExecutionID,
		BatchNumber:  req.BatchNumber,
		Direction:    DirectionUp,
		CursorStart:  fmt.Sprintf("%d", (req.BatchNumber-1)*100+1),
		CursorEnd:    fmt.Sprintf("%d", req.BatchNumber*100),
		RowsAffected: 100,
		CommittedAt:  time.Now().UTC(),
	}
	f.checkpoints[key] = append(f.checkpoints[key], cp)
	return BatchStepResult{Checkpoint: cp}, nil
}

func (f *fakeBatchConnector) RevertBatchStep(_ context.Context, _ string, req RevertStepRequest) (BatchStepResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := req.ExecutionID + ":down"
	for _, cp := range f.checkpoints[key] {
		if cp.BatchNumber == req.Checkpoint.BatchNumber {
			return BatchStepResult{Checkpoint: cp, AlreadyCommitted: true}, nil
		}
	}
	if f.failRevertAt > 0 && req.Checkpoint.BatchNumber == f.failRevertAt {
		return BatchStepResult{}, errors.New("simulated revert failure")
	}
	cp := BatchCheckpoint{
		ExecutionID:  req.ExecutionID,
		BatchNumber:  req.Checkpoint.BatchNumber,
		Direction:    DirectionDown,
		CursorStart:  req.Checkpoint.CursorStart,
		CursorEnd:    req.Checkpoint.CursorEnd,
		RowsAffected: req.Checkpoint.RowsAffected,
		CommittedAt:  time.Now().UTC(),
	}
	f.checkpoints[key] = append(f.checkpoints[key], cp)
	f.revertedSeq = append(f.revertedSeq, req.Checkpoint.BatchNumber)
	return BatchStepResult{Checkpoint: cp}, nil
}

func (f *fakeBatchConnector) ListCheckpoints(_ context.Context, _, executionID string, direction CheckpointDirection) ([]BatchCheckpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := executionID + ":" + string(direction)
	out := make([]BatchCheckpoint, len(f.checkpoints[key]))
	copy(out, f.checkpoints[key])
	return out, nil
}

func (f *fakeBatchConnector) ExecNormalTx(context.Context, string, string) (int64, error) {
	return 1, nil
}

type transientPGError struct {
	msg  string
	code string
}

func (e transientPGError) Error() string    { return e.msg }
func (e transientPGError) SQLState() string { return e.code }

func TestWorkerBatchPartialResumeAndCompensatingRevert(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	editor := Principal{UserID: "editor-1", Username: "andi"}
	authz.Grant(editor.UserID, "db-1", "VIEW_EDIT")

	store := newWorkerTestStore()
	locks := newFakeLockStore()
	jobs := newClaimableJobStore(locks)
	metrics := observability.NewMetrics()
	svc := NewService(authz, store, &mockTargetDB{}, nil)
	svc.ConfigureAsync(jobs, locks, AllowAllApprovals{}, metrics)

	batchSize := 100
	draft, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID:       "db-1",
		Title:            "Large batch update",
		SQLContent:       "UPDATE items SET v = 2 WHERE id >= $1 AND id <= $2",
		SQLDown:          "UPDATE items SET v = 1 WHERE id >= $1 AND id <= $2",
		ExecutionMode:    ModeBatch,
		BatchByAttribute: "id",
		BatchSize:        &batchSize,
	})
	if err != nil {
		t.Fatal(err)
	}

	batchConn := newFakeBatchConnector(3)
	batchConn.failAtBatch = 2
	pool := NewWorkerPool(svc, batchConn, DefaultWorkerConfig())

	enq, err := svc.EnqueueExecute(ctx, editor, "db-1", draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := pool.ProcessOne(ctx, "worker-1")
	if err != nil || !processed {
		t.Fatalf("expected job processed, processed=%t err=%v", processed, err)
	}

	partialJob, err := jobs.GetJob(ctx, enq.JobID)
	if err != nil || partialJob.Status != JobStatusPartial || partialJob.LastCommittedBatch != 1 || partialJob.CommittedRows != 100 {
		t.Fatalf("expected partial job after batch 1 committed, got %+v err=%v", partialJob, err)
	}
	lock, err := locks.GetLock(ctx, "db-1")
	if err != nil || lock.LockState != LockStateHeldPartial {
		t.Fatalf("expected held_partial lock blocking db-1, got %+v err=%v", lock, err)
	}

	batchConn.failAtBatch = 0
	resumed, err := svc.EnqueueResumeBatch(ctx, editor, "db-1", enq.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ProcessOne(ctx, "worker-2"); err != nil {
		t.Fatal(err)
	}
	doneJob, err := jobs.GetJob(ctx, resumed.JobID)
	if err != nil || doneJob.Status != JobStatusSuccess || doneJob.LastCommittedBatch != 3 || doneJob.CommittedRows != 300 {
		t.Fatalf("expected resumed batch job succeeded with 300 rows, got %+v err=%v", doneJob, err)
	}
	if _, err := locks.GetLock(ctx, "db-1"); !errors.Is(err, ErrLockNotFound) {
		t.Fatalf("expected lock released after full completion, got %v", err)
	}

	draft2, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID:       "db-1",
		Title:            "Batch to revert",
		SQLContent:       "UPDATE items SET v = 3 WHERE id >= $1 AND id <= $2",
		SQLDown:          "UPDATE items SET v = 2 WHERE id >= $1 AND id <= $2",
		ExecutionMode:    ModeBatch,
		BatchByAttribute: "id",
		BatchSize:        &batchSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	batchConn2 := newFakeBatchConnector(3)
	batchConn2.failAtBatch = 3
	pool2 := NewWorkerPool(svc, batchConn2, DefaultWorkerConfig())

	enq2, err := svc.EnqueueExecute(ctx, editor, "db-1", draft2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool2.ProcessOne(ctx, "worker-1"); err != nil {
		t.Fatal(err)
	}

	batchConn2.failRevertAt = 1
	rev1, err := svc.EnqueueRevertBatch(ctx, editor, "db-1", enq2.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool2.ProcessOne(ctx, "worker-2"); err != nil {
		t.Fatal(err)
	}
	partRev, err := jobs.GetJob(ctx, rev1.JobID)
	if err != nil || partRev.Status != JobStatusPartiallyReverted {
		t.Fatalf("expected partially_reverted after batch 1 revert failure, got %+v err=%v", partRev, err)
	}
	lock2, err := locks.GetLock(ctx, "db-1")
	if err != nil || lock2.LockState != LockStateHeldPartial {
		t.Fatalf("expected held_partial lock during partially_reverted, got %+v err=%v", lock2, err)
	}

	batchConn2.failRevertAt = 0
	rev2, err := svc.EnqueueRevertBatch(ctx, editor, "db-1", rev1.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool2.ProcessOne(ctx, "worker-3"); err != nil {
		t.Fatal(err)
	}
	finalRev, err := jobs.GetJob(ctx, rev2.JobID)
	if err != nil || finalRev.Status != JobStatusSuccess {
		t.Fatalf("expected revert completion, got %+v err=%v", finalRev, err)
	}
	if len(batchConn2.revertedSeq) != 2 || batchConn2.revertedSeq[0] != 2 || batchConn2.revertedSeq[1] != 1 {
		t.Fatalf("expected reverse batch revert order [2, 1], got %v", batchConn2.revertedSeq)
	}
	if _, err := locks.GetLock(ctx, "db-1"); !errors.Is(err, ErrLockNotFound) {
		t.Fatalf("expected lock released after full revert, got %v", err)
	}
}

type workerTestStore struct {
	*inMemoryStore
	evidenceByRun map[int64]TransactionEvidence
}

func newWorkerTestStore() *workerTestStore {
	return &workerTestStore{
		inMemoryStore: newInMemoryStore(),
		evidenceByRun: make(map[int64]TransactionEvidence),
	}
}

func (s *workerTestStore) CreateRun(_ context.Context, run *MigrationRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.migrations[run.MigrationID]
	if !ok {
		return ErrNotFound
	}
	expectedSQL := m.SQLContent
	if run.Operation == RunRollback || run.Operation == RunRevertBatch {
		expectedSQL = m.SQLDown
	}
	if m.Revision != run.Revision || expectedSQL != run.SQLSnapshot {
		return ErrRevisionChanged
	}
	if m.FrozenAt == nil {
		m.FrozenAt = &run.StartedAt
	}
	run.AttemptNumber = len(s.runsByMigID[run.MigrationID]) + 1
	run.ID = s.nextRunID
	s.nextRunID++
	run.SQLDispatchStarted = new(false)
	cp := *run
	s.runsByMigID[run.MigrationID] = append(s.runsByMigID[run.MigrationID], cp)
	return nil
}

func (s *workerTestStore) MarkSQLDispatchStarted(ctx context.Context, runID int64, evidence TransactionEvidence) error {
	if err := s.inMemoryStore.MarkSQLDispatchStarted(ctx, runID, evidence); err != nil {
		return err
	}
	s.mu.Lock()
	s.evidenceByRun[runID] = evidence
	s.mu.Unlock()
	return nil
}

func (s *workerTestStore) GetRunForReconciliation(ctx context.Context, databaseID string, runID int64) (MigrationRun, TransactionEvidence, error) {
	run, _, err := s.inMemoryStore.GetRunForReconciliation(ctx, databaseID, runID)
	if err != nil {
		return MigrationRun{}, TransactionEvidence{}, err
	}
	s.mu.Lock()
	ev := s.evidenceByRun[runID]
	s.mu.Unlock()
	return run, ev, nil
}

type verifyingTargetDB struct {
	*mockTargetDB
	outcome TransactionOutcome
}

func (v *verifyingTargetDB) VerifyTransaction(context.Context, string, TransactionEvidence) (TransactionOutcome, error) {
	return v.outcome, nil
}

func TestWorkerCrashRecoveryBatchAndNormal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	authz := newMockAuthorizer()
	editor := Principal{UserID: "editor-1", Username: "andi"}
	authz.Grant(editor.UserID, "db-batch", "VIEW_EDIT")
	authz.Grant(editor.UserID, "db-norm", "VIEW_EDIT")

	store := newWorkerTestStore()
	locks := newFakeLockStore()
	jobs := newClaimableJobStore(locks)
	evidence := TransactionEvidence{XactID: "9001", TargetIdentity: "sys:1:00000001:100:200"}
	targetDB := &verifyingTargetDB{
		mockTargetDB: &mockTargetDB{},
		outcome:      TransactionCommitted,
	}
	svc := NewService(authz, store, targetDB, nil)
	svc.SetClock(func() time.Time { return now })
	svc.ConfigureAsync(jobs, locks, AllowAllApprovals{}, observability.NewMetrics())

	batchSize := 100
	draftBatch, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID:       "db-batch",
		Title:            "Crash batch",
		SQLContent:       "UPDATE items SET v = 2 WHERE id >= $1 AND id <= $2",
		SQLDown:          "UPDATE items SET v = 1 WHERE id >= $1 AND id <= $2",
		ExecutionMode:    ModeBatch,
		BatchByAttribute: "id",
		BatchSize:        &batchSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	batchConn := newFakeBatchConnector(2)
	pool := NewWorkerPool(svc, batchConn, DefaultWorkerConfig())

	enqB, err := svc.EnqueueExecute(ctx, editor, "db-batch", draftBatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimedB, _, err := jobs.ClaimNextJob(ctx, "dead-worker", 15*time.Second, now)
	if err != nil || claimedB == nil {
		t.Fatal("failed to claim batch job")
	}
	batchConn.checkpoints[enqB.ExecutionID+":up"] = []BatchCheckpoint{{
		ExecutionID: enqB.ExecutionID, BatchNumber: 1, Direction: DirectionUp,
		CursorStart: "1", CursorEnd: "100", RowsAffected: 100, CommittedAt: now,
	}}

	draftNorm, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID:         "db-norm",
		Title:              "Crash normal",
		SQLContent:         "CREATE TABLE t_ok (id INT);",
		IrreversibleReason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	enqN, err := svc.EnqueueExecute(ctx, editor, "db-norm", draftNorm.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimedN, _, err := jobs.ClaimNextJob(ctx, "dead-worker", 15*time.Second, now)
	if err != nil || claimedN == nil {
		t.Fatal("failed to claim normal job")
	}
	runN := &MigrationRun{
		MigrationID:   draftNorm.ID,
		DatabaseID:    "db-norm",
		JobID:         &enqN.JobID,
		ExecutionUUID: &enqN.ExecutionID,
		Revision:      draftNorm.Revision,
		Operation:     RunApply,
		SQLSnapshot:   draftNorm.SQLContent,
		Status:        RunStatusRunning,
		ExecutedBy:    editor.UserID,
		StartedAt:     now,
	}
	if err := store.CreateRun(ctx, runN); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSQLDispatchStarted(ctx, runN.ID, evidence); err != nil {
		t.Fatal(err)
	}

	now = now.Add(30 * time.Second)
	count, err := pool.RunRecoverySweep(ctx)
	if err != nil || count == 0 {
		t.Fatalf("expected expired locks swept and reconciled, count=%d err=%v", count, err)
	}

	recoveredB, err := jobs.GetJob(ctx, enqB.JobID)
	if err != nil || recoveredB.Status != JobStatusPending || recoveredB.LastCommittedBatch != 1 || recoveredB.CommittedRows != 100 {
		t.Fatalf("expected batch job synced to batch 1 and requeued, got %+v err=%v", recoveredB, err)
	}

	recoveredN, err := jobs.GetJob(ctx, enqN.JobID)
	if err != nil || recoveredN.Status != JobStatusSuccess {
		t.Fatalf("expected normal job reconciled as success via VerifyTransaction, got %+v err=%v", recoveredN, err)
	}
	if _, err := locks.GetLock(ctx, "db-norm"); !errors.Is(err, ErrLockNotFound) {
		t.Fatalf("expected db-norm lock released after committed verification, got %v", err)
	}
}

func TestWorkerTransientRetryAndRevalidationFailure(t *testing.T) {
	ctx := context.Background()
	authz := newMockAuthorizer()
	editor := Principal{UserID: "editor-1", Username: "andi"}
	authz.Grant(editor.UserID, "db-1", "VIEW_EDIT")

	store := newWorkerTestStore()
	locks := newFakeLockStore()
	jobs := newClaimableJobStore(locks)
	approved := true
	checker := ApprovalCheckerFunc(func(context.Context, string, int64, JobAction) error {
		if !approved {
			return ErrNotApproved
		}
		return nil
	})
	targetDB := &mockTargetDB{shouldFailSQL: true, failErrMsg: "deadlock detected"}
	svc := NewService(authz, store, targetDB, nil)
	svc.ConfigureAsync(jobs, locks, checker, observability.NewMetrics())

	draft, err := svc.CreateDraft(ctx, editor, CreateDraftInput{
		DatabaseID:         "db-1",
		Title:              "Revoked approval test",
		SQLContent:         "CREATE TABLE t1 (id INT);",
		IrreversibleReason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	enq, err := svc.EnqueueExecute(ctx, editor, "db-1", draft.ID)
	if err != nil {
		t.Fatal(err)
	}

	approved = false
	pool := NewWorkerPool(svc, newFakeBatchConnector(1), DefaultWorkerConfig())
	if _, err := pool.ProcessOne(ctx, "worker-1"); err != nil {
		t.Fatal(err)
	}
	failedJob, err := jobs.GetJob(ctx, enq.JobID)
	if err != nil || failedJob.Status != JobStatusFailed {
		t.Fatalf("expected job failed after post-lock approval revocation, got %+v err=%v", failedJob, err)
	}
	if _, err := locks.GetLock(ctx, "db-1"); !errors.Is(err, ErrLockNotFound) {
		t.Fatalf("expected lock released after revalidation failure, got %v", err)
	}

	if !isTransientError(transientPGError{msg: "deadlock", code: "40P01"}, "40P01") {
		t.Fatal("expected SQLSTATE 40P01 to be classified as transient")
	}
	if isTransientError(ErrInvalidSQL, "42601") {
		t.Fatal("expected syntax error 42601 not to be classified as transient")
	}
}
