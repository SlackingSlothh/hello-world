package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hello-world/internal/services/access"
	"hello-world/internal/services/authz"
	catalogapp "hello-world/internal/services/catalog"
	"hello-world/internal/services/migration"
	"hello-world/internal/transport/http/handler"
)

type routeJobStore struct {
	mu   sync.Mutex
	jobs map[string]*migration.Job
}

func newRouteJobStore() *routeJobStore {
	return &routeJobStore{jobs: make(map[string]*migration.Job)}
}

func (s *routeJobStore) EnqueueJob(_ context.Context, job *migration.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *job
	s.jobs[job.JobID] = &cp
	return nil
}

func (s *routeJobStore) GetJob(_ context.Context, jobID string) (*migration.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[jobID]
	if !ok {
		return nil, migration.ErrJobNotFound
	}
	cp := *j
	return &cp, nil
}

func (s *routeJobStore) GetJobByExecutionID(_ context.Context, executionID string) (*migration.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ExecutionID == executionID {
			cp := *j
			return &cp, nil
		}
	}
	return nil, migration.ErrJobNotFound
}

func (s *routeJobStore) ListJobsByDatabase(_ context.Context, databaseID string, _, _ int) ([]migration.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]migration.Job, 0)
	for _, j := range s.jobs {
		if j.DatabaseID == databaseID {
			out = append(out, *j)
		}
	}
	return out, nil
}

func (s *routeJobStore) ClaimNextJob(context.Context, string, time.Duration, time.Time) (*migration.Job, *migration.DatabaseLock, error) {
	return nil, nil, nil
}
func (s *routeJobStore) SetTargetScopeMax(context.Context, string, string, time.Time) error { return nil }
func (s *routeJobStore) UpdateBatchProgress(context.Context, string, int, string, int64, time.Time) error {
	return nil
}
func (s *routeJobStore) ScheduleRetry(context.Context, string, time.Time, string, string, time.Time) error {
	return nil
}
func (s *routeJobStore) FinishJob(_ context.Context, jobID string, status migration.JobStatus, errMsg, sqlState *string, now time.Time) error {
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
func (s *routeJobStore) RequeueRecoveredJob(context.Context, string, int, *string, int64, time.Time, time.Time) error {
	return nil
}
func (s *routeJobStore) CountJobsByStatus(context.Context) ([]migration.JobStatusMetric, error) {
	return nil, nil
}

func TestAsyncMigrationJobRoutes(t *testing.T) {
	editorAccess := authz.UserAccess{
		UserID:   "editor-1",
		Active:   true,
		GroupIDs: []string{"editors"},
		Grants:   []authz.GroupGrant{{GroupID: "editors", DatabaseID: "conn-1", Level: authz.ViewEdit}},
	}
	viewerAccess := authz.UserAccess{
		UserID:   "viewer-1",
		Active:   true,
		GroupIDs: []string{"readers"},
		Grants:   []authz.GroupGrant{{GroupID: "readers", DatabaseID: "conn-1", Level: authz.ViewOnly}},
	}

	store := newRouteMigrationStore()
	jobStore := newRouteJobStore()
	approved := true
	checker := migration.ApprovalCheckerFunc(func(context.Context, string, int64, migration.JobAction) error {
		if !approved {
			return migration.ErrNotApproved
		}
		return nil
	})

	editorSvc := migration.NewService(
		migration.AccessAuthorizer{Reader: migrationAccessReader{access: editorAccess}},
		store, routeTargetConnector{}, nil,
	)
	editorSvc.ConfigureAsync(jobStore, nil, checker, nil)
	editorAPI := handler.New(connectionappServiceForTest(), catalogapp.Service{}, access.Service{}, migrationRouteSecurity{access: editorAccess}, editorSvc)

	viewerSvc := migration.NewService(
		migration.AccessAuthorizer{Reader: migrationAccessReader{access: viewerAccess}},
		store, routeTargetConnector{}, nil,
	)
	viewerSvc.ConfigureAsync(jobStore, nil, checker, nil)
	viewerAPI := handler.New(connectionappServiceForTest(), catalogapp.Service{}, access.Service{}, migrationRouteSecurity{access: viewerAccess}, viewerSvc)

	createBody := `{"title":"Batch update","sql_content":"UPDATE items SET v = 2 WHERE id >= $1 AND id <= $2","sql_down":"UPDATE items SET v = 1 WHERE id >= $1 AND id <= $2","execution_mode":"batch","batch_by_attribute":"id","batch_size":500}`
	createRec := httptest.NewRecorder()
	editorAPI.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/api/v1/connections/conn-1/migrations", strings.NewReader(createBody)))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 created, got %d: %s", createRec.Code, createRec.Body.String())
	}

	approved = false
	deniedApproval := httptest.NewRecorder()
	editorAPI.ServeHTTP(deniedApproval, httptest.NewRequest(http.MethodPost, "/api/v1/connections/conn-1/migrations/1/execute", nil))
	if deniedApproval.Code != http.StatusConflict {
		t.Fatalf("expected 409 when unapproved, got %d: %s", deniedApproval.Code, deniedApproval.Body.String())
	}

	approved = true
	execRec := httptest.NewRecorder()
	editorAPI.ServeHTTP(execRec, httptest.NewRequest(http.MethodPost, "/api/v1/connections/conn-1/migrations/1/execute", nil))
	if execRec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 accepted, got %d: %s", execRec.Code, execRec.Body.String())
	}
	var enq migration.EnqueueResult
	if err := json.Unmarshal(execRec.Body.Bytes(), &enq); err != nil || enq.JobID == "" || enq.Status != migration.JobStatusPending {
		t.Fatalf("unexpected enqueue response: %s", execRec.Body.String())
	}

	getRec := httptest.NewRecorder()
	viewerAPI.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+enq.JobID, nil))
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), enq.JobID) {
		t.Fatalf("expected viewer to poll job status, got %d: %s", getRec.Code, getRec.Body.String())
	}

	listRec := httptest.NewRecorder()
	viewerAPI.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/v1/connections/conn-1/jobs", nil))
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), enq.JobID) {
		t.Fatalf("expected viewer to list jobs, got %d: %s", listRec.Code, listRec.Body.String())
	}

	_ = jobStore.FinishJob(context.Background(), enq.JobID, migration.JobStatusPartial, nil, nil, time.Now().UTC())

	viewerResume := httptest.NewRecorder()
	viewerAPI.ServeHTTP(viewerResume, httptest.NewRequest(http.MethodPost, "/api/v1/connections/conn-1/jobs/"+enq.JobID+"/resume", nil))
	if viewerResume.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for ViewOnly resume, got %d", viewerResume.Code)
	}

	resumeRec := httptest.NewRecorder()
	editorAPI.ServeHTTP(resumeRec, httptest.NewRequest(http.MethodPost, "/api/v1/connections/conn-1/jobs/"+enq.JobID+"/resume", nil))
	if resumeRec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 accepted on resume, got %d: %s", resumeRec.Code, resumeRec.Body.String())
	}

	revertRec := httptest.NewRecorder()
	editorAPI.ServeHTTP(revertRec, httptest.NewRequest(http.MethodPost, "/api/v1/connections/conn-1/jobs/"+enq.JobID+"/revert", nil))
	if revertRec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 accepted on revert, got %d: %s", revertRec.Code, revertRec.Body.String())
	}
}
