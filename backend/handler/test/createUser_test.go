package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	accessapp "hello-world/app/access"
	catalogapp "hello-world/app/catalog"
	connectionapp "hello-world/app/connection"
	"hello-world/authn"
	"hello-world/authz"
	"hello-world/handler"
	"hello-world/httpapi"
	"hello-world/session"
	"hello-world/session/test/support"
	"hello-world/users"
)

type userStore struct {
	user    users.User
	failure error
	calls   int
}

func (s *userStore) CreateUser(_ context.Context, user accessapp.User, hash string) error {
	s.calls++
	if s.failure != nil {
		return s.failure
	}
	if s.user.Username == user.Username {
		return accessapp.ErrUsernameTaken
	}
	s.user = users.User{ID: user.ID, Username: user.Username, Active: user.Active, PasswordHash: hash}
	return nil
}

func (s *userStore) FindByUsername(_ context.Context, username string) (users.User, error) {
	if s.user.Username != username {
		return users.User{}, users.ErrNotFound
	}
	return s.user, nil
}

type accessReader struct{ admin bool }

func (a *accessReader) LoadAccess(_ context.Context, id string) (authz.UserAccess, error) {
	return authz.UserAccess{UserID: id, Active: true, IsPlatformAdmin: a.admin}, nil
}

func TestAdminAddsUser(t *testing.T) {
	store := &userStore{}
	access := &accessReader{admin: true}
	authenticator, err := authn.NewAuthenticator(store)
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
	api := handler.New(connectionapp.Service{}, catalogapp.Service{}, accessapp.Service{Users: store}, handler.NewSecurity(authentication, authorization), nil)
	securedAPI := authentication.RequireSameOrigin(api)
	_, cookie := support.Create(t, sessions, "admin")
	form := url.Values{"username": {"alice"}, "password": {"a test password"}}.Encode()
	for _, tc := range []struct {
		name          string
		body          string
		admin         bool
		authenticated bool
		origin        string
		query         string
		failure       error
		status        int
	}{
		{name: "admin creates user", body: form, admin: true, authenticated: true, origin: "https://example.com", status: 201},
		{name: "duplicate username", body: form, admin: true, authenticated: true, origin: "https://example.com", status: 409},
		{name: "empty password", body: "username=bob&password=", admin: true, authenticated: true, origin: "https://example.com", status: 400},
		{name: "username with spaces", body: "username=bob+smith&password=test", admin: true, authenticated: true, origin: "https://example.com", status: 400},
		{name: "cannot assign admin through form", body: form + "&isAdmin=true", admin: true, authenticated: true, origin: "https://example.com", status: 400},
		{name: "credentials in query rejected", body: form, query: "?username=alice", admin: true, authenticated: true, origin: "https://example.com", status: 400},
		{name: "ordinary user denied", body: form, authenticated: true, origin: "https://example.com", status: 403},
		{name: "missing session", body: form, admin: true, origin: "https://example.com", status: 401},
		{name: "cross origin denied", body: form, admin: true, authenticated: true, origin: "https://other.example", status: 403},
		{name: "storage unavailable", body: form, admin: true, authenticated: true, origin: "https://example.com", failure: errors.New("storage failure"), status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access.admin = tc.admin
			store.failure = tc.failure
			calls := store.calls
			request := httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/access/users"+tc.query, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", tc.origin)
			if tc.authenticated {
				request.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			securedAPI.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if tc.status == 400 || tc.status == 401 || tc.status == 403 {
				if store.calls != calls {
					t.Fatal("rejected request reached storage")
				}
			}
			if strings.Contains(response.Body.String(), "a test password") || strings.Contains(response.Body.String(), "$argon2id$") {
				t.Fatal("response contains credentials")
			}
		})
	}
	store.failure = nil
	hash, err := authn.ParsePasswordHash(store.user.PasswordHash)
	if err != nil || !hash.Matches("a test password") {
		t.Fatal("stored password cannot be verified")
	}
	if !store.user.Active || store.user.IsPlatformAdmin || store.user.ID == "" {
		t.Fatal("incorrect new user defaults")
	}
	identity, err := authenticator.Authenticate(context.Background(), "alice", "a test password")
	if err != nil || identity.UserID != store.user.ID {
		t.Fatalf("created user cannot authenticate: %v", err)
	}
}
