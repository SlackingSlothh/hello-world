package session

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid or expired session")
	ErrUnavailable = errors.New("session service unavailable")
	ErrNotFound    = errors.New("session not found")
)

type Digest [32]byte

type Session struct {
	ID          string
	UserID      string
	TokenDigest Digest `json:"-"`
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	RevokedAt   time.Time
}

type Store interface {
	Create(ctx context.Context, record Session) error
	Update(ctx context.Context, digest Digest, change func(*Session) error) error
	RevokeUser(ctx context.Context, userID string, revokedAt time.Time) error
}
