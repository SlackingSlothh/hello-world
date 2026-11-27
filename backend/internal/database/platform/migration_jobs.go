package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"hello-world/internal/services/migration"
)

const MigrationJobsNotifyChannel = "migration_jobs_channel"

type JobStore struct {
	platformDB *sql.DB
}

func NewJobStore(platformDB *sql.DB) *JobStore {
	return &JobStore{platformDB: platformDB}
}

func (s *JobStore) EnqueueJob(ctx context.Context, job *migration.Job) error {
	tx, err := s.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.EnqueueJobTx(ctx, tx, job); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *JobStore) EnqueueJobTx(ctx context.Context, tx *sql.Tx, job *migration.Job) error {
	traceCtx := job.TraceContext
	if len(traceCtx) == 0 {
		traceCtx = []byte("{}")
	}
	const query = `
		INSERT INTO migration_jobs (
			job_id, migration_id, execution_id, parent_execution_id, database_id,
			actor_id, actor_username, action, mode, status,
			batch_by_attribute, batch_size, target_scope_max,
			last_committed_batch, last_committed_cursor, committed_rows,
			attempt, max_retries, next_retry_at, trace_context,
			enqueued_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13,
			$14, $15, $16,
			$17, $18, $19, $20,
			$21, $22
		)
	`
	if _, err := tx.ExecContext(
		ctx, query,
		job.JobID, job.MigrationID, job.ExecutionID, job.ParentExecutionID, job.DatabaseID,
		job.ActorID, job.ActorUsername, string(job.Action), string(job.Mode), string(job.Status),
		job.BatchByAttribute, job.BatchSize, job.TargetScopeMax,
		job.LastCommittedBatch, job.LastCommittedCursor, job.CommittedRows,
		job.Attempt, job.MaxRetries, job.NextRetryAt, traceCtx,
		job.EnqueuedAt, job.UpdatedAt,
	); err != nil {
		return err
	}
	_, _ = tx.ExecContext(ctx, `SELECT pg_notify($1, $2)`, MigrationJobsNotifyChannel, job.JobID)
	return nil
}

func (s *JobStore) GetJob(ctx context.Context, jobID string) (*migration.Job, error) {
	const query = `
		SELECT job_id, migration_id, execution_id, parent_execution_id, database_id,
		       actor_id, actor_username, action, mode, status,
		       batch_by_attribute, batch_size, target_scope_max,
		       last_committed_batch, last_committed_cursor, committed_rows,
		       attempt, max_retries, next_retry_at, trace_context,
		       error_message, sqlstate, enqueued_at, started_at, finished_at, updated_at
		FROM migration_jobs
		WHERE job_id = $1
	`
	job, err := scanJob(s.platformDB.QueryRowContext(ctx, query, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, migration.ErrJobNotFound
	}
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *JobStore) GetJobByExecutionID(ctx context.Context, executionID string) (*migration.Job, error) {
	const query = `
		SELECT job_id, migration_id, execution_id, parent_execution_id, database_id,
		       actor_id, actor_username, action, mode, status,
		       batch_by_attribute, batch_size, target_scope_max,
		       last_committed_batch, last_committed_cursor, committed_rows,
		       attempt, max_retries, next_retry_at, trace_context,
		       error_message, sqlstate, enqueued_at, started_at, finished_at, updated_at
		FROM migration_jobs
		WHERE execution_id = $1
	`
	job, err := scanJob(s.platformDB.QueryRowContext(ctx, query, executionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, migration.ErrJobNotFound
	}
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *JobStore) ListJobsByDatabase(ctx context.Context, databaseID string, limit, offset int) ([]migration.Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	const query = `
		SELECT job_id, migration_id, execution_id, parent_execution_id, database_id,
		       actor_id, actor_username, action, mode, status,
		       batch_by_attribute, batch_size, target_scope_max,
		       last_committed_batch, last_committed_cursor, committed_rows,
		       attempt, max_retries, next_retry_at, trace_context,
		       error_message, sqlstate, enqueued_at, started_at, finished_at, updated_at
		FROM migration_jobs
		WHERE database_id = $1
		ORDER BY enqueued_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := s.platformDB.QueryContext(ctx, query, databaseID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]migration.Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *job)
	}
	return jobs, rows.Err()
}

func (s *JobStore) ClaimNextJob(ctx context.Context, workerID string, leaseDuration time.Duration, now time.Time) (*migration.Job, *migration.DatabaseLock, error) {
	tx, err := s.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	const selectQuery = `
		SELECT j.job_id, j.migration_id, j.execution_id, j.parent_execution_id, j.database_id,
		       j.actor_id, j.actor_username, j.action, j.mode, j.status,
		       j.batch_by_attribute, j.batch_size, j.target_scope_max,
		       j.last_committed_batch, j.last_committed_cursor, j.committed_rows,
		       j.attempt, j.max_retries, j.next_retry_at, j.trace_context,
		       j.error_message, j.sqlstate, j.enqueued_at, j.started_at, j.finished_at, j.updated_at
		FROM migration_jobs j
		WHERE j.status = 'pending'
		  AND (j.next_retry_at IS NULL OR j.next_retry_at <= $1)
		  AND (
		      (j.action IN ('execute', 'rollback', 'forward') AND NOT EXISTS (
		          SELECT 1 FROM database_locks l WHERE l.database_id = j.database_id
		      ))
		      OR
		      (j.action IN ('resume_batch', 'revert_batch') AND EXISTS (
		          SELECT 1 FROM database_locks l
		          WHERE l.database_id = j.database_id
		            AND l.lock_state = 'held_partial'
		            AND (j.parent_execution_id IS NULL OR l.execution_id = j.parent_execution_id)
		      ))
		  )
		ORDER BY j.enqueued_at ASC
		FOR UPDATE OF j SKIP LOCKED
		LIMIT 1
	`
	job, err := scanJob(tx.QueryRowContext(ctx, selectQuery, now))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	leaseExpiresAt := now.Add(leaseDuration)
	lock := &migration.DatabaseLock{
		DatabaseID:     job.DatabaseID,
		JobID:          job.JobID,
		ExecutionID:    job.ExecutionID,
		WorkerID:       workerID,
		LockState:      migration.LockStateActive,
		AcquiredAt:     now,
		LeaseExpiresAt: leaseExpiresAt,
	}

	if job.Action == migration.JobActionResumeBatch || job.Action == migration.JobActionRevertBatch {
		lockExecID := job.ExecutionID
		if job.ParentExecutionID != nil && *job.ParentExecutionID != "" {
			lockExecID = *job.ParentExecutionID
		}
		lock.ExecutionID = lockExecID
		res, err := tx.ExecContext(ctx, `
			UPDATE database_locks
			SET job_id = $1,
			    execution_id = $2,
			    worker_id = $3,
			    lock_state = 'active',
			    acquired_at = $4,
			    lease_expires_at = $5
			WHERE database_id = $6 AND lock_state = 'held_partial'
		`, job.JobID, lockExecID, workerID, now, leaseExpiresAt, job.DatabaseID)
		if err != nil {
			return nil, nil, err
		}
		affected, err := res.RowsAffected()
		if err != nil || affected == 0 {
			return nil, nil, nil
		}
	} else {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO database_locks (
				database_id, job_id, execution_id, worker_id, lock_state, acquired_at, lease_expires_at
			) VALUES ($1, $2, $3, $4, 'active', $5, $6)
			ON CONFLICT (database_id) DO NOTHING
		`, job.DatabaseID, job.JobID, job.ExecutionID, workerID, now, leaseExpiresAt)
		if err != nil {
			return nil, nil, err
		}
		affected, err := res.RowsAffected()
		if err != nil || affected == 0 {
			return nil, nil, nil
		}
	}

	if err := tx.QueryRowContext(ctx, `
		UPDATE migration_jobs
		SET status = 'running',
		    attempt = attempt + 1,
		    started_at = COALESCE(started_at, $1),
		    next_retry_at = NULL,
		    updated_at = $1
		WHERE job_id = $2
		RETURNING attempt, started_at, updated_at
	`, now, job.JobID).Scan(&job.Attempt, &job.StartedAt, &job.UpdatedAt); err != nil {
		return nil, nil, err
	}
	job.Status = migration.JobStatusRunning
	job.NextRetryAt = nil

	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return job, lock, nil
}

func (s *JobStore) SetTargetScopeMax(ctx context.Context, jobID, scopeMax string, now time.Time) error {
	_, err := s.platformDB.ExecContext(ctx, `
		UPDATE migration_jobs
		SET target_scope_max = $1,
		    updated_at = $2
		WHERE job_id = $3
	`, scopeMax, now, jobID)
	return err
}

func (s *JobStore) UpdateBatchProgress(ctx context.Context, jobID string, lastBatch int, lastCursor string, committedRows int64, now time.Time) error {
	_, err := s.platformDB.ExecContext(ctx, `
		UPDATE migration_jobs
		SET last_committed_batch = $1,
		    last_committed_cursor = $2,
		    committed_rows = $3,
		    updated_at = $4
		WHERE job_id = $5
	`, lastBatch, lastCursor, committedRows, now, jobID)
	return err
}

func (s *JobStore) ScheduleRetry(ctx context.Context, jobID string, nextRetryAt time.Time, errMsg, sqlState string, now time.Time) error {
	var statePtr *string
	if sqlState != "" {
		statePtr = &sqlState
	}
	_, err := s.platformDB.ExecContext(ctx, `
		UPDATE migration_jobs
		SET status = 'pending',
		    next_retry_at = $1,
		    error_message = $2,
		    sqlstate = $3,
		    updated_at = $4
		WHERE job_id = $5
	`, nextRetryAt, errMsg, statePtr, now, jobID)
	return err
}

func (s *JobStore) FinishJob(ctx context.Context, jobID string, status migration.JobStatus, errMsg, sqlState *string, now time.Time) error {
	_, err := s.platformDB.ExecContext(ctx, `
		UPDATE migration_jobs
		SET status = $1,
		    error_message = $2,
		    sqlstate = $3,
		    finished_at = $4,
		    updated_at = $4
		WHERE job_id = $5
	`, string(status), errMsg, sqlState, now, jobID)
	return err
}

func (s *JobStore) RequeueRecoveredJob(ctx context.Context, jobID string, lastBatch int, lastCursor *string, committedRows int64, nextRetryAt time.Time, now time.Time) error {
	_, err := s.platformDB.ExecContext(ctx, `
		UPDATE migration_jobs
		SET status = 'pending',
		    last_committed_batch = $1,
		    last_committed_cursor = $2,
		    committed_rows = $3,
		    next_retry_at = $4,
		    updated_at = $5
		WHERE job_id = $6
	`, lastBatch, lastCursor, committedRows, nextRetryAt, now, jobID)
	return err
}

func (s *JobStore) CountJobsByStatus(ctx context.Context) ([]migration.JobStatusMetric, error) {
	rows, err := s.platformDB.QueryContext(ctx, `
		SELECT database_id, status, COUNT(*)
		FROM migration_jobs
		GROUP BY database_id, status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]migration.JobStatusMetric, 0)
	for rows.Next() {
		var item migration.JobStatusMetric
		var status string
		if err := rows.Scan(&item.DatabaseID, &status, &item.Count); err != nil {
			return nil, err
		}
		item.Status = migration.JobStatus(status)
		out = append(out, item)
	}
	return out, rows.Err()
}

type jobScanner interface {
	Scan(dest ...any) error
}

func scanJob(row jobScanner) (*migration.Job, error) {
	var job migration.Job
	var parentExecID, batchAttr, targetScope, lastCursor, errMsg, sqlState sql.NullString
	var batchSize sql.NullInt64
	var nextRetryAt, startedAt, finishedAt sql.NullTime
	var action, mode, status string

	if err := row.Scan(
		&job.JobID, &job.MigrationID, &job.ExecutionID, &parentExecID, &job.DatabaseID,
		&job.ActorID, &job.ActorUsername, &action, &mode, &status,
		&batchAttr, &batchSize, &targetScope,
		&job.LastCommittedBatch, &lastCursor, &job.CommittedRows,
		&job.Attempt, &job.MaxRetries, &nextRetryAt, &job.TraceContext,
		&errMsg, &sqlState, &job.EnqueuedAt, &startedAt, &finishedAt, &job.UpdatedAt,
	); err != nil {
		return nil, err
	}
	job.Action = migration.JobAction(action)
	job.Mode = migration.ExecutionMode(mode)
	job.Status = migration.JobStatus(status)
	if parentExecID.Valid {
		v := parentExecID.String
		job.ParentExecutionID = &v
	}
	if batchAttr.Valid {
		v := batchAttr.String
		job.BatchByAttribute = &v
	}
	if batchSize.Valid {
		v := int(batchSize.Int64)
		job.BatchSize = &v
	}
	if targetScope.Valid {
		v := targetScope.String
		job.TargetScopeMax = &v
	}
	if lastCursor.Valid {
		v := lastCursor.String
		job.LastCommittedCursor = &v
	}
	if nextRetryAt.Valid {
		t := nextRetryAt.Time
		job.NextRetryAt = &t
	}
	if errMsg.Valid {
		v := errMsg.String
		job.ErrorMessage = &v
	}
	if sqlState.Valid {
		v := sqlState.String
		job.SQLState = &v
	}
	if startedAt.Valid {
		t := startedAt.Time
		job.StartedAt = &t
	}
	if finishedAt.Valid {
		t := finishedAt.Time
		job.FinishedAt = &t
	}
	return &job, nil
}
