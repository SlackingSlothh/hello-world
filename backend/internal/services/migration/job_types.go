package migration

import (
	"context"
	"errors"
	"time"
)

var (
	ErrJobNotFound     = errors.New("migration: job not found")
	ErrLockConflict    = errors.New("migration: database lock already held")
	ErrLockNotFound    = errors.New("migration: database lock not found")
	ErrNotApproved     = errors.New("migration: all required group leaders must approve before execution")
	ErrInvalidJobState = errors.New("migration: invalid job state for requested operation")
	ErrAsyncUnavailable = errors.New("migration: async job queue is not configured")
)

type ExecutionMode string

const (
	ModeNormal ExecutionMode = "normal"
	ModeBatch  ExecutionMode = "batch"
)

type JobAction string

const (
	JobActionExecute     JobAction = "execute"
	JobActionRollback    JobAction = "rollback"
	JobActionForward     JobAction = "forward"
	JobActionResumeBatch JobAction = "resume_batch"
	JobActionRevertBatch JobAction = "revert_batch"
)

type JobStatus string

const (
	JobStatusPending           JobStatus = "pending"
	JobStatusRunning           JobStatus = "running"
	JobStatusSuccess           JobStatus = "success"
	JobStatusFailed            JobStatus = "failed"
	JobStatusPartial           JobStatus = "partial"
	JobStatusPartiallyReverted JobStatus = "partially_reverted"
	JobStatusUncertain         JobStatus = "uncertain"
)

type LockState string

const (
	LockStateActive        LockState = "active"
	LockStateHeldPartial   LockState = "held_partial"
	LockStateHeldUncertain LockState = "held_uncertain"
)

type CheckpointDirection string

const (
	DirectionUp   CheckpointDirection = "up"
	DirectionDown CheckpointDirection = "down"
)

type Job struct {
	JobID               string        `json:"job_id"`
	MigrationID         int64         `json:"migration_id"`
	ExecutionID         string        `json:"execution_id"`
	ParentExecutionID   *string       `json:"parent_execution_id,omitempty"`
	DatabaseID          string        `json:"database_id"`
	ActorID             string        `json:"actor_id"`
	ActorUsername       string        `json:"actor_username"`
	Action              JobAction     `json:"action"`
	Mode                ExecutionMode `json:"mode"`
	Status              JobStatus     `json:"status"`
	BatchByAttribute    *string       `json:"batch_by_attribute,omitempty"`
	BatchSize           *int          `json:"batch_size,omitempty"`
	TargetScopeMax      *string       `json:"target_scope_max,omitempty"`
	LastCommittedBatch  int           `json:"last_committed_batch"`
	LastCommittedCursor *string       `json:"last_committed_cursor,omitempty"`
	CommittedRows       int64         `json:"committed_rows"`
	Attempt             int           `json:"attempt"`
	MaxRetries          int           `json:"max_retries"`
	NextRetryAt         *time.Time    `json:"next_retry_at,omitempty"`
	TraceContext        []byte        `json:"-"`
	ErrorMessage        *string       `json:"error_message,omitempty"`
	SQLState            *string       `json:"sqlstate,omitempty"`
	EnqueuedAt          time.Time     `json:"enqueued_at"`
	StartedAt           *time.Time    `json:"started_at,omitempty"`
	FinishedAt          *time.Time    `json:"finished_at,omitempty"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

type DatabaseLock struct {
	DatabaseID     string    `json:"database_id"`
	JobID          string    `json:"job_id"`
	ExecutionID    string    `json:"execution_id"`
	WorkerID       string    `json:"worker_id"`
	LockState      LockState `json:"lock_state"`
	AcquiredAt     time.Time `json:"acquired_at"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
}

type BatchCheckpoint struct {
	ExecutionID      string              `json:"execution_id"`
	BatchNumber      int                 `json:"batch_number"`
	Direction        CheckpointDirection `json:"direction"`
	CursorStart      string              `json:"cursor_start"`
	CursorEnd        string              `json:"cursor_end"`
	RowsAffected     int64               `json:"rows_affected"`
	RecoveryMetadata []byte              `json:"recovery_metadata,omitempty"`
	CommittedAt      time.Time           `json:"committed_at"`
}

type JobStatusMetric struct {
	DatabaseID string
	Status     JobStatus
	Count      int64
}

type BatchStepRequest struct {
	ExecutionID    string
	BatchNumber    int
	SQLQuery       string
	CursorColumn   string
	LastCursor     string
	TargetScopeMax string
	BatchSize      int
}

type RevertStepRequest struct {
	ExecutionID  string
	Checkpoint   BatchCheckpoint
	SQLDown      string
	CursorColumn string
	BatchSize    int
}

type BatchStepResult struct {
	Checkpoint       BatchCheckpoint
	AlreadyCommitted bool
	Done             bool
}

type EnqueueResult struct {
	JobID       string    `json:"job_id"`
	ExecutionID string    `json:"execution_id,omitempty"`
	Status      JobStatus `json:"status,omitempty"`
}

type ApprovalChecker interface {
	VerifyApproved(ctx context.Context, databaseID string, migrationID int64, action JobAction) error
}

type ApprovalCheckerFunc func(ctx context.Context, databaseID string, migrationID int64, action JobAction) error

func (fn ApprovalCheckerFunc) VerifyApproved(ctx context.Context, databaseID string, migrationID int64, action JobAction) error {
	return fn(ctx, databaseID, migrationID, action)
}

type AllowAllApprovals struct{}

func (AllowAllApprovals) VerifyApproved(context.Context, string, int64, JobAction) error {
	return nil
}

type JobStore interface {
	EnqueueJob(ctx context.Context, job *Job) error
	GetJob(ctx context.Context, jobID string) (*Job, error)
	GetJobByExecutionID(ctx context.Context, executionID string) (*Job, error)
	ListJobsByDatabase(ctx context.Context, databaseID string, limit, offset int) ([]Job, error)
	ClaimNextJob(ctx context.Context, workerID string, leaseDuration time.Duration, now time.Time) (*Job, *DatabaseLock, error)
	SetTargetScopeMax(ctx context.Context, jobID, scopeMax string, now time.Time) error
	UpdateBatchProgress(ctx context.Context, jobID string, lastBatch int, lastCursor string, committedRows int64, now time.Time) error
	ScheduleRetry(ctx context.Context, jobID string, nextRetryAt time.Time, errMsg, sqlState string, now time.Time) error
	FinishJob(ctx context.Context, jobID string, status JobStatus, errMsg, sqlState *string, now time.Time) error
	RequeueRecoveredJob(ctx context.Context, jobID string, lastBatch int, lastCursor *string, committedRows int64, nextRetryAt time.Time, now time.Time) error
	CountJobsByStatus(ctx context.Context) ([]JobStatusMetric, error)
}

type LockStore interface {
	AcquireLock(ctx context.Context, lock DatabaseLock) error
	TakeoverPartialLock(ctx context.Context, databaseID, jobID, executionID, workerID string, acquiredAt, leaseExpiresAt time.Time) error
	GetLock(ctx context.Context, databaseID string) (*DatabaseLock, error)
	RenewLease(ctx context.Context, databaseID, jobID, workerID string, leaseExpiresAt time.Time) error
	TransitionLockState(ctx context.Context, databaseID, jobID string, newState LockState, executionID string) error
	ReleaseLock(ctx context.Context, databaseID, jobID string) error
	SweepExpiredLeases(ctx context.Context, now time.Time) ([]DatabaseLock, error)
	ListUncertainLocks(ctx context.Context) ([]DatabaseLock, error)
}

type BatchTargetConnector interface {
	EnsureCheckpointSchema(ctx context.Context, databaseID string) error
	ResolveTargetScopeMax(ctx context.Context, databaseID, sqlQuery, cursorCol string) (string, error)
	ExecuteBatchStep(ctx context.Context, databaseID string, req BatchStepRequest) (BatchStepResult, error)
	RevertBatchStep(ctx context.Context, databaseID string, req RevertStepRequest) (BatchStepResult, error)
	ListCheckpoints(ctx context.Context, databaseID, executionID string, direction CheckpointDirection) ([]BatchCheckpoint, error)
	ExecNormalTx(ctx context.Context, databaseID, sqlQuery string) (int64, error)
}
