package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"hello-world/internal/observability"
)

type WorkerConfig struct {
	WorkerIDPrefix    string
	Concurrency       int
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	SweepInterval     time.Duration
	BaseRetryBackoff  time.Duration
}

func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		WorkerIDPrefix:    "worker",
		Concurrency:       2,
		PollInterval:      500 * time.Millisecond,
		LeaseDuration:     15 * time.Second,
		HeartbeatInterval: 5 * time.Second,
		SweepInterval:     5 * time.Second,
		BaseRetryBackoff:  2 * time.Second,
	}
}

type WorkerPool struct {
	service        *Service
	batchConnector BatchTargetConnector
	cfg            WorkerConfig
	notifyCh       chan struct{}
}

func NewWorkerPool(service *Service, batchConnector BatchTargetConnector, cfg WorkerConfig) *WorkerPool {
	defaults := DefaultWorkerConfig()
	if cfg.WorkerIDPrefix == "" {
		cfg.WorkerIDPrefix = defaults.WorkerIDPrefix
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = defaults.Concurrency
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaults.PollInterval
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = defaults.LeaseDuration
	}
	if cfg.HeartbeatInterval <= 0 || cfg.HeartbeatInterval >= cfg.LeaseDuration {
		cfg.HeartbeatInterval = cfg.LeaseDuration / 3
		if cfg.HeartbeatInterval <= 0 {
			cfg.HeartbeatInterval = time.Second
		}
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = defaults.SweepInterval
	}
	if cfg.BaseRetryBackoff <= 0 {
		cfg.BaseRetryBackoff = defaults.BaseRetryBackoff
	}
	return &WorkerPool{
		service:        service,
		batchConnector: batchConnector,
		cfg:            cfg,
		notifyCh:       make(chan struct{}, 16),
	}
}

func (w *WorkerPool) Notify() {
	select {
	case w.notifyCh <- struct{}{}:
	default:
	}
}

func (w *WorkerPool) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < w.cfg.Concurrency; i++ {
		workerID := fmt.Sprintf("%s-%d", w.cfg.WorkerIDPrefix, i+1)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			w.workerLoop(ctx, id)
		}(workerID)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.sweeperLoop(ctx)
	}()
	wg.Wait()
}

func (w *WorkerPool) workerLoop(ctx context.Context, workerID string) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	for {
		processed, err := w.ProcessOne(ctx, workerID)
		if err != nil && ctx.Err() == nil {
			w.service.logAudit(ctx, AuditEvent{
				Action:    "worker.poll_error",
				Status:    "ERROR",
				Error:     err.Error(),
				CreatedAt: w.service.nowFn(),
			})
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.notifyCh:
		case <-ticker.C:
		}
	}
}

func (w *WorkerPool) sweeperLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		_, _ = w.RunRecoverySweep(ctx)
		w.syncMetrics(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *WorkerPool) syncMetrics(ctx context.Context) {
	if w.service.metrics == nil || w.service.jobStore == nil {
		return
	}
	counts, err := w.service.jobStore.CountJobsByStatus(ctx)
	if err != nil {
		return
	}
	items := make([]observability.JobStatusCount, 0, len(counts))
	for _, c := range counts {
		items = append(items, observability.JobStatusCount{
			Database: c.DatabaseID,
			Status:   string(c.Status),
			Count:    float64(c.Count),
		})
	}
	w.service.metrics.SyncJobsByStatus(items)
}

func (w *WorkerPool) ProcessOne(ctx context.Context, workerID string) (bool, error) {
	if w.service == nil || w.service.jobStore == nil || w.service.lockStore == nil {
		return false, ErrAsyncUnavailable
	}
	claimStarted := w.service.nowFn()
	job, lock, err := w.service.jobStore.ClaimNextJob(ctx, workerID, w.cfg.LeaseDuration, claimStarted)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}

	parentCtx := observability.ExtractTraceContext(ctx, job.TraceContext)
	jobCtx, span := observability.Tracer("datadock/worker").Start(parentCtx, "worker.process_job")
	span.SetAttributes(
		attribute.String("database", job.DatabaseID),
		attribute.Int64("migration_id", job.MigrationID),
		attribute.String("action", string(job.Action)),
		attribute.String("mode", string(job.Mode)),
		attribute.Int("attempt", job.Attempt),
	)
	defer span.End()

	if w.service.metrics != nil {
		w.service.metrics.ObserveQueueWait(job.DatabaseID, string(job.Mode), claimStarted.Sub(job.EnqueuedAt))
		if lock != nil {
			w.service.metrics.ObserveLockWait(job.DatabaseID, lock.AcquiredAt.Sub(claimStarted))
		}
	}

	w.service.logAudit(jobCtx, AuditEvent{
		ActorUserID:   job.ActorID,
		Action:        "worker.claim",
		ObjectType:    "migration_job",
		ObjectID:      job.MigrationID,
		DatabaseID:    job.DatabaseID,
		JobID:         job.JobID,
		ExecutionUUID: job.ExecutionID,
		AttemptNumber: job.Attempt,
		Status:        "RUNNING",
		CreatedAt:     claimStarted,
	})

	hbCtx, stopHeartbeat := context.WithCancel(jobCtx)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		w.heartbeatLoop(hbCtx, job.DatabaseID, job.JobID, workerID)
	}()
	defer func() {
		stopHeartbeat()
		<-hbDone
	}()

	execStarted := w.service.nowFn()
	m, revErr := w.revalidate(jobCtx, job)
	if revErr != nil {
		span.RecordError(revErr)
		span.SetStatus(codes.Error, revErr.Error())
		errMsg := revErr.Error()
		_ = w.service.jobStore.FinishJob(jobCtx, job.JobID, JobStatusFailed, &errMsg, nil, w.service.nowFn())
		w.releaseOrRestoreLock(jobCtx, job)
		w.recordCompletionMetrics(job, JobStatusFailed, w.service.nowFn().Sub(execStarted))
		return true, nil
	}

	if job.Mode == ModeBatch && (job.Action == JobActionExecute || job.Action == JobActionForward || job.Action == JobActionResumeBatch || job.Action == JobActionRevertBatch) {
		if job.Action == JobActionRevertBatch {
			w.executeRevertBatchJob(jobCtx, span, job, m, execStarted)
		} else {
			w.executeBatchUpJob(jobCtx, span, job, m, execStarted)
		}
		return true, nil
	}

	w.executeNormalJob(jobCtx, span, job, execStarted)
	return true, nil
}

func (w *WorkerPool) heartbeatLoop(ctx context.Context, databaseID, jobID, workerID string) {
	ticker := time.NewTicker(w.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			leaseUntil := w.service.nowFn().Add(w.cfg.LeaseDuration)
			_ = w.service.lockStore.RenewLease(ctx, databaseID, jobID, workerID, leaseUntil)
		}
	}
}

func (w *WorkerPool) revalidate(ctx context.Context, job *Job) (*Migration, error) {
	ctx, span := observability.Tracer("datadock/worker").Start(ctx, "migration.revalidate")
	defer span.End()

	principal := Principal{UserID: job.ActorID, Username: job.ActorUsername}
	if err := w.service.authorize(ctx, principal, job.DatabaseID, ActionExecuteMigration); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if err := w.service.verifyApproved(ctx, job.DatabaseID, job.MigrationID, job.Action); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	m, err := w.service.getVerifiedMigration(ctx, job.DatabaseID, job.MigrationID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if job.Action == JobActionRollback || job.Action == JobActionRevertBatch {
		if strings.TrimSpace(m.SQLDown) == "" {
			return nil, ErrIrreversible
		}
		if err := ValidateTransactionalSQL(m.SQLDown); err != nil {
			return nil, err
		}
	} else {
		if err := ValidateTransactionalSQL(m.SQLContent); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (w *WorkerPool) executeNormalJob(ctx context.Context, span trace.Span, job *Job, started time.Time) {
	op := mapJobActionToRunOperation(job.Action)
	principal := Principal{UserID: job.ActorID, Username: job.ActorUsername}
	jobID := job.JobID
	execID := job.ExecutionID

	_, err := w.service.executeWithJob(ctx, principal, job.DatabaseID, job.MigrationID, op, &jobID, &execID)
	finished := w.service.nowFn()
	if err == nil {
		_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusSuccess, nil, nil, finished)
		_ = w.service.lockStore.ReleaseLock(ctx, job.DatabaseID, job.JobID)
		w.recordCompletionMetrics(job, JobStatusSuccess, finished.Sub(started))
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())

	sqlState := extractSQLState(err)
	errMsg := err.Error()
	if errors.Is(err, ErrOutcomeUnconfirmed) {
		_ = w.service.lockStore.TransitionLockState(ctx, job.DatabaseID, job.JobID, LockStateHeldUncertain, job.ExecutionID)
		_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusUncertain, &errMsg, ptrIfNotEmpty(sqlState), finished)
		return
	}

	if isTransientError(err, sqlState) && job.Attempt < job.MaxRetries {
		nextRetry := finished.Add(w.retryBackoff(job.Attempt))
		_ = w.service.jobStore.ScheduleRetry(ctx, job.JobID, nextRetry, errMsg, sqlState, finished)
		_ = w.service.lockStore.ReleaseLock(ctx, job.DatabaseID, job.JobID)
		if w.service.metrics != nil {
			w.service.metrics.IncJobRetry(job.DatabaseID, string(job.Mode), string(job.Action))
		}
		w.service.logAudit(ctx, AuditEvent{
			ActorUserID:   job.ActorID,
			Action:        "worker.retry_scheduled",
			ObjectType:    "migration_job",
			ObjectID:      job.MigrationID,
			DatabaseID:    job.DatabaseID,
			JobID:         job.JobID,
			ExecutionUUID: job.ExecutionID,
			AttemptNumber: job.Attempt,
			Status:        "PENDING",
			Error:         errMsg,
			SQLState:      sqlState,
			CreatedAt:     finished,
		})
		return
	}

	_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusFailed, &errMsg, ptrIfNotEmpty(sqlState), finished)
	_ = w.service.lockStore.ReleaseLock(ctx, job.DatabaseID, job.JobID)
	w.recordCompletionMetrics(job, JobStatusFailed, finished.Sub(started))
}

func (w *WorkerPool) executeBatchUpJob(ctx context.Context, _ any, job *Job, m *Migration, started time.Time) {
	if w.batchConnector == nil {
		errMsg := "batch target connector is not configured"
		_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusFailed, &errMsg, nil, w.service.nowFn())
		w.releaseOrRestoreLock(ctx, job)
		return
	}

	cursorCol := m.BatchByAttribute
	if job.BatchByAttribute != nil && *job.BatchByAttribute != "" {
		cursorCol = *job.BatchByAttribute
	}
	batchSize := 0
	if job.BatchSize != nil && *job.BatchSize > 0 {
		batchSize = *job.BatchSize
	} else if m.BatchSize != nil && *m.BatchSize > 0 {
		batchSize = *m.BatchSize
	}

	checkpointExecID := job.ExecutionID
	if job.ParentExecutionID != nil && *job.ParentExecutionID != "" {
		checkpointExecID = *job.ParentExecutionID
	}

	runOp := mapJobActionToRunOperation(job.Action)
	jobID := job.JobID
	execID := job.ExecutionID
	run := &MigrationRun{
		MigrationID:   m.ID,
		DatabaseID:    job.DatabaseID,
		JobID:         &jobID,
		ExecutionUUID: &execID,
		Revision:      m.Revision,
		Operation:     runOp,
		SQLSnapshot:   m.SQLContent,
		Status:        RunStatusRunning,
		ExecutedBy:    job.ActorID,
		StartedAt:     started,
	}
	if err := w.service.store.CreateRun(ctx, run); err != nil {
		errMsg := err.Error()
		_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusFailed, &errMsg, nil, w.service.nowFn())
		w.releaseOrRestoreLock(ctx, job)
		w.recordCompletionMetrics(job, JobStatusFailed, w.service.nowFn().Sub(started))
		return
	}

	if err := w.batchConnector.EnsureCheckpointSchema(ctx, job.DatabaseID); err != nil {
		w.failBatchAttempt(ctx, job, m, run, checkpointExecID, started, job.LastCommittedBatch, err)
		return
	}

	scopeMax := ""
	if job.TargetScopeMax != nil {
		scopeMax = *job.TargetScopeMax
	}
	if scopeMax == "" {
		resolved, err := w.batchConnector.ResolveTargetScopeMax(ctx, job.DatabaseID, m.SQLContent, cursorCol)
		if err != nil {
			w.failBatchAttempt(ctx, job, m, run, checkpointExecID, started, job.LastCommittedBatch, err)
			return
		}
		scopeMax = resolved
		_ = w.service.jobStore.SetTargetScopeMax(ctx, job.JobID, scopeMax, w.service.nowFn())
	}

	if err := w.service.store.MarkSQLDispatchStarted(ctx, run.ID, TransactionEvidence{}); err != nil {
		w.failBatchAttempt(ctx, job, m, run, checkpointExecID, started, job.LastCommittedBatch, err)
		return
	}

	batchesCommitted := job.LastCommittedBatch
	lastCursor := ""
	if job.LastCommittedCursor != nil {
		lastCursor = *job.LastCommittedCursor
	}
	committedRows := job.CommittedRows

	for {
		if err := ctx.Err(); err != nil {
			w.failBatchAttempt(ctx, job, m, run, checkpointExecID, started, batchesCommitted, err)
			return
		}
		nextBatch := batchesCommitted + 1
		batchStarted := w.service.nowFn()
		batchCtx, batchSpan := observability.Tracer("datadock/worker").Start(ctx, "batch.execute")
		batchSpan.SetAttributes(
			attribute.String("database", job.DatabaseID),
			attribute.Int("batch", nextBatch),
			attribute.String("direction", string(DirectionUp)),
		)

		w.service.logAudit(batchCtx, AuditEvent{
			ActorUserID:   job.ActorID,
			Action:        "batch.execute.start",
			ObjectType:    "migration_job",
			ObjectID:      m.ID,
			DatabaseID:    job.DatabaseID,
			JobID:         job.JobID,
			ExecutionUUID: job.ExecutionID,
			AttemptNumber: job.Attempt,
			Batch:         nextBatch,
			SQL:           m.SQLContent,
			Status:        "RUNNING",
			CreatedAt:     batchStarted,
		})

		stepRes, err := w.batchConnector.ExecuteBatchStep(batchCtx, job.DatabaseID, BatchStepRequest{
			ExecutionID:    checkpointExecID,
			BatchNumber:    nextBatch,
			SQLQuery:       m.SQLContent,
			CursorColumn:   cursorCol,
			LastCursor:     lastCursor,
			TargetScopeMax: scopeMax,
			BatchSize:      batchSize,
		})
		if err != nil {
			batchSpan.RecordError(err)
			batchSpan.SetStatus(codes.Error, err.Error())
			batchSpan.End()
			w.service.logAudit(batchCtx, AuditEvent{
				ActorUserID:   job.ActorID,
				Action:        "batch.execute.rollback",
				ObjectType:    "migration_job",
				ObjectID:      m.ID,
				DatabaseID:    job.DatabaseID,
				JobID:         job.JobID,
				ExecutionUUID: job.ExecutionID,
				AttemptNumber: job.Attempt,
				Batch:         nextBatch,
				DurationMs:    max(0, w.service.nowFn().Sub(batchStarted).Milliseconds()),
				SQL:           m.SQLContent,
				Error:         err.Error(),
				SQLState:      extractSQLState(err),
				Status:        "FAILED",
				CreatedAt:     w.service.nowFn(),
			})
			w.failBatchAttempt(ctx, job, m, run, checkpointExecID, started, batchesCommitted, err)
			return
		}
		if stepRes.Done {
			batchSpan.End()
			break
		}

		batchesCommitted = stepRes.Checkpoint.BatchNumber
		lastCursor = stepRes.Checkpoint.CursorEnd
		if !stepRes.AlreadyCommitted {
			committedRows += stepRes.Checkpoint.RowsAffected
			if w.service.metrics != nil {
				w.service.metrics.AddBatchesProcessed(job.DatabaseID, string(DirectionUp), 1)
				w.service.metrics.AddRowsProcessed(job.DatabaseID, string(DirectionUp), float64(stepRes.Checkpoint.RowsAffected))
			}
		}
		_ = w.service.jobStore.UpdateBatchProgress(batchCtx, job.JobID, batchesCommitted, lastCursor, committedRows, w.service.nowFn())

		rowsAffected := stepRes.Checkpoint.RowsAffected
		w.service.logAudit(batchCtx, AuditEvent{
			ActorUserID:   job.ActorID,
			Action:        "batch.execute.commit",
			ObjectType:    "migration_job",
			ObjectID:      m.ID,
			DatabaseID:    job.DatabaseID,
			JobID:         job.JobID,
			ExecutionUUID: job.ExecutionID,
			AttemptNumber: job.Attempt,
			Batch:         batchesCommitted,
			DurationMs:    max(0, w.service.nowFn().Sub(batchStarted).Milliseconds()),
			RowsAffected:  &rowsAffected,
			SQL:           m.SQLContent,
			Status:        "COMMITTED",
			CreatedAt:     w.service.nowFn(),
		})
		batchSpan.End()
	}

	finished := w.service.nowFn()
	execMs := max(0, finished.Sub(started).Milliseconds())
	_, _ = w.service.store.CompleteRunSucceeded(ctx, run.ID, m.ID, job.DatabaseID, execMs, finished)
	_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusSuccess, nil, nil, finished)
	_ = w.service.lockStore.ReleaseLock(ctx, job.DatabaseID, job.JobID)
	w.recordCompletionMetrics(job, JobStatusSuccess, finished.Sub(started))
}

func (w *WorkerPool) executeRevertBatchJob(ctx context.Context, _ any, job *Job, m *Migration, started time.Time) {
	if w.batchConnector == nil {
		errMsg := "batch target connector is not configured"
		_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusFailed, &errMsg, nil, w.service.nowFn())
		w.releaseOrRestoreLock(ctx, job)
		return
	}

	checkpointExecID := job.ExecutionID
	if job.ParentExecutionID != nil && *job.ParentExecutionID != "" {
		checkpointExecID = *job.ParentExecutionID
	}
	cursorCol := m.BatchByAttribute
	if job.BatchByAttribute != nil && *job.BatchByAttribute != "" {
		cursorCol = *job.BatchByAttribute
	}
	batchSize := 0
	if job.BatchSize != nil && *job.BatchSize > 0 {
		batchSize = *job.BatchSize
	} else if m.BatchSize != nil && *m.BatchSize > 0 {
		batchSize = *m.BatchSize
	}

	jobID := job.JobID
	execID := job.ExecutionID
	run := &MigrationRun{
		MigrationID:   m.ID,
		DatabaseID:    job.DatabaseID,
		JobID:         &jobID,
		ExecutionUUID: &execID,
		Revision:      m.Revision,
		Operation:     RunRevertBatch,
		SQLSnapshot:   m.SQLDown,
		Status:        RunStatusRunning,
		ExecutedBy:    job.ActorID,
		StartedAt:     started,
	}
	if err := w.service.store.CreateRun(ctx, run); err != nil {
		errMsg := err.Error()
		_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusPartiallyReverted, &errMsg, nil, w.service.nowFn())
		_ = w.service.lockStore.TransitionLockState(ctx, job.DatabaseID, job.JobID, LockStateHeldPartial, checkpointExecID)
		w.recordCompletionMetrics(job, JobStatusPartiallyReverted, w.service.nowFn().Sub(started))
		return
	}

	if err := w.batchConnector.EnsureCheckpointSchema(ctx, job.DatabaseID); err != nil {
		w.failRevertAttempt(ctx, job, m, run, checkpointExecID, started, err)
		return
	}
	upCheckpoints, err := w.batchConnector.ListCheckpoints(ctx, job.DatabaseID, checkpointExecID, DirectionUp)
	if err != nil {
		w.failRevertAttempt(ctx, job, m, run, checkpointExecID, started, err)
		return
	}
	downCheckpoints, err := w.batchConnector.ListCheckpoints(ctx, job.DatabaseID, checkpointExecID, DirectionDown)
	if err != nil {
		w.failRevertAttempt(ctx, job, m, run, checkpointExecID, started, err)
		return
	}
	revertedMap := make(map[int]bool, len(downCheckpoints))
	var revertedRows int64
	for _, cp := range downCheckpoints {
		revertedMap[cp.BatchNumber] = true
		revertedRows += cp.RowsAffected
	}

	if err := w.service.store.MarkSQLDispatchStarted(ctx, run.ID, TransactionEvidence{}); err != nil {
		w.failRevertAttempt(ctx, job, m, run, checkpointExecID, started, err)
		return
	}

	for i := len(upCheckpoints) - 1; i >= 0; i-- {
		cp := upCheckpoints[i]
		if revertedMap[cp.BatchNumber] {
			continue
		}
		if err := ctx.Err(); err != nil {
			w.failRevertAttempt(ctx, job, m, run, checkpointExecID, started, err)
			return
		}
		batchStarted := w.service.nowFn()
		batchCtx, batchSpan := observability.Tracer("datadock/worker").Start(ctx, "batch.execute")
		batchSpan.SetAttributes(
			attribute.String("database", job.DatabaseID),
			attribute.Int("batch", cp.BatchNumber),
			attribute.String("direction", string(DirectionDown)),
		)

		stepRes, err := w.batchConnector.RevertBatchStep(batchCtx, job.DatabaseID, RevertStepRequest{
			ExecutionID:  checkpointExecID,
			Checkpoint:   cp,
			SQLDown:      m.SQLDown,
			CursorColumn: cursorCol,
			BatchSize:    batchSize,
		})
		if err != nil {
			batchSpan.RecordError(err)
			batchSpan.SetStatus(codes.Error, err.Error())
			batchSpan.End()
			w.service.logAudit(batchCtx, AuditEvent{
				ActorUserID:   job.ActorID,
				Action:        "batch.revert.rollback",
				ObjectType:    "migration_job",
				ObjectID:      m.ID,
				DatabaseID:    job.DatabaseID,
				JobID:         job.JobID,
				ExecutionUUID: job.ExecutionID,
				AttemptNumber: job.Attempt,
				Batch:         cp.BatchNumber,
				DurationMs:    max(0, w.service.nowFn().Sub(batchStarted).Milliseconds()),
				SQL:           m.SQLDown,
				Error:         err.Error(),
				SQLState:      extractSQLState(err),
				Status:        "FAILED",
				CreatedAt:     w.service.nowFn(),
			})
			w.failRevertAttempt(ctx, job, m, run, checkpointExecID, started, err)
			return
		}

		if !stepRes.AlreadyCommitted {
			revertedRows += stepRes.Checkpoint.RowsAffected
			if w.service.metrics != nil {
				w.service.metrics.AddBatchesProcessed(job.DatabaseID, string(DirectionDown), 1)
				w.service.metrics.AddRowsProcessed(job.DatabaseID, string(DirectionDown), float64(stepRes.Checkpoint.RowsAffected))
			}
		}
		_ = w.service.jobStore.UpdateBatchProgress(batchCtx, job.JobID, cp.BatchNumber, cp.CursorStart, revertedRows, w.service.nowFn())
		rowsAffected := stepRes.Checkpoint.RowsAffected
		w.service.logAudit(batchCtx, AuditEvent{
			ActorUserID:   job.ActorID,
			Action:        "batch.revert.commit",
			ObjectType:    "migration_job",
			ObjectID:      m.ID,
			DatabaseID:    job.DatabaseID,
			JobID:         job.JobID,
			ExecutionUUID: job.ExecutionID,
			AttemptNumber: job.Attempt,
			Batch:         cp.BatchNumber,
			DurationMs:    max(0, w.service.nowFn().Sub(batchStarted).Milliseconds()),
			RowsAffected:  &rowsAffected,
			SQL:           m.SQLDown,
			Status:        "COMMITTED",
			CreatedAt:     w.service.nowFn(),
		})
		batchSpan.End()
	}

	finished := w.service.nowFn()
	execMs := max(0, finished.Sub(started).Milliseconds())
	_, _ = w.service.store.CompleteRunSucceeded(ctx, run.ID, m.ID, job.DatabaseID, execMs, finished)
	_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusSuccess, nil, nil, finished)
	_ = w.service.lockStore.ReleaseLock(ctx, job.DatabaseID, job.JobID)
	w.recordCompletionMetrics(job, JobStatusSuccess, finished.Sub(started))
}

func (w *WorkerPool) failBatchAttempt(ctx context.Context, job *Job, m *Migration, run *MigrationRun, checkpointExecID string, started time.Time, batchesCommitted int, cause error) {
	finished := w.service.nowFn()
	execMs := max(0, finished.Sub(started).Milliseconds())
	errMsg := cause.Error()
	sqlState := extractSQLState(cause)
	_ = w.service.store.CompleteRunFailed(ctx, run.ID, m.ID, errMsg, execMs, finished)

	if batchesCommitted > 0 {
		_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusPartial, &errMsg, ptrIfNotEmpty(sqlState), finished)
		_ = w.service.lockStore.TransitionLockState(ctx, job.DatabaseID, job.JobID, LockStateHeldPartial, checkpointExecID)
		w.recordCompletionMetrics(job, JobStatusPartial, finished.Sub(started))
		return
	}

	if isTransientError(cause, sqlState) && job.Attempt < job.MaxRetries {
		nextRetry := finished.Add(w.retryBackoff(job.Attempt))
		_ = w.service.jobStore.ScheduleRetry(ctx, job.JobID, nextRetry, errMsg, sqlState, finished)
		_ = w.service.lockStore.ReleaseLock(ctx, job.DatabaseID, job.JobID)
		if w.service.metrics != nil {
			w.service.metrics.IncJobRetry(job.DatabaseID, string(job.Mode), string(job.Action))
		}
		return
	}

	_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusFailed, &errMsg, ptrIfNotEmpty(sqlState), finished)
	_ = w.service.lockStore.ReleaseLock(ctx, job.DatabaseID, job.JobID)
	w.recordCompletionMetrics(job, JobStatusFailed, finished.Sub(started))
}

func (w *WorkerPool) failRevertAttempt(ctx context.Context, job *Job, m *Migration, run *MigrationRun, checkpointExecID string, started time.Time, cause error) {
	finished := w.service.nowFn()
	execMs := max(0, finished.Sub(started).Milliseconds())
	errMsg := cause.Error()
	sqlState := extractSQLState(cause)
	_ = w.service.store.CompleteRunFailed(ctx, run.ID, m.ID, errMsg, execMs, finished)
	_ = w.service.jobStore.FinishJob(ctx, job.JobID, JobStatusPartiallyReverted, &errMsg, ptrIfNotEmpty(sqlState), finished)
	_ = w.service.lockStore.TransitionLockState(ctx, job.DatabaseID, job.JobID, LockStateHeldPartial, checkpointExecID)
	w.recordCompletionMetrics(job, JobStatusPartiallyReverted, finished.Sub(started))
}

func (w *WorkerPool) releaseOrRestoreLock(ctx context.Context, job *Job) {
	if job.Action == JobActionResumeBatch || job.Action == JobActionRevertBatch {
		execID := job.ExecutionID
		if job.ParentExecutionID != nil && *job.ParentExecutionID != "" {
			execID = *job.ParentExecutionID
		}
		_ = w.service.lockStore.TransitionLockState(ctx, job.DatabaseID, job.JobID, LockStateHeldPartial, execID)
		return
	}
	_ = w.service.lockStore.ReleaseLock(ctx, job.DatabaseID, job.JobID)
}

func (w *WorkerPool) recordCompletionMetrics(job *Job, status JobStatus, d time.Duration) {
	if w.service.metrics == nil {
		return
	}
	w.service.metrics.IncJobCompleted(job.DatabaseID, string(status), string(job.Mode), string(job.Action))
	w.service.metrics.ObserveExecutionDuration(job.DatabaseID, string(job.Mode), string(job.Action), string(status), d)
}

func (w *WorkerPool) retryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return w.cfg.BaseRetryBackoff
	}
	shift := min(attempt-1, 5)
	return w.cfg.BaseRetryBackoff * time.Duration(1<<shift)
}

func mapJobActionToRunOperation(action JobAction) RunOperation {
	switch action {
	case JobActionRollback:
		return RunRollback
	case JobActionForward:
		return RunForward
	case JobActionResumeBatch:
		return RunResumeBatch
	case JobActionRevertBatch:
		return RunRevertBatch
	default:
		return RunApply
	}
}

func extractSQLState(err error) string {
	if err == nil {
		return ""
	}
	var sqlErr interface{ SQLState() string }
	if errors.As(err, &sqlErr) {
		return sqlErr.SQLState()
	}
	return ""
}

func isTransientError(err error, sqlState string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrInvalidSQL) || errors.Is(err, ErrIrreversible) ||
		errors.Is(err, ErrAlreadyApplied) || errors.Is(err, ErrMigrationOrder) || errors.Is(err, ErrNotApproved) {
		return false
	}
	switch sqlState {
	case "40001", "40P01", "55P03", "57P01", "57P02", "57P03", "53300":
		return true
	}
	if strings.HasPrefix(sqlState, "08") {
		return true
	}
	return false
}

func ptrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
