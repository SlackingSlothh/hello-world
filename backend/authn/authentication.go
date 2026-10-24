package authn

import (
	"context"
	"crypto/rand"
	"errors"

	"hello-world/users"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUnavailable        = errors.New("authentication unavailable")
)

type Identity struct {
	UserID string
}

type Authenticator struct {
	reader    users.Reader
	dummyHash PasswordHash
}

func NewAuthenticator(reader users.Reader) (*Authenticator, error) {
	if reader == nil {
		return nil, errors.New("authentication requires a user reader")
	}
	dummyHash, err := HashPassword(rand.Text())
	if err != nil {
		return nil, ErrUnavailable
	}
	return &Authenticator{reader: reader, dummyHash: dummyHash}, nil
}

func (auth *Authenticator) Authenticate(ctx context.Context, username, suppliedPassword string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	if username == "" || suppliedPassword == "" {
		return Identity{}, ErrInvalidCredentials
	}

	user, lookupErr := auth.reader.FindByUsername(ctx, username)
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	if errors.Is(lookupErr, users.ErrNotFound) {
		_ = auth.dummyHash.Matches(suppliedPassword)
		if err := ctx.Err(); err != nil {
			return Identity{}, err
		}
		return Identity{}, ErrInvalidCredentials
	}
	if lookupErr != nil {
		return Identity{}, ErrUnavailable
	}
	if user.ID == "" || user.Username != username {
		return Identity{}, ErrUnavailable
	}
	hash, err := ParsePasswordHash(user.PasswordHash)
	if err != nil {
		return Identity{}, ErrUnavailable
	}
	matched := hash.Matches(suppliedPassword)
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}

	if !matched || !user.Active {
		return Identity{}, ErrInvalidCredentials
	}
	return Identity{UserID: user.ID}, nil
}
