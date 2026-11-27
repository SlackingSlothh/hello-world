package migration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"hello-world/internal/observability"
)

type inMemoryJobStore struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

func newInMemoryJobStore() *inMemoryJobStore {
	return &inMemoryJobStore{jobs: make(map[string]*Job)}
}

func (s *inMemoryJobStore) EnqueueJob(_ context.Context, job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := *job
	s.jobs[job.JobID] = &copied
	return nil
}

func (s *inMemoryJobStore) GetJob(_ context.Context, jobID string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[jobID]
	if !ok {
		return nil, ErrJobNotFound
	}
	copied := *j
	return &copied, nil
}

func (s *inMemoryJobStore) GetJobByExecutionID(_ context.Context, executionID string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ExecutionID == executionID {
			copied := *j
			return &copied, nil
		}
	}
	return nil, ErrJobNotFound
}

func (s *inMemoryJobStore) ListJobsByDatabase(_ context.Context, databaseID string, _, _ int) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Job
	for _, j := range s.jobs {
		if j.DatabaseID == databaseID {
			out = append(out, *j)
		}
	}
	return out, nil
}

func (s *inMemoryJobStore) ClaimNextJob(context.Context, string, time.Duration, time.Time) (*Job, *DatabaseLock, error) {
	return nil, nil, nil
}

func (s *inMemoryJobStore) SetTargetScopeMax(_ context.Context, jobID, scopeMax string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[jobID]; ok {
		v := scopeMax
		j.TargetScopeMax = &v
		j.UpdatedAt = now
	}
	return nil
}

func (s *inMemoryJobStore) UpdateBatchProgress(_ context.Context, jobID string, lastBatch int, lastCursor string, committedRows int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[jobID]; ok {
		j.LastCommittedBatch = lastBatch
		c := lastCursor
		j.LastCommittedCursor = &c
		j.CommittedRows = committedRows
		j.UpdatedAt = now
	}
	return nil
}

func (s *inMemoryJobStore) ScheduleRetry(_ context.Context, jobID string, nextRetryAt time.Time, errMsg, sqlState string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[jobID]; ok {
		j.Status = JobStatusPending
		j.NextRetryAt = &nextRetryAt
		j.ErrorMessage = &errMsg
		if sqlState != "" {
			j.SQLState = &sqlState
		}
		j.UpdatedAt = now
	}
	return nil
}

func (s *inMemoryJobStore) FinishJob(_ context.Context, jobID string, status JobStatus, errMsg, sqlState *string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[jobID]; ok {
		j.Status = status
		j.ErrorMessage = errMsg
		j.SQLState = sqlState
		j.FinishedAt = &now
		j.UpdatedAt = now
	}
	return nil
}

func (s *inMemoryJobStore) RequeueRecoveredJob(_ context.Context, jobID string, lastBatch int, lastCursor *string, committedRows int64, nextRetryAt time.Time, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[jobID]; ok {
		j.Status = JobStatusPending
		j.LastCommittedBatch = lastBatch
		j.LastCommittedCursor = lastCursor
		j.CommittedRows = committedRows
		j.NextRetryAt = &nextRetryAt
		j.UpdatedAt = now
	}
	return nil
}

func (s *inMemoryJobStore) CountJobsByStatus(context.Context) ([]JobStatusMetric, error) {
	return nil, nil
}

func TestEnqueueExecuteTracePropagationAndAuthz(t *testing.T) {
	telemetry, err := observability.InitTelemetry(context.Background(), observability.TelemetryConfig{
		Exporter:    "none",
		ServiceName: "datadock-enqueue-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = telemetry.Shutdown(context.Background()) }()

	authz := newMockAuthorizer()
	editor := Principal{UserID: "editor-1", Username: "andi"}
	viewer := Principal{UserID: "viewer-1", Username: "siti"}
	authz.Grant(editor.UserID, "db-1", "VIEW_EDIT")
	authz.Grant(viewer.UserID, "db-1", "VIEW_ONLY")

	store := newInMemoryStore()
	jobStore := newInMemoryJobStore()
	svc := NewService(authz, store, &mockTargetDB{}, nil)
	svc.ConfigureAsync(jobStore, nil, AllowAllApprovals{}, nil)

	batchSize := 500
	draft, err := svc.CreateDraft(context.Background(), editor, CreateDraftInput{
		DatabaseID:       "db-1",
		Title:            "Batch update",
		SQLContent:       "UPDATE items SET v = 2 WHERE id >= $1 AND id <= $2",
		SQLDown:          "UPDATE items SET v = 1 WHERE id >= $1 AND id <= $2",
		ExecutionMode:    ModeBatch,
		BatchByAttribute: "id",
		BatchSize:        &batchSize,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.EnqueueExecute(context.Background(), viewer, "db-1", draft.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for View Only user, got %v", err)
	}

	res, err := svc.EnqueueExecute(context.Background(), editor, "db-1", draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.JobID == "" || res.ExecutionID == "" || res.Status != JobStatusPending {
		t.Fatalf("unexpected enqueue result: %+v", res)
	}

	stored, err := svc.GetJob(context.Background(), viewer, res.JobID)
	if err != nil {
		t.Fatalf("expected View Only user to read job status, got %v", err)
	}
	var carrier map[string]string
	if err := json.Unmarshal(stored.TraceContext, &carrier); err != nil || carrier["traceparent"] == "" {
		t.Fatalf("expected W3C traceparent in job.TraceContext, got %s", string(stored.TraceContext))
	}
}

func TestEnqueueApprovalEnforcementAndBatchRecovery(t *testing.T) {
	authz := newMockAuthorizer()
	editor := Principal{UserID: "editor-1", Username: "andi"}
	authz.Grant(editor.UserID, "db-1", "VIEW_EDIT")

	store := newInMemoryStore()
	jobStore := newInMemoryJobStore()
	approved := false
	checker := ApprovalCheckerFunc(func(context.Context, string, int64, JobAction) error {
		if !approved {
			return ErrNotApproved
		}
		return nil
	})
	svc := NewService(authz, store, &mockTargetDB{}, nil)
	svc.ConfigureAsync(jobStore, nil, checker, nil)

	batchSize := 100
	draft, err := svc.CreateDraft(context.Background(), editor, CreateDraftInput{
		DatabaseID:       "db-1",
		Title:            "Reversible batch",
		SQLContent:       "UPDATE items SET v = 2 WHERE id >= $1 AND id <= $2",
		SQLDown:          "UPDATE items SET v = 1 WHERE id >= $1 AND id <= $2",
		ExecutionMode:    ModeBatch,
		BatchByAttribute: "id",
		BatchSize:        &batchSize,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.EnqueueExecute(context.Background(), editor, "db-1", draft.ID); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("expected ErrNotApproved before approval, got %v", err)
	}

	approved = true
	res, err := svc.EnqueueExecute(context.Background(), editor, "db-1", draft.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.EnqueueResumeBatch(context.Background(), editor, "db-1", res.JobID); !errors.Is(err, ErrInvalidJobState) {
		t.Fatalf("expected ErrInvalidJobState for pending job resume, got %v", err)
	}

	_ = jobStore.SetTargetScopeMax(context.Background(), res.JobID, "1000", time.Now().UTC())
	_ = jobStore.UpdateBatchProgress(context.Background(), res.JobID, 3, "300", 300, time.Now().UTC())
	_ = jobStore.FinishJob(context.Background(), res.JobID, JobStatusPartial, nil, nil, time.Now().UTC())

	resumed, err := svc.EnqueueResumeBatch(context.Background(), editor, "db-1", res.JobID)
	if err != nil {
		t.Fatal(err)
	}
	resumedJob, err := jobStore.GetJob(context.Background(), resumed.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if resumedJob.ParentExecutionID == nil || *resumedJob.ParentExecutionID != res.ExecutionID || resumedJob.LastCommittedBatch != 3 || *resumedJob.LastCommittedCursor != "300" || *resumedJob.TargetScopeMax != "1000" {
		t.Fatalf("resume job did not inherit checkpoint state: %+v", resumedJob)
	}

	reverted, err := svc.EnqueueRevertBatch(context.Background(), editor, "db-1", res.JobID)
	if err != nil {
		t.Fatal(err)
	}
	revertedJob, err := jobStore.GetJob(context.Background(), reverted.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if revertedJob.Action != JobActionRevertBatch || revertedJob.ParentExecutionID == nil || *revertedJob.ParentExecutionID != res.ExecutionID {
		t.Fatalf("unexpected revert job: %+v", revertedJob)
	}

	irrev, err := svc.CreateDraft(context.Background(), editor, CreateDraftInput{
		DatabaseID:         "db-1",
		Title:              "Irreversible batch",
		SQLContent:         "DELETE FROM items WHERE id >= $1 AND id <= $2",
		IrreversibleReason: "deleted rows cannot be restored",
		ExecutionMode:      ModeBatch,
		BatchByAttribute:   "id",
		BatchSize:          &batchSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	irrevRes, err := svc.EnqueueExecute(context.Background(), editor, "db-1", irrev.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = jobStore.FinishJob(context.Background(), irrevRes.JobID, JobStatusPartial, nil, nil, time.Now().UTC())
	if _, err := svc.EnqueueRevertBatch(context.Background(), editor, "db-1", irrevRes.JobID); !errors.Is(err, ErrIrreversible) {
		t.Fatalf("expected ErrIrreversible on revert_batch for irreversible migration, got %v", err)
	}
}

func TestBatchDraftConfigValidation(t *testing.T) {
	authz := newMockAuthorizer()
	editor := Principal{UserID: "editor-1"}
	authz.Grant(editor.UserID, "db-1", "VIEW_EDIT")
	svc := NewService(authz, newInMemoryStore(), &mockTargetDB{}, nil)

	zero := 0
	if _, err := svc.CreateDraft(context.Background(), editor, CreateDraftInput{
		DatabaseID:       "db-1",
		Title:            "Bad batch size",
		SQLContent:       "UPDATE items SET v = 1",
		SQLDown:          "UPDATE items SET v = 0",
		ExecutionMode:    ModeBatch,
		BatchByAttribute: "id",
		BatchSize:        &zero,
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("expected ErrInvalidDefinition for batch_size=0, got %v", err)
	}

	size := 100
	if _, err := svc.CreateDraft(context.Background(), editor, CreateDraftInput{
		DatabaseID:       "db-1",
		Title:            "Bad cursor column",
		SQLContent:       "UPDATE items SET v = 1",
		SQLDown:          "UPDATE items SET v = 0",
		ExecutionMode:    ModeBatch,
		BatchByAttribute: "id; DROP TABLE x",
		BatchSize:        &size,
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("expected ErrInvalidDefinition for invalid batch_by_attribute, got %v", err)
	}
}
