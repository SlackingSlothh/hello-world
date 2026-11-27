package migration

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"hello-world/internal/observability"
)

func (s *Service) EnqueueExecute(ctx context.Context, principal Principal, databaseID string, migrationID int64) (*EnqueueResult, error) {
	return s.enqueueStandard(ctx, principal, databaseID, migrationID, JobActionExecute)
}

func (s *Service) EnqueueRollback(ctx context.Context, principal Principal, databaseID string, migrationID int64) (*EnqueueResult, error) {
	return s.enqueueStandard(ctx, principal, databaseID, migrationID, JobActionRollback)
}

func (s *Service) EnqueueForward(ctx context.Context, principal Principal, databaseID string, migrationID int64) (*EnqueueResult, error) {
	return s.enqueueStandard(ctx, principal, databaseID, migrationID, JobActionForward)
}

func (s *Service) enqueueStandard(ctx context.Context, principal Principal, databaseID string, migrationID int64, action JobAction) (*EnqueueResult, error) {
	if s.jobStore == nil {
		return nil, ErrAsyncUnavailable
	}
	if err := s.authorize(ctx, principal, databaseID, ActionExecuteMigration); err != nil {
		return nil, err
	}
	m, err := s.getVerifiedMigration(ctx, databaseID, migrationID)
	if err != nil {
		return nil, err
	}
	switch action {
	case JobActionExecute:
		if m.Status == StatusApplied {
			return nil, ErrAlreadyApplied
		}
		if err := ValidateTransactionalSQL(m.SQLContent); err != nil {
			return nil, err
		}
	case JobActionRollback:
		if m.Status != StatusApplied {
			return nil, ErrMigrationOrder
		}
		if strings.TrimSpace(m.SQLDown) == "" {
			return nil, ErrIrreversible
		}
		if err := ValidateTransactionalSQL(m.SQLDown); err != nil {
			return nil, err
		}
	case JobActionForward:
		if m.Status != StatusApplied {
			return nil, ErrMigrationOrder
		}
		if err := ValidateTransactionalSQL(m.SQLContent); err != nil {
			return nil, err
		}
	default:
		return nil, ErrInvalidJobState
	}

	if err := s.verifyApproved(ctx, databaseID, m.ID, action); err != nil {
		return nil, err
	}

	mode := m.ExecutionMode
	if mode == "" {
		mode = ModeNormal
	}
	var batchAttr *string
	if m.BatchByAttribute != "" {
		attr := m.BatchByAttribute
		batchAttr = &attr
	}

	now := s.nowFn()
	actorName := strings.TrimSpace(principal.Username)
	if actorName == "" {
		actorName = principal.UserID
	}
	job := &Job{
		JobID:            uuid.NewString(),
		MigrationID:      m.ID,
		ExecutionID:      uuid.NewString(),
		DatabaseID:       databaseID,
		ActorID:          principal.UserID,
		ActorUsername:    actorName,
		Action:           action,
		Mode:             mode,
		Status:           JobStatusPending,
		BatchByAttribute: batchAttr,
		BatchSize:        m.BatchSize,
		MaxRetries:       s.maxRetries,
		EnqueuedAt:       now,
		UpdatedAt:        now,
	}
	return s.publishJob(ctx, job)
}

func (s *Service) EnqueueResumeBatch(ctx context.Context, principal Principal, databaseID, pausedJobID string) (*EnqueueResult, error) {
	if s.jobStore == nil {
		return nil, ErrAsyncUnavailable
	}
	if err := s.authorize(ctx, principal, databaseID, ActionExecuteMigration); err != nil {
		return nil, err
	}
	paused, err := s.jobStore.GetJob(ctx, pausedJobID)
	if err != nil {
		return nil, err
	}
	if paused.DatabaseID != databaseID {
		return nil, ErrDatabaseMismatch
	}
	if paused.Mode != ModeBatch || paused.Status != JobStatusPartial {
		return nil, ErrInvalidJobState
	}
	if err := s.verifyApproved(ctx, databaseID, paused.MigrationID, JobActionResumeBatch); err != nil {
		return nil, err
	}

	rootExecID := paused.ExecutionID
	if paused.ParentExecutionID != nil && *paused.ParentExecutionID != "" {
		rootExecID = *paused.ParentExecutionID
	}
	now := s.nowFn()
	actorName := strings.TrimSpace(principal.Username)
	if actorName == "" {
		actorName = principal.UserID
	}
	job := &Job{
		JobID:               uuid.NewString(),
		MigrationID:         paused.MigrationID,
		ExecutionID:         uuid.NewString(),
		ParentExecutionID:   &rootExecID,
		DatabaseID:          databaseID,
		ActorID:             principal.UserID,
		ActorUsername:       actorName,
		Action:              JobActionResumeBatch,
		Mode:                ModeBatch,
		Status:              JobStatusPending,
		BatchByAttribute:    paused.BatchByAttribute,
		BatchSize:           paused.BatchSize,
		TargetScopeMax:      paused.TargetScopeMax,
		LastCommittedBatch:  paused.LastCommittedBatch,
		LastCommittedCursor: paused.LastCommittedCursor,
		CommittedRows:       paused.CommittedRows,
		MaxRetries:          s.maxRetries,
		EnqueuedAt:          now,
		UpdatedAt:           now,
	}
	return s.publishJob(ctx, job)
}

func (s *Service) EnqueueRevertBatch(ctx context.Context, principal Principal, databaseID, pausedJobID string) (*EnqueueResult, error) {
	if s.jobStore == nil {
		return nil, ErrAsyncUnavailable
	}
	if err := s.authorize(ctx, principal, databaseID, ActionExecuteMigration); err != nil {
		return nil, err
	}
	paused, err := s.jobStore.GetJob(ctx, pausedJobID)
	if err != nil {
		return nil, err
	}
	if paused.DatabaseID != databaseID {
		return nil, ErrDatabaseMismatch
	}
	if paused.Mode != ModeBatch || (paused.Status != JobStatusPartial && paused.Status != JobStatusPartiallyReverted) {
		return nil, ErrInvalidJobState
	}
	m, err := s.getVerifiedMigration(ctx, databaseID, paused.MigrationID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(m.SQLDown) == "" {
		return nil, ErrIrreversible
	}
	if err := ValidateTransactionalSQL(m.SQLDown); err != nil {
		return nil, err
	}
	if err := s.verifyApproved(ctx, databaseID, paused.MigrationID, JobActionRevertBatch); err != nil {
		return nil, err
	}

	rootExecID := paused.ExecutionID
	if paused.ParentExecutionID != nil && *paused.ParentExecutionID != "" {
		rootExecID = *paused.ParentExecutionID
	}
	now := s.nowFn()
	actorName := strings.TrimSpace(principal.Username)
	if actorName == "" {
		actorName = principal.UserID
	}
	job := &Job{
		JobID:               uuid.NewString(),
		MigrationID:         paused.MigrationID,
		ExecutionID:         uuid.NewString(),
		ParentExecutionID:   &rootExecID,
		DatabaseID:          databaseID,
		ActorID:             principal.UserID,
		ActorUsername:       actorName,
		Action:              JobActionRevertBatch,
		Mode:                ModeBatch,
		Status:              JobStatusPending,
		BatchByAttribute:    paused.BatchByAttribute,
		BatchSize:           paused.BatchSize,
		TargetScopeMax:      paused.TargetScopeMax,
		LastCommittedBatch:  paused.LastCommittedBatch,
		LastCommittedCursor: paused.LastCommittedCursor,
		CommittedRows:       paused.CommittedRows,
		MaxRetries:          s.maxRetries,
		EnqueuedAt:          now,
		UpdatedAt:           now,
	}
	return s.publishJob(ctx, job)
}

func (s *Service) GetJob(ctx context.Context, principal Principal, jobID string) (*Job, error) {
	if s.jobStore == nil {
		return nil, ErrAsyncUnavailable
	}
	if strings.TrimSpace(principal.UserID) == "" {
		return nil, ErrUnauthorized
	}
	job, err := s.jobStore.GetJob(ctx, strings.TrimSpace(jobID))
	if err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, principal, job.DatabaseID, ActionReadMigration); err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Service) ListJobsByDatabase(ctx context.Context, principal Principal, databaseID string, limit, offset int) ([]Job, error) {
	if s.jobStore == nil {
		return nil, ErrAsyncUnavailable
	}
	if err := s.authorize(ctx, principal, databaseID, ActionReadMigration); err != nil {
		return nil, err
	}
	return s.jobStore.ListJobsByDatabase(ctx, databaseID, limit, offset)
}

func (s *Service) verifyApproved(ctx context.Context, databaseID string, migrationID int64, action JobAction) error {
	if s.approvals == nil {
		return nil
	}
	return s.approvals.VerifyApproved(ctx, databaseID, migrationID, action)
}

func (s *Service) publishJob(ctx context.Context, job *Job) (*EnqueueResult, error) {
	ctx, span := observability.Tracer("datadock/queue").Start(ctx, "queue.publish")
	span.SetAttributes(
		attribute.String("database", job.DatabaseID),
		attribute.Int64("migration_id", job.MigrationID),
		attribute.String("action", string(job.Action)),
		attribute.String("mode", string(job.Mode)),
	)
	defer span.End()

	traceBytes, err := observability.InjectTraceContext(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	job.TraceContext = traceBytes

	if err := s.jobStore.EnqueueJob(ctx, job); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.logAudit(ctx, AuditEvent{
			ActorUserID:   job.ActorID,
			Action:        "queue.publish",
			ObjectType:    "migration_job",
			ObjectID:      job.MigrationID,
			DatabaseID:    job.DatabaseID,
			JobID:         job.JobID,
			ExecutionUUID: job.ExecutionID,
			Status:        "FAILED",
			Error:         err.Error(),
			CreatedAt:     s.nowFn(),
		})
		return nil, err
	}

	s.logAudit(ctx, AuditEvent{
		ActorUserID:   job.ActorID,
		Action:        "queue.publish",
		ObjectType:    "migration_job",
		ObjectID:      job.MigrationID,
		DatabaseID:    job.DatabaseID,
		JobID:         job.JobID,
		ExecutionUUID: job.ExecutionID,
		Status:        string(job.Status),
		CreatedAt:     s.nowFn(),
	})

	return &EnqueueResult{
		JobID:       job.JobID,
		ExecutionID: job.ExecutionID,
		Status:      job.Status,
	}, nil
}
