package httpapi_test

import (
	"context"
	"hello-world/authn"
	"hello-world/authz"
	"hello-world/httpapi"
	"hello-world/session"
	"hello-world/session/test/support"
	"hello-world/users"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testUsers struct{ user users.User }

func (reader testUsers) FindByUsername(ctx context.Context, username string) (users.User, error) {
	if username != reader.user.Username {
		return users.User{}, users.ErrNotFound
	}
	return reader.user, nil
}

func TestIntegrationLoginAccessAndLogout(t *testing.T) {
	hash, err := authn.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hash.Encoded()
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := authn.NewAuthenticator(testUsers{user: users.User{ID: "u1", Username: "luis", Active: true, PasswordHash: encoded}})
	if err != nil {
		t.Fatal(err)
	}
	sessions, _, _ := support.NewService(t)
	authentication, err := httpapi.NewAuthenticationHandler(authenticator, sessions, session.Cookies{}, "https://app.example")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := httpapi.NewAuthorizationHandler(accessReader(func(ctx context.Context, id string) (authz.UserAccess, error) {
		return authz.UserAccess{UserID: id, Active: true, GroupIDs: []string{"g1"}, Grants: []authz.GroupGrant{{GroupID: "g1", DatabaseID: "db1", Level: authz.ViewOnly}}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	dataCalls := 0
	dataHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dataCalls++
		w.WriteHeader(http.StatusNoContent)
	})
	protectedData := authentication.RequireSession(authorization.RequirePermission(authz.ReadData, "databaseID", dataHandler))
	mux := http.NewServeMux()
	mux.Handle("/auth/", authentication.Routes())
	mux.Handle("GET /databases/{databaseID}/data", protectedData)
	routes := authentication.RequireSameOrigin(mux)
	request := func(method, path, body string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		incoming := newFormRequest(method, path, body)
		if cookie != nil {
			incoming.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, incoming)
		assertStatus(t, response, want)
		return response
	}
	request("GET", "/databases/db1/data", "", nil, 401)
	login := request("POST", "/auth/login", "username=luis&password=correct-password", nil, 204)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("login did not issue a cookie")
	}
	cookie := cookies[0]
	request("GET", "/databases/db1/data", "", cookie, 204)
	request("GET", "/databases/db2/data", "", cookie, 403)
	logout := request("POST", "/auth/logout", "", cookie, 204)
	cookies = logout.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	request("GET", "/databases/db1/data", "", cookie, 401)
	if dataCalls != 1 {
		t.Fatal("data handler ran outside the authorized request")
	}
}
