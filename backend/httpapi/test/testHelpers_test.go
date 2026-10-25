package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hello-world/authn"
	"hello-world/authz"
	"hello-world/httpapi"
	"hello-world/session"
	"hello-world/session/test/support"
)

type authenticatorStub func(context.Context, string, string) (authn.Identity, error)

func (stub authenticatorStub) Authenticate(ctx context.Context, username, password string) (authn.Identity, error) {
	return stub(ctx, username, password)
}

type sessionStub struct {
	start    func(context.Context, string, session.Identifier) (session.Issued, error)
	logout   func(context.Context, session.Identifier) error
	validate func(context.Context, session.Identifier) (session.Validated, error)
}

func (stub sessionStub) StartAfterLogin(ctx context.Context, id string, previous session.Identifier) (session.Issued, error) {
	if stub.start == nil {
		panic("unexpected StartAfterLogin")
	}
	return stub.start(ctx, id, previous)
}
func (stub sessionStub) Logout(ctx context.Context, id session.Identifier) error {
	if stub.logout == nil {
		panic("unexpected Logout")
	}
	return stub.logout(ctx, id)
}
func (stub sessionStub) Validate(ctx context.Context, id session.Identifier) (session.Validated, error) {
	if stub.validate == nil {
		panic("unexpected Validate")
	}
	return stub.validate(ctx, id)
}

type accessReader func(context.Context, string) (authz.UserAccess, error)

func (reader accessReader) LoadAccess(ctx context.Context, id string) (authz.UserAccess, error) {
	return reader(ctx, id)
}

func newAuthenticationHandler(t *testing.T, auth httpapi.Authenticator, sessions httpapi.Sessions) *httpapi.AuthenticationHandler {
	t.Helper()
	if auth == nil {
		auth = authenticatorStub(func(context.Context, string, string) (authn.Identity, error) { panic("unexpected Authenticate") })
	}
	handler, err := httpapi.NewAuthenticationHandler(auth, sessions, session.Cookies{}, "https://app.example")
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func newFormRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(body))
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func assertStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, want, response.Body.String())
	}
}

func createIssuedSession(t *testing.T) session.Issued {
	t.Helper()
	service, _, _ := support.NewService(t)
	issued, err := service.Create(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

var privateError = errors.New("private database connection details")
