package users

import (
	"context"
	"errors"

	"hello-world/authn"
)

var ErrNotFound = errors.New("user not found")

type User struct {
	ID              string             `json:"id"`
	Username        string             `json:"username"`
	IsPlatformAdmin bool               `json:"is_platform_admin"`
	Active          bool               `json:"active"`
	PasswordHash    authn.PasswordHash `json:"-"`
}


type Reader interface {
	FindByUsername(context.Context, string) (User, error)
}
