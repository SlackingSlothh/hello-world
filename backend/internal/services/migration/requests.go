package migration

import (
	"context"
	"errors"
	"strings"
	"time"

	"hello-world/internal/observability"
)

var ErrInvalidMigrationRequest = errors.New("migration: invalid request operation or revision")

type RequestStatus string

const (
	RequestPendingReview RequestStatus = "PENDING_REVIEW"
	RequestApproved      RequestStatus = "APPROVED"
	RequestQueued        RequestStatus = "QUEUED"
	RequestStale         RequestStatus = "STALE"
)

type MigrationRequest struct {
	ID          int64         `json:"id"`
	MigrationID int64         `json:"migration_id"`
	DatabaseID  string        `json:"database_id"`
	Operation   RunOperation  `json:"operation"`
	Revision    int64         `json:"revision"`
	TargetRunID *int64        `json:"target_run_id,omitempty"`
	SQLSnapshot string        `json:"sql_snapshot"`
	RequestedBy string        `json:"requested_by"`
	CreatedAt   time.Time     `json:"created_at"`
	Status      RequestStatus `json:"status"`
	JobID       *string       `json:"job_id,omitempty"`
	Reviews     []GroupReview `json:"reviews"`
}

type GroupReview struct {
	GroupID    string     `json:"group_id"`
	GroupName  string     `json:"group_name"`
	LeaderID   *string    `json:"leader_id,omitempty"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
}

type EnqueueResult struct {
	JobID string `json:"job_id"`
}

type RequestStore interface {
	CreateMigrationRequest(context.Context, MigrationRequest) (MigrationRequest, error)
	GetMigrationRequest(ctx context.Context, databaseID string, requestID int64) (MigrationRequest, error)
	ApproveMigrationRequest(ctx context.Context, databaseID string, requestID int64, groupID, reviewerID string, approvedAt time.Time) (MigrationRequest, error)
	EnqueueMigrationRequest(ctx context.Context, databaseID string, requestID int64, actorID string, enqueuedAt time.Time, traceContext []byte) (jobID string, created bool, err error)
}

func (s *Service) SubmitMigrationRequest(ctx context.Context, principal Principal, databaseID string, migrationID, revision int64, operation RunOperation) (MigrationRequest, error) {
	if err := s.authorize(ctx, principal, databaseID, ActionExecuteMigration); err != nil {
		return MigrationRequest{}, err
	}
	if revision <= 0 || (operation != RunApply && operation != RunRollback && operation != RunForward) {
		return MigrationRequest{}, ErrInvalidMigrationRequest
	}
	request, err := s.store.CreateMigrationRequest(ctx, MigrationRequest{
		MigrationID: migrationID, DatabaseID: databaseID, Operation: operation,
		Revision: revision, RequestedBy: principal.UserID, CreatedAt: s.nowFn(),
	})
	if err == nil {
		s.logAudit(ctx, AuditEvent{
			ActorUserID: principal.UserID, Action: "migration_request_submitted", ObjectType: "migration_request",
			ObjectID: request.ID, DatabaseID: databaseID, Operation: operation, Revision: revision,
			Status: string(request.Status), CreatedAt: request.CreatedAt,
		})
	}
	return request, err
}

func (s *Service) GetMigrationRequest(ctx context.Context, principal Principal, databaseID string, requestID int64) (MigrationRequest, error) {
	if err := s.authorize(ctx, principal, databaseID, ActionReadMigration); err != nil {
		return MigrationRequest{}, err
	}
	return s.store.GetMigrationRequest(ctx, databaseID, requestID)
}

func (s *Service) ApproveMigrationRequest(ctx context.Context, principal Principal, databaseID string, requestID int64, groupID string) (MigrationRequest, error) {
	if err := s.authorize(ctx, principal, databaseID, ActionReadMigration); err != nil {
		return MigrationRequest{}, err
	}
	if strings.TrimSpace(groupID) == "" {
		return MigrationRequest{}, ErrInvalidMigrationRequest
	}
	now := s.nowFn()
	request, err := s.store.ApproveMigrationRequest(ctx, databaseID, requestID, groupID, principal.UserID, now)
	if err == nil {
		s.logAudit(ctx, AuditEvent{
			ActorUserID: principal.UserID, Action: "migration_request_group_approved", ObjectType: "migration_request",
			ObjectID: request.ID, DatabaseID: databaseID, Operation: request.Operation, Revision: request.Revision,
			Status: string(request.Status), CreatedAt: now, Message: groupID,
		})
	}
	return request, err
}

func (s *Service) EnqueueMigrationRequest(ctx context.Context, principal Principal, databaseID string, requestID int64) (EnqueueResult, error) {
	if err := s.authorize(ctx, principal, databaseID, ActionExecuteMigration); err != nil {
		return EnqueueResult{}, err
	}
	traceContext, err := observability.InjectTraceContext(ctx)
	if err != nil {
		return EnqueueResult{}, err
	}
	now := s.nowFn()
	jobID, created, err := s.store.EnqueueMigrationRequest(ctx, databaseID, requestID, principal.UserID, now, traceContext)
	if err != nil {
		return EnqueueResult{}, err
	}
	if created {
		s.logAudit(ctx, AuditEvent{
			ActorUserID: principal.UserID, Action: "migration_request_enqueued", ObjectType: "migration_request",
			ObjectID: requestID, DatabaseID: databaseID, JobID: jobID, Status: string(RequestQueued), CreatedAt: now,
		})
	}
	return EnqueueResult{JobID: jobID}, nil
}
