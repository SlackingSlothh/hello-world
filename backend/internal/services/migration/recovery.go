package migration

import (
	"context"
	"time"
)

func (w *WorkerPool) RunRecoverySweep(ctx context.Context) (int, error) {
	if w.service == nil || w.service.lockStore == nil || w.service.jobStore == nil {
		return 0, ErrAsyncUnavailable
	}
	now := w.service.nowFn()
	expired, err := w.service.lockStore.SweepExpiredLeases(ctx, now)
	if err != nil {
		return 0, err
	}
	for _, lock := range expired {
		w.service.logAudit(ctx, AuditEvent{
			Action:        "lock.lease_expired",
			ObjectType:    "database_lock",
			DatabaseID:    lock.DatabaseID,
			JobID:         lock.JobID,
			ExecutionUUID: lock.ExecutionID,
			Status:        "UNCERTAIN",
			CreatedAt:     now,
		})
	}

	uncertainLocks, err := w.service.lockStore.ListUncertainLocks(ctx)
	if err != nil {
		return len(expired), err
	}
	reconciled := 0
	for _, lock := range uncertainLocks {
		if ok := w.reconcileUncertainLock(ctx, lock); ok {
			reconciled++
		}
	}
	return len(expired) + reconciled, nil
}

func (w *WorkerPool) reconcileUncertainLock(ctx context.Context, lock DatabaseLock) bool {
	job, err := w.service.jobStore.GetJob(ctx, lock.JobID)
	if err != nil {
		return false
	}
	if job.Mode == ModeBatch && w.batchConnector != nil {
		return w.reconcileUncertainBatchJob(ctx, lock, job)
	}
	return w.reconcileUncertainNormalJob(ctx, lock, job)
}

func (w *WorkerPool) reconcileUncertainNormalJob(ctx context.Context, lock DatabaseLock, job *Job) bool {
	runs, err := w.service.store.ListRunsByMigration(ctx, job.MigrationID)
	if err != nil {
		return false
	}
	var matched *MigrationRun
	for i := range runs {
		if (runs[i].JobID != nil && *runs[i].JobID == job.JobID) ||
			(runs[i].ExecutionUUID != nil && *runs[i].ExecutionUUID == job.ExecutionID) {
			matched = &runs[i]
			break
		}
	}

	now := w.service.nowFn()
	if matched == nil {
		return w.rescheduleOrFailNormal(ctx, lock, job, "worker crashed before recording run", now)
	}
	if matched.Status == RunStatusSucceeded {
		_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusSuccess, nil, nil, now)
		_ = w.service.lockStore.ReleaseLock(ctx, lock.DatabaseID, lock.JobID)
		return true
	}
	if matched.Status == RunStatusFailed {
		msg := "previous attempt failed"
		if matched.ErrorMessage != nil {
			msg = *matched.ErrorMessage
		}
		return w.rescheduleOrFailNormal(ctx, lock, job, msg, now)
	}

	run, evidence, err := w.service.store.GetRunForReconciliation(ctx, job.DatabaseID, matched.ID)
	if err != nil {
		return false
	}
	if run.SQLDispatchStarted != nil && !*run.SQLDispatchStarted {
		failed, err := w.service.store.FailRunBeforeDispatch(ctx, run.ID, run.MigrationID, "worker crashed before SQL dispatch", now)
		if err != nil {
			return false
		}
		if failed {
			return w.rescheduleOrFailNormal(ctx, lock, job, "worker crashed before SQL dispatch", now)
		}
		run, evidence, err = w.service.store.GetRunForReconciliation(ctx, job.DatabaseID, matched.ID)
		if err != nil {
			return false
		}
		if run.Status == RunStatusSucceeded {
			_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusSuccess, nil, nil, now)
			_ = w.service.lockStore.ReleaseLock(ctx, lock.DatabaseID, lock.JobID)
			return true
		}
		if run.Status == RunStatusFailed {
			return w.rescheduleOrFailNormal(ctx, lock, job, "previous attempt failed", now)
		}
	}

	verifier, ok := w.service.connector.(targetTransactionVerifier)
	if !ok || evidence.XactID == "" || evidence.TargetIdentity == "" {
		return false
	}
	outcome, err := verifier.VerifyTransaction(ctx, job.DatabaseID, evidence)
	if err != nil {
		return false
	}
	switch outcome {
	case TransactionCommitted:
		_, err := w.service.store.CompleteRunSucceeded(ctx, run.ID, run.MigrationID, job.DatabaseID, run.ExecutionMs, now)
		if err != nil {
			return false
		}
		_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusSuccess, nil, nil, now)
		_ = w.service.lockStore.ReleaseLock(ctx, lock.DatabaseID, lock.JobID)
		w.service.logAudit(ctx, AuditEvent{
			Action:        "lock.reconciled_committed",
			ObjectType:    "migration_job",
			ObjectID:      job.MigrationID,
			DatabaseID:    job.DatabaseID,
			JobID:         job.JobID,
			ExecutionUUID: job.ExecutionID,
			Status:        "SUCCESS",
			CreatedAt:     now,
		})
		return true
	case TransactionAborted:
		_ = w.service.store.CompleteRunFailed(ctx, run.ID, run.MigrationID, "target transaction aborted after worker crash", run.ExecutionMs, now)
		return w.rescheduleOrFailNormal(ctx, lock, job, "target transaction aborted after worker crash", now)
	default:
		return false
	}
}

func (w *WorkerPool) rescheduleOrFailNormal(ctx context.Context, lock DatabaseLock, job *Job, reason string, now time.Time) bool {
	if job.Attempt < job.MaxRetries {
		nextRetry := now.Add(w.retryBackoff(job.Attempt))
		_ = w.service.jobStore.ScheduleRetry(ctx, job.JobID, nextRetry, reason, "", now)
		_ = w.service.lockStore.ReleaseLock(ctx, lock.DatabaseID, lock.JobID)
		if w.service.metrics != nil {
			w.service.metrics.IncJobRetry(job.DatabaseID, string(job.Mode), string(job.Action))
		}
		return true
	}
	_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusFailed, &reason, nil, now)
	_ = w.service.lockStore.ReleaseLock(ctx, lock.DatabaseID, lock.JobID)
	return true
}

func (w *WorkerPool) reconcileUncertainBatchJob(ctx context.Context, lock DatabaseLock, job *Job) bool {
	checkpointExecID := job.ExecutionID
	if job.ParentExecutionID != nil && *job.ParentExecutionID != "" {
		checkpointExecID = *job.ParentExecutionID
	}
	direction := DirectionUp
	if job.Action == JobActionRevertBatch {
		direction = DirectionDown
	}
	checkpoints, err := w.batchConnector.ListCheckpoints(ctx, lock.DatabaseID, checkpointExecID, direction)
	if err != nil {
		return false
	}

	now := w.service.nowFn()
	w.closeDanglingRunningRun(ctx, job, now)

	maxBatch := job.LastCommittedBatch
	var lastCursor *string = job.LastCommittedCursor
	var totalRows int64
	for _, cp := range checkpoints {
		totalRows += cp.RowsAffected
		if cp.BatchNumber >= maxBatch {
			maxBatch = cp.BatchNumber
			c := cp.CursorEnd
			if direction == DirectionDown {
				c = cp.CursorStart
			}
			lastCursor = &c
		}
	}
	if totalRows < job.CommittedRows {
		totalRows = job.CommittedRows
	}

	if job.Attempt < job.MaxRetries {
		nextRetry := now.Add(w.retryBackoff(job.Attempt))
		_ = w.service.jobStore.RequeueRecoveredJob(ctx, job.JobID, maxBatch, lastCursor, totalRows, nextRetry, now)
		if job.Action == JobActionResumeBatch || job.Action == JobActionRevertBatch {
			_ = w.service.lockStore.TransitionLockState(ctx, lock.DatabaseID, lock.JobID, LockStateHeldPartial, checkpointExecID)
		} else {
			_ = w.service.lockStore.ReleaseLock(ctx, lock.DatabaseID, lock.JobID)
		}
		if w.service.metrics != nil {
			w.service.metrics.IncJobRetry(job.DatabaseID, string(job.Mode), string(job.Action))
		}
		w.service.logAudit(ctx, AuditEvent{
			Action:        "lock.reconciled_batch_requeue",
			ObjectType:    "migration_job",
			ObjectID:      job.MigrationID,
			DatabaseID:    job.DatabaseID,
			JobID:         job.JobID,
			ExecutionUUID: job.ExecutionID,
			Batch:         maxBatch,
			Status:        "PENDING",
			CreatedAt:     now,
		})
		return true
	}

	msg := "worker crashed and retries exhausted"
	if maxBatch > 0 {
		finalStatus := JobStatusPartial
		if direction == DirectionDown {
			finalStatus = JobStatusPartiallyReverted
		}
		if lastCursor != nil {
			_ = w.service.jobStore.UpdateBatchProgress(ctx, job.JobID, maxBatch, *lastCursor, totalRows, now)
		}
		_ = w.service.jobStore.FinishJob(ctx, job.JobID, finalStatus, &msg, nil, now)
		_ = w.service.lockStore.TransitionLockState(ctx, lock.DatabaseID, lock.JobID, LockStateHeldPartial, checkpointExecID)
		return true
	}

	_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusFailed, &msg, nil, now)
	_ = w.service.lockStore.ReleaseLock(ctx, lock.DatabaseID, lock.JobID)
	return true
}

func (w *WorkerPool) closeDanglingRunningRun(ctx context.Context, job *Job, now time.Time) {
	runs, err := w.service.store.ListRunsByMigration(ctx, job.MigrationID)
	if err != nil {
		return
	}
	for _, r := range runs {
		if r.Status != RunStatusRunning {
			continue
		}
		if (r.JobID != nil && *r.JobID == job.JobID) ||
			(r.ExecutionUUID != nil && *r.ExecutionUUID == job.ExecutionID) {
			_ = w.service.store.CompleteRunFailed(ctx, r.ID, r.MigrationID, "recovered after worker crash", r.ExecutionMs, now)
			return
		}
	}
}
