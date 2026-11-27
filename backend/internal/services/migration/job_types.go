package migration

import (
	"errors"
	"time"
)

var (
	ErrJobNotFound     = errors.New("migration: job not found")
	ErrLockConflict    = errors.New("migration: database lock already held")
	ErrLockNotFound    = errors.New("migration: database lock not found")
	ErrNotApproved     = errors.New("migration: all required group leaders must approve before execution")
	ErrInvalidJobState = errors.New("migration: invalid job state for requested operation")
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
