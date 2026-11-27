package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hello-world/internal/database/target/manageddb"
	accessapp "hello-world/internal/services/access"
	"hello-world/internal/services/authz"
	catalogapp "hello-world/internal/services/catalog"
	connectionapp "hello-world/internal/services/connection"
	"hello-world/internal/services/migration"
	"hello-world/internal/transport/http/handler"
)

type identityKey struct{}

type migrationRouteSecurity struct {
	access authz.UserAccess
}

func (s migrationRouteSecurity) RequireUser(next http.Handler) http.Handler {
	return s.withIdentity(next)
}

func (s migrationRouteSecurity) RequireAdmin(next http.Handler) http.Handler {
	return s.withIdentity(next)
}

func (s migrationRouteSecurity) RequireConnectionAccess(action authz.Action, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), identityKey{}, s.access.UserID))
		if err := authz.Authorize(s.access, action, r.PathValue("id")); err != nil {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s migrationRouteSecurity) withIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, s.access.UserID)))
	})
}

func (migrationRouteSecurity) VisibleConnections(_ context.Context, items []manageddb.Connection) ([]manageddb.Connection, error) {
	return items, nil
}

func (migrationRouteSecurity) UserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(identityKey{}).(string)
	return id, ok && id != ""
}

type migrationAccessReader struct {
	access authz.UserAccess
}

func (r migrationAccessReader) LoadAccess(_ context.Context, _ string) (authz.UserAccess, error) {
	return r.access, nil
}

type routeMigrationStore struct {
	nextMigrationID int64
	nextRunID       int64
	migrations      map[int64]*migration.Migration
	runs            map[int64][]migration.MigrationRun
}

func newRouteMigrationStore() *routeMigrationStore {
	return &routeMigrationStore{nextMigrationID: 1, nextRunID: 1, migrations: map[int64]*migration.Migration{}, runs: map[int64][]migration.MigrationRun{}}
}

func (s *routeMigrationStore) CreateMigration(_ context.Context, item *migration.Migration) error {
	item.ID = s.nextMigrationID
	s.nextMigrationID++
	copy := *item
	s.migrations[item.ID] = &copy
	return nil
}

func (s *routeMigrationStore) GetMigrationByID(_ context.Context, id int64) (*migration.Migration, error) {
	item, ok := s.migrations[id]
	if !ok {
		return nil, migration.ErrNotFound
	}
	copy := *item
	return &copy, nil
}

func (s *routeMigrationStore) ListMigrationsByDatabase(_ context.Context, databaseID string) ([]migration.Migration, error) {
	var result []migration.Migration
	for _, item := range s.migrations {
		if item.DatabaseID == databaseID {
			result = append(result, *item)
		}
	}
	return result, nil
}

func (s *routeMigrationStore) UpdateDraftSQL(_ context.Context, item *migration.Migration) error {
	if _, ok := s.migrations[item.ID]; !ok {
		return migration.ErrNotFound
	}
	item.Revision++
	copy := *item
	s.migrations[item.ID] = &copy
	return nil
}

func (s *routeMigrationStore) CreateRun(_ context.Context, run *migration.MigrationRun) error {
	run.ID = s.nextRunID
	s.nextRunID++
	run.AttemptNumber = len(s.runs[run.MigrationID]) + 1
	s.migrations[run.MigrationID].FrozenAt = &run.StartedAt
	s.runs[run.MigrationID] = append(s.runs[run.MigrationID], *run)
	return nil
}

func (s *routeMigrationStore) CompleteRunFailed(_ context.Context, _ int64, migrationID int64, _ string, _ int64, finishedAt time.Time) error {
	s.migrations[migrationID].Status = migration.StatusFailed
	s.migrations[migrationID].UpdatedAt = finishedAt
	return nil
}

func (s *routeMigrationStore) CompleteRunSucceeded(_ context.Context, runID, migrationID int64, _ string, _ int64, finishedAt time.Time) (int64, error) {
	item := s.migrations[migrationID]
	version := int64(1)
	item.Status, item.Version, item.AppliedAt, item.UpdatedAt = migration.StatusApplied, &version, &finishedAt, finishedAt
	for index := range s.runs[migrationID] {
		if s.runs[migrationID][index].ID == runID {
			s.runs[migrationID][index].Status = migration.RunStatusSucceeded
			s.runs[migrationID][index].FinishedAt = &finishedAt
		}
	}
	return version, nil
}

func (s *routeMigrationStore) ListRunsByMigration(_ context.Context, migrationID int64) ([]migration.MigrationRun, error) {
	return append([]migration.MigrationRun(nil), s.runs[migrationID]...), nil
}

func (s *routeMigrationStore) ListMigrationHistory(context.Context, string) ([]migration.HistoryEntry, error) {
	return []migration.HistoryEntry{}, nil
}

type routeTargetConnector struct{}

func (routeTargetConnector) BeginTargetTx(context.Context, string) (migration.TargetTx, error) {
	return routeTargetTx{}, nil
}

type routeTargetTx struct{}

func (routeTargetTx) Dialect() migration.TargetDialect                { return migration.DialectPostgres }
func (routeTargetTx) AcquireTargetLock(context.Context, string) error { return nil }
func (routeTargetTx) ExecSQL(context.Context, string) error           { return nil }
func (routeTargetTx) ExplainSQL(context.Context, string, bool) (string, error) {
	return `[{"Plan":{"Node Type":"Seq Scan","Startup Cost":0,"Total Cost":1,"Plan Rows":1}}]`, nil
}
func (routeTargetTx) InspectSchema(context.Context, string) ([]migration.TableSchema, error) {
	return nil, nil
}
func (routeTargetTx) Commit(context.Context) error   { return nil }
func (routeTargetTx) Rollback(context.Context) error { return nil }

func TestMigrationRoutes_EndToEndFlow(t *testing.T) {
	access := authz.UserAccess{
		UserID:   "user-42",
		Active:   true,
		GroupIDs: []string{"editors"},
		Grants: []authz.GroupGrant{{
			GroupID: "editors", DatabaseID: "connection-1", Level: authz.ViewEdit,
		}},
	}
	store := newRouteMigrationStore()
	migrationService := migration.NewService(
		migration.AccessAuthorizer{Reader: migrationAccessReader{access: access}},
		store,
		routeTargetConnector{},
		nil,
	)
	api := handler.New(
		connectionappServiceForTest(),
		catalogapp.Service{},
		accessapp.Service{},
		migrationRouteSecurity{access: access},
		migrationService,
	)

	for _, invalid := range []string{
		`{"title":"Missing rollback definition","sql_content":"CREATE TABLE categories (id INT);"}`,
		`{"title":"Two rollback definitions","sql_content":"CREATE TABLE categories (id INT);","sql_down":"DROP TABLE categories;","irreversible_reason":"Cannot restore data"}`,
		`{"title":"Unsafe down","sql_content":"CREATE TABLE categories (id INT);","sql_down":"COMMIT;"}`,
	} {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/connections/connection-1/migrations", strings.NewReader(invalid)))
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid rollback definition: got %d: %s", response.Code, response.Body.String())
		}
	}
	body := `{"title":"Tambah tabel kategori","description":"Migrasi awal","sql_content":"CREATE TABLE categories (id SERIAL PRIMARY KEY, name TEXT NOT NULL);","sql_down":"DROP TABLE categories;"}`
	createdResponse := httptest.NewRecorder()
	api.ServeHTTP(createdResponse, httptest.NewRequest(http.MethodPost, "/api/v1/connections/connection-1/migrations", strings.NewReader(body)))
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create migration: got %d: %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created migration.Migration
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.CreatedBy != access.UserID || created.UpdatedBy != access.UserID {
		t.Fatalf("audit identity should come from the authenticated session: %+v", created)
	}
	if created.Revision != 1 || created.SQLDown != "DROP TABLE categories;" {
		t.Fatalf("create did not return the saved definition: %+v", created)
	}

	updatedResponse := httptest.NewRecorder()
	updatedBody := `{"title":"Kategori","description":"Diperbarui","sql_content":"CREATE TABLE categories (id SERIAL PRIMARY KEY, name TEXT NOT NULL);","irreversible_reason":"Existing data must be preserved"}`
	api.ServeHTTP(updatedResponse, httptest.NewRequest(http.MethodPatch, "/api/v1/connections/connection-1/migrations/1", strings.NewReader(updatedBody)))
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("update migration: got %d: %s", updatedResponse.Code, updatedResponse.Body.String())
	}
	var updated migration.Migration
	if err := json.Unmarshal(updatedResponse.Body.Bytes(), &updated); err != nil ||
		updated.Revision != 2 || updated.IrreversibleReason != "Existing data must be preserved" || updated.SQLDown != "" {
		t.Fatalf("update did not replace the definition: %s", updatedResponse.Body.String())
	}

	detailResponse := httptest.NewRecorder()
	api.ServeHTTP(detailResponse, httptest.NewRequest(http.MethodGet, "/api/v1/connections/connection-1/migrations/1", nil))
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("get migration: got %d: %s", detailResponse.Code, detailResponse.Body.String())
	}

	executeResponse := httptest.NewRecorder()
	api.ServeHTTP(executeResponse, httptest.NewRequest(http.MethodPost, "/api/v1/connections/connection-1/migrations/1/execute", nil))
	if executeResponse.Code != http.StatusOK {
		t.Fatalf("execute migration: got %d: %s", executeResponse.Code, executeResponse.Body.String())
	}

	listResponse := httptest.NewRecorder()
	api.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/v1/connections/connection-1/migrations", nil))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"version":1`) {
		t.Fatalf("list migrations should return the applied version: %d: %s", listResponse.Code, listResponse.Body.String())
	}
}

func TestMigrationRoutes_ViewOnlyCannotMutate(t *testing.T) {
	access := authz.UserAccess{
		UserID:   "viewer",
		Active:   true,
		GroupIDs: []string{"readers"},
		Grants: []authz.GroupGrant{{
			GroupID: "readers", DatabaseID: "connection-1", Level: authz.ViewOnly,
		}},
	}
	service := migration.NewService(
		migration.AccessAuthorizer{Reader: migrationAccessReader{access: access}},
		newRouteMigrationStore(),
		routeTargetConnector{},
		nil,
	)
	api := handler.New(connectionappServiceForTest(), catalogapp.Service{}, accessapp.Service{}, migrationRouteSecurity{access: access}, service)

	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/connections/connection-1/migrations",
		strings.NewReader(`{"title":"nope","sql_content":"CREATE TABLE denied (id INT);"}`)))
	if response.Code != http.StatusForbidden {
		t.Fatalf("ViewOnly migration create should be forbidden, got %d: %s", response.Code, response.Body.String())
	}
	history := httptest.NewRecorder()
	api.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/api/v1/connections/connection-1/migrations/history", nil))
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), `"runs":[]`) {
		t.Fatalf("ViewOnly should see an empty history: got %d: %s", history.Code, history.Body.String())
	}
	forbidden := httptest.NewRecorder()
	api.ServeHTTP(forbidden, httptest.NewRequest(http.MethodGet, "/api/v1/connections/another-db/migrations/history", nil))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("history from another connection should be forbidden: %d", forbidden.Code)
	}
}

func connectionappServiceForTest() connectionapp.Service { return connectionapp.Service{} }
