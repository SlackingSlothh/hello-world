package handler_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"hello-world/internal/database/target/manageddb"
	accessapp "hello-world/internal/services/access"
	"hello-world/internal/services/authn"
	catalogapp "hello-world/internal/services/catalog"
	connectionapp "hello-world/internal/services/connection"
	"hello-world/internal/services/session"
	"hello-world/internal/services/session/test/support"
	"hello-world/internal/transport/http/handler"
	"hello-world/internal/transport/http/httpapi"
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

func TestAdminUpdatesOnlyConnectionPool(t *testing.T) {
	registry := manageddb.NewRegistryWithOpener(func(_ string, dsn string) (*sql.DB, error) {
		if dsn != "old-secret" {
			return nil, errors.New("unexpected DSN")
		}
		return sql.OpenDB(pingConnector{}), nil
	})
	defer registry.Close()
	initial := manageddb.Config{
		ID: "db1", Name: "Original", Driver: "postgres", DSN: "old-secret",
		Pool: manageddb.PoolConfig{
			MaxOpen: 5, MaxIdle: 2, MaxLifetimeSeconds: 60, MaxIdleTimeSeconds: 30,
		},
	}
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
	request := func(method, path, body, origin string, authenticated bool, status int) *httptest.ResponseRecorder {
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
		return response
	}
	adminConnections := request("GET", "/api/v1/admin/connections", "", "https://example.com", true, 200)
	if !strings.Contains(adminConnections.Body.String(), `"id":"db1"`) {
		t.Fatal("admin connection list omitted an ungranted connection")
	}
	userConnections := request("GET", "/api/v1/connections", "", "https://example.com", true, 200)
	if userConnections.Body.String() != "[]\n" {
		t.Fatalf("user connection list exposed an ungranted connection: %s", userConnections.Body.String())
	}
	path := "/api/v1/connections/db1"
	request("GET", path+"/settings", "", "https://example.com", true, 200)
	poolBody := `{"pool":{"maxOpen":8,"maxIdle":3,"maxLifetimeSeconds":120,"maxIdleTimeSeconds":15}}`
	request("PATCH", path, poolBody, "https://example.com", true, 200)
	view, err := registry.Get("db1")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Connected || view.Name != "Original" || view.Pool.MaxOpen != 8 ||
		view.Pool.MaxIdle != 3 || view.Pool.MaxLifetimeSeconds != 120 ||
		view.Pool.MaxIdleTimeSeconds != 15 {
		t.Fatalf("pool update changed identity or lost pool settings: %+v", view)
	}
	if store.saved.Name != "Original" || store.saved.Driver != "postgres" ||
		store.saved.DSN != "old-secret" || store.saved.Pool.MaxOpen != 8 {
		t.Fatalf("pool update changed stored connection identity: %+v", store.saved)
	}
	if err := registry.Disconnect("db1"); err != nil {
		t.Fatal(err)
	}
	request("PATCH", path, `{"pool":{"maxOpen":6,"maxIdle":2,"maxLifetimeSeconds":90,"maxIdleTimeSeconds":20}}`, "https://example.com", true, 200)
	view, err = registry.Get("db1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Connected || view.Name != "Original" || view.Pool.MaxOpen != 6 ||
		view.Pool.MaxIdle != 2 || view.Pool.MaxLifetimeSeconds != 90 ||
		view.Pool.MaxIdleTimeSeconds != 20 {
		t.Fatalf("pool update changed disconnected connection state: %+v", view)
	}
	invalidUpdates := []string{
		`{}`,
		`{"pool":{}}`,
		`{"pool":{"maxOpen":-1,"maxIdle":0,"maxLifetimeSeconds":0,"maxIdleTimeSeconds":0}}`,
		`{"pool":{"maxOpen":2,"maxIdle":3,"maxLifetimeSeconds":0,"maxIdleTimeSeconds":0}}`,
		`{"pool":{"maxOpen":4,"maxIdle":2,"maxLifetimeSeconds":60,"maxIdleTimeSeconds":10},"name":"Changed"}`,
		`{"pool":{"maxOpen":4,"maxIdle":2,"maxLifetimeSeconds":60,"maxIdleTimeSeconds":10},"driver":"mysql"}`,
		`{"pool":{"maxOpen":4,"maxIdle":2,"maxLifetimeSeconds":60,"maxIdleTimeSeconds":10},"dsn":"new-secret"}`,
	}
	for _, body := range invalidUpdates {
		request("PATCH", path, body, "https://example.com", true, 400)
	}
	request("PATCH", path, `{"pool":{"maxOpen":4}}`, "https://example.com", true, 400)
	request("PATCH", "/api/v1/connections/missing", poolBody, "https://example.com", true, 404)

	store.failure = errors.New("store unavailable")
	request("PATCH", path, poolBody, "https://example.com", true, 503)
	view, err = registry.Get("db1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Name != "Original" || view.Connected || view.Pool.MaxOpen != 6 ||
		store.saved.Pool.MaxOpen != 6 {
		t.Fatal("failed persistence changed runtime or stored settings")
	}
	store.failure = nil

	restored := manageddb.NewRegistryWithOpener(func(string, string) (*sql.DB, error) { return sql.OpenDB(pingConnector{}), nil })
	defer restored.Close()
	reloaded := connectionapp.Service{Registry: restored, Store: store}
	if err := reloaded.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err = reloaded.Get("db1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Name != "Original" || view.Pool.MaxOpen != 6 ||
		view.Pool.MaxLifetimeSeconds != 90 || view.Pool.MaxIdleTimeSeconds != 20 {
		t.Fatal("saved changes did not survive restore")
	}
	request("PATCH", path, poolBody, "https://example.com", false, 401)
	request("PATCH", path, poolBody, "https://other.example", true, 403)
	access.admin = false
	request("GET", "/api/v1/admin/connections", "", "https://example.com", true, 403)
	request("GET", path+"/settings", "", "https://example.com", true, 403)
	request("PATCH", path, poolBody, "https://example.com", true, 403)
}
