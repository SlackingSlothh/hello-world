package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hello-world/authz"
	"hello-world/httpapi"
	"hello-world/session"
	"hello-world/session/test/support"
)

func TestRequirePermission(t *testing.T) {
	good := authz.UserAccess{UserID: "u1", Active: true, GroupIDs: []string{"g1"}, Grants: []authz.GroupGrant{{GroupID: "g1", DatabaseID: "db1", Level: authz.ViewOnly}}}
	inactive := good
	inactive.Active = false
	wrongUser := good
	wrongUser.UserID = "u2"
	admin := authz.UserAccess{UserID: "u1", Active: true, IsPlatformAdmin: true}
	cases := []struct {
		name        string
		action      authz.Action
		param, path string
		access      authz.UserAccess
		err         error
		noIdentity  bool
		status      int
		loads       int
	}{
		{name: "allowed read", action: authz.ReadData, param: "databaseID", path: "db1", access: good, status: 204, loads: 1},
		{name: "denied write", action: authz.ExecuteMigration, param: "databaseID", path: "db1", access: good, status: 403, loads: 1},
		{name: "different database", action: authz.ReadData, param: "databaseID", path: "db2", access: good, status: 403, loads: 1},
		{name: "inactive", action: authz.ReadData, param: "databaseID", path: "db1", access: inactive, status: 403, loads: 1},
		{name: "wrong identity returned", action: authz.ReadData, param: "databaseID", path: "db1", access: wrongUser, status: 503, loads: 1},
		{name: "repository failure", action: authz.ReadData, param: "databaseID", path: "db1", err: privateError, status: 503, loads: 1},
		{name: "no identity", action: authz.ReadData, param: "databaseID", path: "db1", noIdentity: true, status: 401},
		{name: "missing path parameter", action: authz.ReadData, param: "missing", path: "db1", status: 400},
		{name: "admin management", action: authz.ManageUsers, path: "db1", access: admin, status: 204, loads: 1},
		{name: "admin without database grant", action: authz.ReadData, param: "databaseID", path: "db1", access: admin, status: 403, loads: 1},
		{name: "unknown action", action: "unknown", path: "db1", access: admin, status: 403, loads: 1},
	}
	cookie := support.Cookie(t, createIssuedSession(t))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loads := 0
			called := false
			permission, err := httpapi.NewAuthorizationHandler(accessReader(func(ctx context.Context, id string) (authz.UserAccess, error) {
				loads++
				if id != "u1" {
					t.Fatal("trusted identity not used")
				}
				return tc.access, tc.err
			}))
			if err != nil {
				t.Fatal(err)
			}
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) })
			protected := permission.RequirePermission(tc.action, tc.param, next)
			if !tc.noIdentity {
				authentication := newAuthenticationHandler(t, nil, sessionStub{validate: func(context.Context, session.Identifier) (session.Validated, error) {
					return session.Validated{UserID: "u1"}, nil
				}})
				protected = authentication.RequireSession(protected)
			}
			mux := http.NewServeMux()
			mux.Handle("GET /databases/{databaseID}", protected)
			request := httptest.NewRequest("GET", "/databases/"+tc.path+"?userID=admin&role=admin", nil)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			assertStatus(t, response, tc.status)
			if loads != tc.loads || called != (tc.status == 204) {
				t.Fatal("incorrect access lookup or handler invocation")
			}
			if strings.Contains(response.Body.String(), privateError.Error()) {
				t.Fatal("private repository error exposed")
			}
		})
	}
	if _, err := httpapi.NewAuthorizationHandler(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
}
