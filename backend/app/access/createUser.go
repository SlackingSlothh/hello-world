package access

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"hello-world/authn"
)

var ErrInvalidUser = errors.New("invalid user credentials")
var ErrUsernameTaken = errors.New("username already exists")

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Active   bool   `json:"active"`
}

type UserStore interface {
	CreateUser(context.Context, User, string) error
}

func (s Service) CreateUser(ctx context.Context, username, password string) (User, error) {
	if username == "" || len(username) > 100 || !utf8.ValidString(username) || strings.ContainsFunc(username, unicode.IsSpace) || strings.ContainsFunc(username, unicode.IsControl) || password == "" || len(password) > 1024 {
		return User{}, ErrInvalidUser
	}
	if s.Users == nil {
		return User{}, errors.New("user store is unavailable")
	}
	hash, err := authn.HashPassword(password)
	if err != nil {
		return User{}, err
	}
	encoded, err := hash.Encoded()
	if err != nil {
		return User{}, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return User{}, err
	}
	user := User{ID: hex.EncodeToString(id[:]), Username: username, Active: true}
	if err := s.Users.CreateUser(ctx, user, encoded); err != nil {
		return User{}, err
	}
	return user, nil
}
