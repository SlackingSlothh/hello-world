package authn_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"hello-world/authn"
	"hello-world/users"
)

type userReader func(context.Context, string) (users.User, error)

func (reader userReader) FindByUsername(ctx context.Context, username string) (users.User, error) {
	return reader(ctx, username)
}

func TestAuthenticate(t *testing.T) {
	hash, err := authn.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hash.Encoded()
	if err != nil {
		t.Fatal(err)
	}
	good := users.User{ID: "u1", Username: "luis", Active: true, PasswordHash: encoded}
	inactive := good
	inactive.Active = false
	invalidHash := good
	invalidHash.PasswordHash = "plaintext"
	wrongUser := good
	wrongUser.Username = "other"
	missingID := good
	missingID.ID = ""
	cases := []struct {
		name, username, password string
		user                     users.User
		lookupErr, wantErr       error
	}{
		{"success", "luis", "correct-password", good, nil, nil},
		{"wrong password", "luis", "wrong", good, nil, authn.ErrInvalidCredentials},
		{"missing user", "luis", "correct-password", users.User{}, fmt.Errorf("wrapped: %w", users.ErrNotFound), authn.ErrInvalidCredentials},
		{"storage failure", "luis", "correct-password", users.User{}, errors.New("private database details"), authn.ErrUnavailable},
		{"empty username", "", "correct-password", good, nil, authn.ErrInvalidCredentials},
		{"empty password", "luis", "", good, nil, authn.ErrInvalidCredentials},
		{"inactive", "luis", "correct-password", inactive, nil, authn.ErrInvalidCredentials},
		{"malformed hash", "luis", "correct-password", invalidHash, nil, authn.ErrUnavailable},
		{"wrong user", "luis", "correct-password", wrongUser, nil, authn.ErrUnavailable},
		{"missing ID", "luis", "correct-password", missingID, nil, authn.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			authenticator, err := authn.NewAuthenticator(userReader(func(ctx context.Context, username string) (users.User, error) {
				calls++
				if username != tc.username {
					t.Fatal("username not forwarded")
				}
				return tc.user, tc.lookupErr
			}))
			if err != nil {
				t.Fatal(err)
			}
			identity, err := authenticator.Authenticate(context.Background(), tc.username, tc.password)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && identity.UserID != "u1" {
				t.Fatal("wrong identity")
			}
			if tc.wantErr != nil && identity.UserID != "" {
				t.Fatal("failure exposed identity")
			}
			if (tc.username == "" || tc.password == "") && calls != 0 {
				t.Fatal("empty input reached repository")
			}
		})
	}
}
