package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	accessapp "hello-world/app/access"
	catalogapp "hello-world/app/catalog"
	connectionapp "hello-world/app/connection"
	"hello-world/authn"
	"hello-world/handler"
	"hello-world/httpapi"
	"hello-world/migrations"
	"hello-world/persistence"
	"hello-world/session"
	"hello-world/session/test/support"
)

func TestGroupMembershipAndSessions(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_DSN to run PostgreSQL integration tests")
	}
	ctx := context.Background()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	adminDB := stdlib.OpenDB(*config)
	defer adminDB.Close()
	schema := fmt.Sprintf("membership_test_%d", time.Now().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := adminDB.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	store, err := persistence.NewAuthStore(db)
	if err != nil {
		t.Fatal(err)
	}
	service := accessapp.Service{Store: store, Users: store, Groups: store, Memberships: store}
	hash, err := authn.HashPassword("test password")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hash.Encoded()
	if err != nil {
		t.Fatal(err)
	}
	seed := persistence.SeedAccount{ID: "admin", Username: "admin", PasswordHash: encoded, PlatformAdmin: true, GroupID: "admins", GroupName: "Admins"}
	if err := store.SeedAccount(ctx, seed); err != nil {
		t.Fatal(err)
	}
	user, err := service.CreateUser(ctx, "alice", "test password")
	if err != nil {
		t.Fatal(err)
	}
	group, err := service.CreateGroup(ctx, "Analysts")
	if err != nil {
		t.Fatal(err)
	}
	sessionStore, err := persistence.NewSessionStore(db)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := session.NewIdentifierCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := session.NewService(sessionStore, session.DefaultConfig(), cipher, nil)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := authn.NewAuthenticator(store)
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := httpapi.NewAuthenticationHandler(authenticator, sessions, session.Cookies{}, "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := httpapi.NewAuthorizationHandler(store)
	if err != nil {
		t.Fatal(err)
	}
	api := authentication.RequireSameOrigin(handler.New(connectionapp.Service{}, catalogapp.Service{}, service, handler.NewSecurity(authentication, authorization), nil))
	_, adminCookie := support.Create(t, sessions, "admin")
	userIdentifier, userCookie := support.Create(t, sessions, user.ID)
	path := "https://example.com/api/v1/access/groups/" + group.ID + "/members"
	request := func(method, target, origin string, cookie *http.Cookie, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, nil)
		req.Header.Set("Origin", origin)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("%s %s: got %d want %d: %s", method, target, response.Code, status, response.Body.String())
		}
		return response
	}
	request("GET", path, "https://example.com", nil, 401)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		target := path
		if method != "GET" {
			target += "/" + user.ID
		}
		request(method, target, "https://example.com", userCookie, 403)
		if method != "GET" {
			request(method, target, "https://other.example", adminCookie, 403)
		}
	}
	request("PUT", path+"/missing", "https://example.com", adminCookie, 404)
	request("GET", "https://example.com/api/v1/access/groups/missing/members", "https://example.com", adminCookie, 404)
	request("PUT", path+"/"+user.ID, "https://example.com", adminCookie, 204)
	response := request("GET", path, "https://example.com", adminCookie, 200)
	var members []accessapp.GroupMember
	if err := json.Unmarshal(response.Body.Bytes(), &members); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, member := range members {
		if member.ID == user.ID {
			found = member.Member
		}
	}
	if !found {
		t.Fatal("added member missing from list")
	}
	access, err := store.LoadAccess(ctx, user.ID)
	if err != nil || len(access.GroupIDs) != 1 || access.GroupIDs[0] != group.ID {
		t.Fatal("new membership not used by authorization")
	}
	validated, err := sessions.Validate(ctx, userIdentifier)
	if err != nil || validated.Replacement == nil {
		t.Fatalf("membership addition did not rotate session: %v", err)
	}
	userIdentifier = support.Identifier(t, support.Cookie(t, *validated.Replacement))
	request("PUT", path+"/"+user.ID, "https://example.com", adminCookie, 204)
	validated, err = sessions.Validate(ctx, userIdentifier)
	if err != nil || validated.Replacement != nil {
		t.Fatal("duplicate add changed session")
	}
	execSQL(t, db, `CREATE FUNCTION reject_session_change() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure'; END $$`)
	execSQL(t, db, `CREATE TRIGGER reject_session_change BEFORE UPDATE ON managed_sessions FOR EACH ROW WHEN (OLD.user_id <> 'admin') EXECUTE FUNCTION reject_session_change()`)
	request("DELETE", path+"/"+user.ID, "https://example.com", adminCookie, 503)
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM user_group_memberships WHERE user_id=$1 AND group_id=$2`, user.ID, group.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed session update did not roll back membership removal")
	}
	execSQL(t, db, `DROP TRIGGER reject_session_change ON managed_sessions`)
	request("DELETE", path+"/"+user.ID, "https://example.com", adminCookie, 204)
	if _, err := sessions.Validate(ctx, userIdentifier); !errors.Is(err, session.ErrInvalid) {
		t.Fatalf("removed member session still valid: %v", err)
	}
	access, err = store.LoadAccess(ctx, user.ID)
	if err != nil || len(access.GroupIDs) != 0 {
		t.Fatal("removed membership still used by authorization")
	}
	newIdentifier, _ := support.Create(t, sessions, user.ID)
	request("DELETE", path+"/"+user.ID, "https://example.com", adminCookie, 204)
	if _, err := sessions.Validate(ctx, newIdentifier); err != nil {
		t.Fatal("duplicate removal revoked a new login")
	}
	if err := store.SetGroupMember(ctx, seed.GroupID, seed.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedAccount(ctx, seed); err != nil {
		t.Fatal(err)
	}
	access, err = store.LoadAccess(ctx, seed.ID)
	if err != nil || access.IsPlatformAdmin {
		t.Fatal("seed restored removed membership")
	}
}

func execSQL(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}
