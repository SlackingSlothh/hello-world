package users

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("user not found")

type User struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
	Active          bool   `json:"active"`
	PasswordHash    string `json:"-"` // Encoded hash, never plaintext.
}

type Reader interface {
	FindByUsername(context.Context, string) (User, error)
}
