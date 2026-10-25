package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hello-world/authn"
	"hello-world/session"
	"hello-world/session/test/support"
)

func TestLoginRejectsInvalidRequestsBeforeAuthentication(t *testing.T) {
	cases := []struct {
		name, method, path, body, contentType string
		modify                                func(*http.Request)
		status                                int
	}{
		{name: "wrongMethod", method: "GET", status: 405},
		{name: "queryParameters", path: "/auth/login?password=secret", status: 400},
		{name: "wrongOrigin", modify: func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, status: 403},
		{name: "jsonBody", contentType: "application/json", status: 415},
		{name: "missingPassword", body: "username=luis", status: 400},
		{name: "emptyPassword", body: "username=luis&password=", status: 400},
		{name: "duplicateUsername", body: "username=luis&username=other&password=secret", status: 400},
		{name: "oversizedBody", body: "username=luis&password=" + strings.Repeat("x", 8192), status: 413},
	}
	handler := newAuthenticationHandler(t, nil, sessionStub{})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method, path, body := tc.method, tc.path, tc.body
			if method == "" {
				method = "POST"
			}
			if path == "" {
				path = "/auth/login"
			}
			if body == "" {
				body = "username=luis&password=secret"
			}
			request := newFormRequest(method, path, body)
			if tc.contentType != "" {
				request.Header.Set("Content-Type", tc.contentType)
			}
			if tc.modify != nil {
				tc.modify(request)
			}
			response := httptest.NewRecorder()
			handler.Login(response, request)
			assertStatus(t, response, tc.status)
			if response.Header().Get("Set-Cookie") != "" {
				t.Fatal("rejected login wrote cookie")
			}
			if tc.status == 405 && response.Header().Get("Allow") != "POST" {
				t.Fatal("missing Allow")
			}
		})
	}
}

func TestLoginOutcomes(t *testing.T) {
	issued := createIssuedSession(t)
	cases := []struct {
		name              string
		identity          authn.Identity
		authErr, startErr error
		issued            session.Issued
		status            int
	}{
		{name: "success", identity: authn.Identity{UserID: "u1"}, issued: issued, status: 204},
		{name: "invalid credentials", authErr: authn.ErrInvalidCredentials, status: 401},
		{name: "authentication unavailable", authErr: privateError, status: 503},
		{name: "session unavailable", identity: authn.Identity{UserID: "u1"}, startErr: privateError, status: 503},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			startCalls := 0
			previousCookie := support.Cookie(t, issued)
			previous := support.Identifier(t, previousCookie)
			handler := newAuthenticationHandler(t, authenticatorStub(func(ctx context.Context, username, password string) (authn.Identity, error) {
				if username != "luis" || password != "a+b & c" {
					t.Fatal("form values not decoded correctly")
				}
				return tc.identity, tc.authErr
			}), sessionStub{start: func(ctx context.Context, id string, identifier session.Identifier) (session.Issued, error) {
				startCalls++
				if id != "u1" || identifier != previous {
					t.Fatal("login did not forward identity or previous session")
				}
				return tc.issued, tc.startErr
			}})
			request := newFormRequest("POST", "/auth/login", "username=luis&password=a%2Bb+%26+c")
			request.AddCookie(previousCookie)
			response := httptest.NewRecorder()
			handler.Login(response, request)
			assertStatus(t, response, tc.status)
			if strings.Contains(response.Body.String(), privateError.Error()) || strings.Contains(response.Body.String(), previousCookie.Value) {
				t.Fatal("sensitive data in error response")
			}
			if (tc.authErr != nil || tc.identity.UserID == "") && startCalls != 0 {
				t.Fatal("failed authentication started session")
			}
			if tc.status == 204 {
				cookies := response.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Value != support.Cookie(t, issued).Value || response.Body.Len() != 0 {
					t.Fatal("successful login response incorrect")
				}
			} else if response.Header().Get("Set-Cookie") != "" {
				t.Fatal("failed login set cookie")
			}
		})
	}
}
