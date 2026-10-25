package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hello-world/session"
	"hello-world/session/test/support"
)

func TestLogout(t *testing.T) {
	cookie := support.Cookie(t, createIssuedSession(t))
	cases := []struct {
		name, body string
		cookie     *http.Cookie
		err        error
		status     int
		calls      int
	}{
		{name: "valid session", cookie: cookie, status: 204, calls: 1},
		{name: "missing cookie", status: 204, calls: 1},
		{name: "storage failure", cookie: cookie, err: privateError, status: 503, calls: 1},
		{name: "unexpected form", body: "username=luis", status: 400},
		{name: "invalid cookie", cookie: &http.Cookie{Name: session.CookieName, Value: "bad"}, status: 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler := newAuthenticationHandler(t, nil, sessionStub{logout: func(ctx context.Context, identifier session.Identifier) error {
				calls++
				if tc.cookie != nil && identifier != support.Identifier(t, tc.cookie) {
					t.Fatal("wrong logout identifier")
				}
				return tc.err
			}})
			request := newFormRequest("POST", "/auth/logout", tc.body)
			if tc.cookie != nil {
				request.AddCookie(tc.cookie)
			}
			response := httptest.NewRecorder()
			handler.Logout(response, request)
			assertStatus(t, response, tc.status)
			if calls != tc.calls {
				t.Fatalf("logout calls = %d, want %d", calls, tc.calls)
			}
			if strings.Contains(response.Body.String(), privateError.Error()) {
				t.Fatal("storage details exposed")
			}
			if tc.status == 204 {
				cookies := response.Result().Cookies()
				if len(cookies) != 1 || cookies[0].MaxAge != -1 || cookies[0].Value != "" {
					t.Fatal("logout did not clear cookie")
				}
			} else if response.Header().Get("Set-Cookie") != "" {
				t.Fatal("failed logout cleared cookie")
			}
		})
	}
}
