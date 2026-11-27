package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	persistence "hello-world/internal/database/platform"
	target "hello-world/internal/database/target/migration"
	"hello-world/internal/services/migration"
)

func TestMigrationRequestRequiresEveryGrantedGroupLeader(t *testing.T) {
	platformDB, _ := migrationTestDatabases(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	execSQL(t, platformDB, `INSERT INTO platform_users(id, username, password_hash) VALUES
		('writer', 'writer', 'unused'), ('viewer', 'viewer', 'unused'), ('replacement', 'replacement', 'unused')`)
	execSQL(t, platformDB, `INSERT INTO access_groups(id, name) VALUES
		('write', 'Writers'), ('read', 'Readers'), ('legacy', 'Legacy'), ('excluded', 'No access')`)
	execSQL(t, platformDB, `INSERT INTO user_group_memberships(user_id, group_id) VALUES
		('editor', 'write'), ('writer', 'write'), ('viewer', 'read'), ('replacement', 'legacy'), ('replacement', 'read')`)
	execSQL(t, platformDB, `UPDATE access_groups SET leader_user_id = 'writer' WHERE id = 'write'`)
	execSQL(t, platformDB, `UPDATE access_groups SET leader_user_id = 'viewer' WHERE id = 'read'`)
	execSQL(t, platformDB, `INSERT INTO connection_grants(group_id, database_id, access_level) VALUES
		('write', 'db', 2), ('read', 'db', 1), ('legacy', 'db', 1), ('excluded', 'db', 0)`)
	authStore, err := persistence.NewAuthStore(platformDB)
	if err != nil {
		t.Fatal(err)
	}
	service := migration.NewService(migration.AccessAuthorizer{Reader: authStore}, persistence.NewPostgresStore(platformDB), nil, nil)
	editor := migration.Principal{UserID: "editor"}
	viewer := migration.Principal{UserID: "viewer"}
	const sqlUp = "CREATE TABLE samples (id INT PRIMARY KEY);"
	draft, err := service.CreateDraft(ctx, editor, migration.CreateDraftInput{
		DatabaseID: "db", Title: "Create samples", SQLContent: sqlUp, SQLDown: "DROP TABLE samples;",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitMigrationRequest(ctx, viewer, "db", draft.ID, draft.Revision, migration.RunApply); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("view-only users should not be able to submit: %v", err)
	}
	request, err := service.SubmitMigrationRequest(ctx, editor, "db", draft.ID, draft.Revision, migration.RunApply)
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != migration.RequestPendingReview || request.SQLSnapshot != sqlUp || request.Revision != draft.Revision ||
		request.RequestedBy != editor.UserID || len(request.Reviews) != 3 {
		t.Fatalf("unexpected submitted request: %+v", request)
	}
	if _, err := service.ApproveMigrationRequest(ctx, editor, "db", request.ID, "write"); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("membership alone should not authorize approval: %v", err)
	}
	if _, err := service.ApproveMigrationRequest(ctx, viewer, "db", request.ID, "write"); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("a leader cannot approve on behalf of another group: %v", err)
	}
	approvals := make(chan error, 2)
	for group, leader := range map[string]string{"read": "viewer", "write": "writer"} {
		go func() {
			got, err := service.ApproveMigrationRequest(ctx, migration.Principal{UserID: leader}, "db", request.ID, group)
			if err == nil && got.Status != migration.RequestPendingReview {
				err = errors.New("group without a leader should keep the request pending")
			}
			approvals <- err
		}()
	}
	for range 2 {
		if err := <-approvals; err != nil {
			t.Fatal(err)
		}
	}
	execSQL(t, platformDB, `UPDATE access_groups SET leader_user_id = 'replacement' WHERE id = 'legacy'`)
	replacement := migration.Principal{UserID: "replacement"}
	approved, err := service.ApproveMigrationRequest(ctx, replacement, "db", request.ID, "legacy")
	if err != nil || approved.Status != migration.RequestApproved {
		t.Fatalf("last leader approval should approve the request: %+v, %v", approved, err)
	}
	if repeated, err := service.ApproveMigrationRequest(ctx, replacement, "db", request.ID, "legacy"); err != nil || repeated.Status != migration.RequestApproved {
		t.Fatalf("repeated approval should leave the request approved: %+v, %v", repeated, err)
	}
	jobs, err := persistence.NewJobStore(platformDB).ListJobsByDatabase(ctx, "db", 10, 0)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("approval should not enqueue a job: %+v, %v", jobs, err)
	}
	execSQL(t, platformDB, `UPDATE access_groups SET leader_user_id = 'replacement' WHERE id = 'read'`)
	if pending, err := service.GetMigrationRequest(ctx, editor, "db", request.ID); err != nil || pending.Status != migration.RequestPendingReview {
		t.Fatalf("a transferred leader must approve for the group: %+v, %v", pending, err)
	}
	if reviewed, err := service.ApproveMigrationRequest(ctx, replacement, "db", request.ID, "read"); err != nil || reviewed.Status != migration.RequestApproved {
		t.Fatalf("the new leader should be able to approve for the group: %+v, %v", reviewed, err)
	}
	if _, err := service.UpdateDraft(ctx, editor, migration.UpdateDraftInput{
		DatabaseID: "db", MigrationID: draft.ID, SQLContent: "CREATE TABLE other_samples (id INT);", SQLDown: "DROP TABLE other_samples;",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApproveMigrationRequest(ctx, replacement, "db", request.ID, "read"); !errors.Is(err, migration.ErrRevisionChanged) {
		t.Fatalf("a stale revision should reject further approval: %v", err)
	}
	if stale, err := service.GetMigrationRequest(ctx, editor, "db", request.ID); err != nil || stale.Status != migration.RequestStale || stale.SQLSnapshot != sqlUp {
		t.Fatalf("request should retain the original SQL and report the revision change: %+v, %v", stale, err)
	}
}

func TestMigrationRequestPinsRollbackAndForwardTargets(t *testing.T) {
	platformDB, targetDB := migrationTestDatabases(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	execSQL(t, platformDB, `INSERT INTO access_groups(id, name) VALUES ('write', 'Writers')`)
	execSQL(t, platformDB, `INSERT INTO user_group_memberships(user_id, group_id) VALUES ('editor', 'write')`)
	execSQL(t, platformDB, `UPDATE access_groups SET leader_user_id = 'editor' WHERE id = 'write'`)
	execSQL(t, platformDB, `INSERT INTO connection_grants(group_id, database_id, access_level) VALUES ('write', 'db', 2)`)
	connector := target.NewSQLTargetConnector(func(context.Context, string) (*sql.DB, error) { return targetDB, nil })
	service := migration.NewService(migration.AuthorizerFunc(func(context.Context, migration.Principal, string, migration.Action) error {
		return nil
	}), persistence.NewPostgresStore(platformDB), connector, nil)
	editor := migration.Principal{UserID: "editor"}
	draft, err := service.CreateDraft(ctx, editor, migration.CreateDraftInput{
		DatabaseID: "db", Title: "Create samples", SQLContent: "CREATE TABLE samples (id INT);", SQLDown: "DROP TABLE samples;",
	})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := service.ExecuteMigration(ctx, editor, "db", draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	request, err := service.SubmitMigrationRequest(ctx, editor, "db", draft.ID, draft.Revision, migration.RunRollback)
	if err != nil || request.TargetRunID == nil || *request.TargetRunID != applied.Run.ID || request.SQLSnapshot != draft.SQLDown {
		t.Fatalf("rollback request must pin the active execution and SQL down: %+v, %v", request, err)
	}
	rolledBack, err := service.RollbackMigration(ctx, editor, "db", draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	request, err = service.SubmitMigrationRequest(ctx, editor, "db", draft.ID, draft.Revision, migration.RunForward)
	if err != nil || request.TargetRunID == nil || *request.TargetRunID != rolledBack.Run.ID || request.SQLSnapshot != draft.SQLContent {
		t.Fatalf("forward request must pin the rollback execution and SQL up: %+v, %v", request, err)
	}
	if _, err := service.ForwardMigration(ctx, editor, "db", draft.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RollbackMigration(ctx, editor, "db", draft.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApproveMigrationRequest(ctx, editor, "db", request.ID, "write"); !errors.Is(err, migration.ErrMigrationOrder) {
		t.Fatalf("pending another rollback of the same migration must invalidate the original target: %v", err)
	}
	if stale, err := service.GetMigrationRequest(ctx, editor, "db", request.ID); err != nil || stale.Status != migration.RequestStale {
		t.Fatalf("the original forward request should be stale: %+v, %v", stale, err)
	}
}
