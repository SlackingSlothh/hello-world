package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hello-world/internal/config"
	"hello-world/internal/services/authn"
	"hello-world/internal/services/authz"
	"hello-world/internal/services/session/test/support"
	"hello-world/internal/services/users"
	"hello-world/internal/transport/http/server"
)

type userReader struct{ user users.User }

func (reader userReader) FindByUsername(ctx context.Context, username string) (users.User, error) {
	if username != reader.user.Username {
		return users.User{}, users.ErrNotFound
	}
	return reader.user, nil
}

type accessReader struct{}

func (accessReader) LoadAccess(ctx context.Context, id string) (authz.UserAccess, error) {
	return authz.UserAccess{UserID: id, Active: true, IsPlatformAdmin: true}, nil
}

func TestServerWithoutAdapters(t *testing.T) {
	app, err := server.New(config.Config{Address: ":8080"}, server.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.HTTP.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	if response.Code != http.StatusNotFound {
		t.Fatal("unexpected response for an unregistered route")
	}
	for _, path := range []string{"/auth/login", "/auth/logout"} {
		response := httptest.NewRecorder()
		app.HTTP.Handler.ServeHTTP(response, httptest.NewRequest("POST", path, nil))
		if response.Code != 503 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing adapters did not fail closed")
		}
	}
	if err := app.HandleProtected("GET /private", authz.ManageUsers, "", http.NotFoundHandler()); err == nil {
		t.Fatal("protected route accepted without adapters")
	}
	if app.Authentication != nil || app.Authorization != nil {
		t.Fatal("auth unexpectedly enabled")
	}
}

func TestServerRejectsIncompleteDependenciesAndConfiguration(t *testing.T) {
	serverConfig := config.Config{Address: ":8080", PublicOrigin: "https://app.example", SessionEncryptionKey: make([]byte, 32)}
	cases := []struct {
		name         string
		dependencies server.Dependencies
	}{
		{"onlyUsers", server.Dependencies{Users: userReader{}}},
		{"missingSessions", server.Dependencies{Users: userReader{}, Access: accessReader{}}},
		{"missingAccess", server.Dependencies{Users: userReader{}, Sessions: support.NewStore()}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := server.New(serverConfig, testCase.dependencies); err == nil {
				t.Fatal("incomplete dependencies accepted")
			}
		})
	}
	dependencies := server.Dependencies{Users: userReader{}, Sessions: support.NewStore(), Access: accessReader{}}
	for name, change := range map[string]func(*config.Config){
		"missing key":                func(c *config.Config) { c.SessionEncryptionKey = nil },
		"missing origin":             func(c *config.Config) { c.PublicOrigin = "" },
		"HTTP without insecure flag": func(c *config.Config) { c.PublicOrigin = "http://localhost" },
		"HTTPS with insecure flag":   func(c *config.Config) { c.AllowInsecureLocalHTTP = true },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := serverConfig
			change(&invalid)
			if _, err := server.New(invalid, dependencies); err == nil {
				t.Fatal("invalid auth configuration accepted")
			}
		})
	}
}

func TestIntegrationServerRoutesAndMiddlewareOrder(t *testing.T) {
	hash, err := authn.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hash.Encoded()
	if err != nil {
		t.Fatal(err)
	}
	app, err := server.New(config.Config{Address: ":8080", PublicOrigin: "https://app.example", SessionEncryptionKey: make([]byte, 32)}, server.Dependencies{
		Users: userReader{user: users.User{ID: "u1", Username: "luis", Active: true, PasswordHash: encoded}}, Sessions: support.NewStore(), Access: accessReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := app.HandleProtected("POST /users", authz.ManageUsers, "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) })); err != nil {
		t.Fatal(err)
	}
	if err := app.HandleProtected("GET /databases/{databaseID}/data", authz.ReadData, "databaseID", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("admin accessed database without grant") })); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, origin string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		app.HTTP.Handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s status=%d, want=%d", method, path, response.Code, want)
		}
		return response
	}
	call("POST", "/users", "", "", nil, 403)
	call("POST", "/users", "", "https://app.example", nil, 401)
	login := call("POST", "/auth/login", "username=luis&password=correct-password", "https://app.example", nil, 204)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	cookie := cookies[0]
	call("POST", "/users", "", "https://evil.example", cookie, 403)
	call("POST", "/users", "", "https://app.example", cookie, 204)
	call("GET", "/databases/db1/data", "", "", cookie, 403)
	call("POST", "/auth/logout", "", "https://app.example", cookie, 204)
	call("POST", "/users", "", "https://app.example", cookie, 401)
	if calls != 1 {
		t.Fatal("handler executed outside authorized request")
	}
	if app.HTTP.Addr != ":8080" || app.HTTP.ReadHeaderTimeout <= 0 || app.HTTP.ReadTimeout <= 0 || app.HTTP.IdleTimeout <= 0 {
		t.Fatal("server config not applied")
	}
}
