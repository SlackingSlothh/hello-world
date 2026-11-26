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
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"hello-world/internal/database/platform"
	"hello-world/internal/database/target/manageddb"
	accessapp "hello-world/internal/services/access"
	"hello-world/internal/services/authn"
	catalogapp "hello-world/internal/services/catalog"
	connectionapp "hello-world/internal/services/connection"
	"hello-world/internal/services/session"
	"hello-world/internal/services/session/test/support"
	"hello-world/internal/transport/http/handler"
	"hello-world/internal/transport/http/httpapi"
	"hello-world/migrations"
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
	seed := persistence.SeedAccount{ID: "admin", Username: "admin", PasswordHash: encoded, GroupIsAdmin: true, GroupID: "admins", GroupName: "Admins"}
	if err := store.SeedAccount(ctx, seed); err != nil {
		t.Fatal(err)
	}
	assertLeader := func(groupID string, want sql.NullString) {
		t.Helper()
		var got sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT leader_user_id FROM access_groups WHERE id=$1`, groupID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("group %s leader = %+v, want %+v", groupID, got, want)
		}
	}
	assertLeader(seed.GroupID, sql.NullString{String: seed.ID, Valid: true})
	user, err := service.CreateUser(ctx, "alice", "test password")
	if err != nil {
		t.Fatal(err)
	}
	group := accessapp.Group{ID: "analysts", Name: "Analysts"}
	execSQL(t, db, `INSERT INTO access_groups(id,name) VALUES ('analysts','Analysts')`)
	assertLeader(group.ID, sql.NullString{})
	_, err = db.ExecContext(ctx, `UPDATE access_groups SET leader_user_id=$1 WHERE id=$2`, seed.ID, group.ID)
	var fkErr *pgconn.PgError
	if !errors.As(err, &fkErr) || fkErr.Code != "23503" {
		t.Fatalf("appointing a leader from another group: expected foreign key violation, got %v", err)
	}
	_, err = db.ExecContext(ctx, `DELETE FROM user_group_memberships WHERE user_id=$1 AND group_id=$2`, seed.ID, seed.GroupID)
	if !errors.As(err, &fkErr) || fkErr.Code != "23503" {
		t.Fatalf("removing the current leader: expected foreign key violation, got %v", err)
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
	connectionService := connectionapp.Service{Registry: manageddb.NewRegistry()}
	api := authentication.RequireSameOrigin(handler.New(connectionService, catalogapp.Service{}, service, handler.NewSecurity(authentication, authorization), nil))
	_, adminCookie := support.Create(t, sessions, "admin")
	userIdentifier, userCookie := support.Create(t, sessions, user.ID)
	path := "https://example.com/api/v1/access/groups/" + group.ID + "/members"
	performRequest := func(method, target, origin string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, req)
		return response
	}
	request := func(method, target, origin string, cookie *http.Cookie, status int, body ...string) *httptest.ResponseRecorder {
		t.Helper()
		response := performRequest(method, target, origin, cookie, strings.Join(body, ""))
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
	const origin = "https://example.com"
	const groupsURL = origin + "/api/v1/access/groups"
	execSQL(t, db, `INSERT INTO platform_users(id,username,password_hash,active) VALUES ('dormant','dormant','unused',FALSE)`)
	leaderBody := fmt.Sprintf(`{"leaderId":%q}`, user.ID)
	request("POST", groupsURL, origin, adminCookie, 400, fmt.Sprintf(`{"name":"Writers","memberIds":[%q,"missing"],"leaderId":%q}`, user.ID, user.ID))
	request("POST", groupsURL, origin, adminCookie, 400, `{"name":"Writers","memberIds":["dormant"],"leaderId":"dormant"}`)
	created := request("POST", groupsURL, origin, adminCookie, 201, fmt.Sprintf(`{"name":"Writers","memberIds":[%q,"dormant"],"leaderId":%q}`, user.ID, user.ID))
	var writers accessapp.Group
	if err := json.Unmarshal(created.Body.Bytes(), &writers); err != nil || writers.ID == "" || writers.LeaderID == nil || *writers.LeaderID != user.ID {
		t.Fatalf("incorrect new group: %s", created.Body.String())
	}
	writersURL := groupsURL + "/" + writers.ID
	response = request("GET", writersURL+"/members", origin, adminCookie, 200)
	members = nil
	if err := json.Unmarshal(response.Body.Bytes(), &members); err != nil {
		t.Fatal(err)
	}
	memberIDs := map[string]bool{}
	for _, member := range members {
		memberIDs[member.ID] = member.Member
	}
	if !memberIDs[user.ID] || !memberIDs["dormant"] || memberIDs[seed.ID] {
		t.Fatalf("incorrect initial group memberships: %s", response.Body.String())
	}
	if validated, err := sessions.Validate(ctx, newIdentifier); err != nil || validated.Replacement == nil {
		t.Fatalf("new group membership did not rotate session: %v", err)
	}
	request("DELETE", writersURL+"/members/"+user.ID, origin, adminCookie, 409)
	request("PUT", writersURL+"/leader", origin, adminCookie, 400, `{"leaderId":"dormant"}`)
	request("PUT", writersURL+"/leader", origin, adminCookie, 400, `{"leaderId":"admin"}`)
	request("PUT", groupsURL+"/missing/leader", origin, adminCookie, 404, leaderBody)
	request("PUT", writersURL+"/leader", origin, nil, 401, leaderBody)
	_, ordinaryCookie := support.Create(t, sessions, user.ID)
	request("PUT", writersURL+"/leader", origin, ordinaryCookie, 403, leaderBody)

	const usersURL = origin + "/api/v1/access/users"
	request("GET", usersURL, origin, nil, 401)
	request("GET", usersURL, origin, ordinaryCookie, 403)
	response = request("GET", usersURL, origin, adminCookie, 200)
	var users []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &users); err != nil || len(users) != 3 {
		t.Fatalf("incorrect candidate users: %s", response.Body.String())
	}
	candidateStatus := make(map[string]bool)
	for _, candidate := range users {
		id, hasID := candidate["id"].(string)
		active, hasStatus := candidate["active"].(bool)
		if len(candidate) != 3 || !hasID || !hasStatus || candidate["username"] == nil {
			t.Fatalf("unexpected candidate fields: %v", candidate)
		}
		candidateStatus[id] = active
	}
	if active, exists := candidateStatus["dormant"]; !candidateStatus[user.ID] || !exists || active {
		t.Fatalf("incorrect candidate activity: %s", response.Body.String())
	}

	listLeaders := func() map[string]*string {
		t.Helper()
		response := request("GET", groupsURL, origin, adminCookie, 200)
		var listing struct{ Groups []accessapp.Group }
		if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
			t.Fatal(err)
		}
		leaders := make(map[string]*string)
		for _, listed := range listing.Groups {
			leaders[listed.ID] = listed.LeaderID
		}
		return leaders
	}
	if leader, exists := listLeaders()[group.ID]; !exists || leader != nil {
		t.Fatal("legacy group should be listed without a leader")
	}
	legacyURL := groupsURL + "/" + group.ID
	request("PUT", legacyURL+"/members/admin", origin, adminCookie, 204)
	_, adminCookie = support.Create(t, sessions, seed.ID)
	request("PUT", legacyURL+"/leader", origin, adminCookie, 204, `{"leaderId":"admin"}`)
	request("PUT", legacyURL+"/members/"+user.ID, origin, adminCookie, 204)
	transferred := make(chan int, 1)
	removed := make(chan int, 1)
	start := make(chan struct{})
	go func() {
		<-start
		transferred <- performRequest("PUT", legacyURL+"/leader", origin, adminCookie, leaderBody).Code
	}()
	go func() {
		<-start
		removed <- performRequest("DELETE", legacyURL+"/members/"+user.ID, origin, adminCookie, "").Code
	}()
	close(start)
	transferStatus, removeStatus := <-transferred, <-removed
	expectedLeader := seed.ID
	switch {
	case transferStatus == 204 && removeStatus == 409:
		expectedLeader = user.ID
	case transferStatus == 400 && removeStatus == 204:
	default:
		t.Fatalf("transfer and remove returned %d and %d", transferStatus, removeStatus)
	}
	leaders := listLeaders()
	if leaders[group.ID] == nil || *leaders[group.ID] != expectedLeader || leaders[writers.ID] == nil || *leaders[writers.ID] != user.ID {
		t.Fatal("listed group leaders do not match the persisted changes")
	}

	if err := store.SetGroupMember(ctx, seed.GroupID, user.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE access_groups SET leader_user_id=$1 WHERE id=$2`, user.ID, seed.GroupID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetGroupMember(ctx, seed.GroupID, seed.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedAccount(ctx, seed); err != nil {
		t.Fatal(err)
	}
	assertLeader(seed.GroupID, sql.NullString{String: user.ID, Valid: true})
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
