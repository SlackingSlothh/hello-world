package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hello-world/httpapi"
	"hello-world/session"
	"hello-world/session/test/support"
)

func TestRequireSession(t *testing.T) {
	issued := createIssuedSession(t)
	cookie := support.Cookie(t, issued)
	cases := []struct {
		name   string
		cookie string
		result session.Validated
		err    error
		status int
		calls  int
	}{
		{name: "valid", cookie: cookie.Value, result: session.Validated{UserID: "u1"}, status: 204, calls: 1},
		{name: "replacement", cookie: cookie.Value, result: session.Validated{UserID: "u1", Replacement: &issued}, status: 204, calls: 1},
		{name: "missing cookie", status: 401},
		{name: "malformed cookie", cookie: "bad", status: 401},
		{name: "expired", cookie: cookie.Value, err: fmt.Errorf("wrapped: %w", session.ErrInvalid), status: 401, calls: 1},
		{name: "unavailable", cookie: cookie.Value, err: privateError, status: 503, calls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			nextCalled := false
			handler := newAuthenticationHandler(t, nil, sessionStub{validate: func(context.Context, session.Identifier) (session.Validated, error) {
				calls++
				return tc.result, tc.err
			}})
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				identity, ok := httpapi.IdentityFromContext(r.Context())
				if !ok || identity.UserID != "u1" {
					t.Fatal("identity not available to next handler")
				}
				if tc.result.Replacement != nil && w.Header().Get("Set-Cookie") == "" {
					t.Fatal("replacement cookie written too late")
				}
				w.WriteHeader(204)
			})
			request := httptest.NewRequest("GET", "/data?page=2", nil)
			if tc.cookie != "" {
				request.AddCookie(&http.Cookie{Name: session.CookieName, Value: tc.cookie})
			}
			response := httptest.NewRecorder()
			handler.RequireSession(next).ServeHTTP(response, request)
			assertStatus(t, response, tc.status)
			if calls != tc.calls || nextCalled != (tc.status == 204) {
				t.Fatal("incorrect downstream invocation")
			}
			if strings.Contains(response.Body.String(), privateError.Error()) {
				t.Fatal("error details exposed")
			}
			if tc.status != 204 && response.Header().Get("Set-Cookie") != "" {
				t.Fatal("rejected request changed cookie")
			}
			if _, ok := httpapi.IdentityFromContext(request.Context()); ok {
				t.Fatal("original request context modified")
			}
		})
	}
}
