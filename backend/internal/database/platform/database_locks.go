package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"hello-world/internal/services/migration"
)

type LockStore struct {
	platformDB *sql.DB
}

func NewLockStore(platformDB *sql.DB) *LockStore {
	return &LockStore{platformDB: platformDB}
}

func (s *LockStore) AcquireLock(ctx context.Context, lock migration.DatabaseLock) error {
	res, err := s.platformDB.ExecContext(ctx, `
		INSERT INTO database_locks (
			database_id, job_id, execution_id, worker_id, lock_state, acquired_at, lease_expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (database_id) DO NOTHING
	`, lock.DatabaseID, lock.JobID, lock.ExecutionID, lock.WorkerID, string(lock.LockState), lock.AcquiredAt, lock.LeaseExpiresAt)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return migration.ErrLockConflict
	}
	return nil
}

func (s *LockStore) TakeoverPartialLock(ctx context.Context, databaseID, jobID, executionID, workerID string, acquiredAt, leaseExpiresAt time.Time) error {
	res, err := s.platformDB.ExecContext(ctx, `
		UPDATE database_locks
		SET job_id = $1,
		    execution_id = $2,
		    worker_id = $3,
		    lock_state = 'active',
		    acquired_at = $4,
		    lease_expires_at = $5
		WHERE database_id = $6 AND lock_state = 'held_partial'
	`, jobID, executionID, workerID, acquiredAt, leaseExpiresAt, databaseID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return migration.ErrLockConflict
	}
	return nil
}

func (s *LockStore) GetLock(ctx context.Context, databaseID string) (*migration.DatabaseLock, error) {
	var lock migration.DatabaseLock
	var state string
	err := s.platformDB.QueryRowContext(ctx, `
		SELECT database_id, job_id, execution_id, worker_id, lock_state, acquired_at, lease_expires_at
		FROM database_locks
		WHERE database_id = $1
	`, databaseID).Scan(
		&lock.DatabaseID, &lock.JobID, &lock.ExecutionID, &lock.WorkerID, &state, &lock.AcquiredAt, &lock.LeaseExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, migration.ErrLockNotFound
	}
	if err != nil {
		return nil, err
	}
	lock.LockState = migration.LockState(state)
	return &lock, nil
}

func (s *LockStore) RenewLease(ctx context.Context, databaseID, jobID, workerID string, leaseExpiresAt time.Time) error {
	res, err := s.platformDB.ExecContext(ctx, `
		UPDATE database_locks
		SET lease_expires_at = $1
		WHERE database_id = $2 AND job_id = $3 AND worker_id = $4 AND lock_state = 'active'
	`, leaseExpiresAt, databaseID, jobID, workerID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return migration.ErrLockNotFound
	}
	return nil
}

func (s *LockStore) TransitionLockState(ctx context.Context, databaseID, jobID string, newState migration.LockState, executionID string) error {
	res, err := s.platformDB.ExecContext(ctx, `
		UPDATE database_locks
		SET lock_state = $1,
		    execution_id = CASE WHEN $2 <> '' THEN $2::uuid ELSE execution_id END
		WHERE database_id = $3 AND job_id = $4
	`, string(newState), executionID, databaseID, jobID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return migration.ErrLockNotFound
	}
	return nil
}

func (s *LockStore) ReleaseLock(ctx context.Context, databaseID, jobID string) error {
	_, err := s.platformDB.ExecContext(ctx, `
		DELETE FROM database_locks
		WHERE database_id = $1 AND job_id = $2
	`, databaseID, jobID)
	return err
}

func (s *LockStore) SweepExpiredLeases(ctx context.Context, now time.Time) ([]migration.DatabaseLock, error) {
	tx, err := s.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT database_id, job_id, execution_id, worker_id, lock_state, acquired_at, lease_expires_at
		FROM database_locks
		WHERE lock_state = 'active' AND lease_expires_at < $1
		FOR UPDATE SKIP LOCKED
	`, now)
	if err != nil {
		return nil, err
	}

	var expired []migration.DatabaseLock
	for rows.Next() {
		var lock migration.DatabaseLock
		var state string
		if err := rows.Scan(
			&lock.DatabaseID, &lock.JobID, &lock.ExecutionID, &lock.WorkerID, &state, &lock.AcquiredAt, &lock.LeaseExpiresAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		lock.LockState = migration.LockStateHeldUncertain
		expired = append(expired, lock)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for _, lock := range expired {
		if _, err := tx.ExecContext(ctx, `
			UPDATE database_locks
			SET lock_state = 'held_uncertain'
			WHERE database_id = $1 AND job_id = $2 AND lock_state = 'active'
		`, lock.DatabaseID, lock.JobID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE migration_jobs
			SET status = 'uncertain',
			    updated_at = $1
			WHERE job_id = $2 AND status = 'running'
		`, now, lock.JobID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return expired, nil
}

func (s *LockStore) ListUncertainLocks(ctx context.Context) ([]migration.DatabaseLock, error) {
	rows, err := s.platformDB.QueryContext(ctx, `
		SELECT database_id, job_id, execution_id, worker_id, lock_state, acquired_at, lease_expires_at
		FROM database_locks
		WHERE lock_state = 'held_uncertain'
		ORDER BY acquired_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]migration.DatabaseLock, 0)
	for rows.Next() {
		var lock migration.DatabaseLock
		var state string
		if err := rows.Scan(
			&lock.DatabaseID, &lock.JobID, &lock.ExecutionID, &lock.WorkerID, &state, &lock.AcquiredAt, &lock.LeaseExpiresAt,
		); err != nil {
			return nil, err
		}
		lock.LockState = migration.LockState(state)
		out = append(out, lock)
	}
	return out, rows.Err()
}
