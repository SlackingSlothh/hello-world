package handler_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	accessapp "hello-world/app/access"
	catalogapp "hello-world/app/catalog"
	connectionapp "hello-world/app/connection"
	"hello-world/authn"
	"hello-world/handler"
	"hello-world/httpapi"
	"hello-world/manageddb"
	"hello-world/session"
	"hello-world/session/test/support"
)

type connectionStore struct {
	saved   manageddb.Config
	failure error
}

func (s *connectionStore) Save(_ context.Context, config manageddb.Config) error {
	if s.failure != nil {
		return s.failure
	}
	s.saved = config
	return nil
}
func (s *connectionStore) LoadAll(context.Context) ([]manageddb.Config, error) {
	return []manageddb.Config{s.saved}, nil
}
func (s *connectionStore) Delete(context.Context, string) error { return nil }

type pingConnection struct{}

func (pingConnection) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (pingConnection) Begin() (driver.Tx, error)           { return nil, errors.New("not used") }
func (pingConnection) Close() error                        { return nil }
func (pingConnection) Ping(context.Context) error          { return nil }

type pingDriver struct{}

func (pingDriver) Open(string) (driver.Conn, error) { return pingConnection{}, nil }

type pingConnector struct{}

func (pingConnector) Connect(context.Context) (driver.Conn, error) { return pingConnection{}, nil }
func (pingConnector) Driver() driver.Driver                        { return pingDriver{} }

func TestAdminEditsConnection(t *testing.T) {
	registry := manageddb.NewRegistryWithOpener(func(_ string, dsn string) (*sql.DB, error) {
		if dsn == "bad-secret" {
			return nil, errors.New("cannot open bad-secret")
		}
		return sql.OpenDB(pingConnector{}), nil
	})
	defer registry.Close()
	initial := manageddb.Config{ID: "db1", Name: "Original", Driver: "postgres", DSN: "old-secret", Pool: manageddb.PoolConfig{MaxOpen: 5, MaxIdle: 2, MaxLifetimeSeconds: 60}}
	if _, err := registry.Restore(initial); err != nil {
		t.Fatal(err)
	}
	store := &connectionStore{saved: initial}
	service := connectionapp.Service{Registry: registry, Store: store}
	access := &accessReader{admin: true}
	authenticator, err := authn.NewAuthenticator(&userStore{})
	if err != nil {
		t.Fatal(err)
	}
	sessions, _, _ := support.NewService(t)
	authentication, err := httpapi.NewAuthenticationHandler(authenticator, sessions, session.Cookies{}, "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := httpapi.NewAuthorizationHandler(access)
	if err != nil {
		t.Fatal(err)
	}
	api := authentication.RequireSameOrigin(handler.New(service, catalogapp.Service{}, accessapp.Service{}, handler.NewSecurity(authentication, authorization), nil))
	_, cookie := support.Create(t, sessions, "admin")
	request := func(method, path, body, origin string, authenticated bool, status int) {
		t.Helper()
		req := httptest.NewRequest(method, "https://example.com"+path, strings.NewReader(body))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("status %d want %d: %s", response.Code, status, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "secret") {
			t.Fatal("response exposed DSN")
		}
	}
	path := "/api/v1/connections/db1"
	request("GET", path+"/settings", "", "https://example.com", true, 200)
	request("PATCH", path, `{"name":"Renamed","dsn":""}`, "https://example.com", true, 200)
	if store.saved.Name != "Renamed" || store.saved.DSN != "old-secret" || store.saved.Pool.MaxOpen != 5 {
		t.Fatal("rename changed omitted settings")
	}
	if err := registry.Disconnect("db1"); err != nil {
		t.Fatal(err)
	}
	request("PATCH", path, `{"name":"Offline","pool":{"maxOpen":8,"maxIdle":3,"maxLifetimeSeconds":60}}`, "https://example.com", true, 200)
	view, _ := registry.Get("db1")
	if view.Connected || view.Name != "Offline" || view.Pool.MaxOpen != 8 || view.Pool.MaxLifetimeSeconds != 60 {
		t.Fatal("metadata update changed connection state or lost settings")
	}
	for _, body := range []string{`{}`, `{"name":" "}`, `{"pool":{"maxOpen":2,"maxIdle":3}}`, `{"driver":"mysql"}`} {
		request("PATCH", path, body, "https://example.com", true, 400)
	}
	request("PATCH", path, `{"name":"Failed","dsn":"bad-secret"}`, "https://example.com", true, 502)
	if store.saved.Name != "Offline" || store.saved.DSN != "old-secret" {
		t.Fatal("failed verification changed saved settings")
	}
	store.failure = errors.New("store unavailable")
	request("PATCH", path, `{"name":"Unsaved","dsn":"new-secret"}`, "https://example.com", true, 503)
	view, _ = registry.Get("db1")
	if view.Name != "Offline" || view.Connected {
		t.Fatal("failed persistence changed runtime settings")
	}
	store.failure = nil
	request("PATCH", path, `{"name":"Replaced","dsn":"new-secret"}`, "https://example.com", true, 200)
	view, _ = registry.Get("db1")
	if !view.Connected || view.ID != "db1" || store.saved.DSN != "new-secret" || store.saved.Pool.MaxOpen != 8 {
		t.Fatal("replacement lost ID, DSN or omitted pool")
	}
	restored := manageddb.NewRegistryWithOpener(func(string, string) (*sql.DB, error) { return sql.OpenDB(pingConnector{}), nil })
	defer restored.Close()
	reloaded := connectionapp.Service{Registry: restored, Store: store}
	if err := reloaded.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, _ = reloaded.Get("db1")
	if view.Name != "Replaced" || view.Pool.MaxOpen != 8 {
		t.Fatal("saved changes did not survive restore")
	}
	request("PATCH", "/api/v1/connections/missing", `{"name":"Missing"}`, "https://example.com", true, 404)
	request("PATCH", path, `{"name":"No session"}`, "https://example.com", false, 401)
	request("PATCH", path, `{"name":"Wrong origin"}`, "https://other.example", true, 403)
	access.admin = false
	request("GET", path+"/settings", "", "https://example.com", true, 403)
	request("PATCH", path, `{"name":"Not admin"}`, "https://example.com", true, 403)
}
