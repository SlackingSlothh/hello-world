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

type PreviousIdentifier struct {
	Digest     Digest `json:"-"`
	ValidUntil time.Time
}

type Session struct {
	ID                  string
	UserID              string
	TokenDigest         Digest               `json:"-"`
	EncryptedIdentifier []byte               `json:"-"`
	PreviousIdentifiers []PreviousIdentifier `json:"-"`
	CreatedAt           time.Time
	LastSeenAt          time.Time
	LastRotatedAt       time.Time
	ExpiresAt           time.Time
	RevokedAt           time.Time
	RotationRequired    bool
}

type Store interface {
	Create(ctx context.Context, record Session) error
	Update(ctx context.Context, digest Digest, change func(*Session) error) error
	RevokeUser(ctx context.Context, userID string, revokedAt time.Time) error
	RequireRotation(ctx context.Context, userID string) error
}
