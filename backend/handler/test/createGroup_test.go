package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessapp "hello-world/app/access"
	catalogapp "hello-world/app/catalog"
	connectionapp "hello-world/app/connection"
	"hello-world/authn"
	"hello-world/handler"
	"hello-world/httpapi"
	"hello-world/session"
	"hello-world/session/test/support"
)

type groupStore struct {
	group   accessapp.Group
	failure error
	calls   int
}

func (s *groupStore) CreateGroup(_ context.Context, group accessapp.Group) error {
	s.calls++
	if s.failure != nil {
		return s.failure
	}
	if s.group.Name == group.Name {
		return accessapp.ErrGroupNameTaken
	}
	s.group = group
	return nil
}

func TestAdminAddsGroup(t *testing.T) {
	store := &groupStore{}
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
	api := handler.New(connectionapp.Service{}, catalogapp.Service{}, accessapp.Service{Groups: store}, handler.NewSecurity(authentication, authorization), nil)
	securedAPI := authentication.RequireSameOrigin(api)
	_, cookie := support.Create(t, sessions, "admin")
	for _, tc := range []struct {
		name          string
		body          string
		admin         bool
		authenticated bool
		origin        string
		failure       error
		status        int
	}{
		{name: "admin creates group", body: `{"name":"  Data analysts  "}`, admin: true, authenticated: true, origin: "https://example.com", status: 201},
		{name: "duplicate name", body: `{"name":"Data analysts"}`, admin: true, authenticated: true, origin: "https://example.com", status: 409},
		{name: "empty name", body: `{"name":"   "}`, admin: true, authenticated: true, origin: "https://example.com", status: 400},
		{name: "long name", body: `{"name":"` + strings.Repeat("a", 101) + `"}`, admin: true, authenticated: true, origin: "https://example.com", status: 400},
		{name: "invalid JSON", body: `{`, admin: true, authenticated: true, origin: "https://example.com", status: 400},
		{name: "cannot create admin group", body: `{"name":"Admins","isAdmin":true}`, admin: true, authenticated: true, origin: "https://example.com", status: 400},
		{name: "ordinary user denied", body: `{"name":"Guests"}`, authenticated: true, origin: "https://example.com", status: 403},
		{name: "missing session", body: `{"name":"Guests"}`, admin: true, origin: "https://example.com", status: 401},
		{name: "cross origin denied", body: `{"name":"Guests"}`, admin: true, authenticated: true, origin: "https://other.example", status: 403},
		{name: "storage unavailable", body: `{"name":"Guests"}`, admin: true, authenticated: true, origin: "https://example.com", failure: errors.New("storage failure"), status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access.admin = tc.admin
			store.failure = tc.failure
			calls := store.calls
			request := httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/access/groups", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
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
			if tc.status == 201 {
				var group accessapp.Group
				if err := json.Unmarshal(response.Body.Bytes(), &group); err != nil {
					t.Fatal(err)
				}
				if group.ID == "" || group.ID != store.group.ID || group.Name != "Data analysts" || group.IsAdmin || group.Grants == nil || len(group.Grants) != 0 {
					t.Fatal("incorrect new group defaults")
				}
			}
		})
	}
}
