package session

import (
	"context"
	"crypto/rand"
	"errors"
	"time"
)

type Service struct {
	store  Store
	config Config
	now    func() time.Time
}

func NewService(store Store, config Config, now func() time.Time) (*Service, error) {
	if store == nil {
		return nil, errors.New("session service requires a store")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, config: config, now: now}, nil
}

func (service *Service) Create(ctx context.Context, userID string) (Issued, error) {
	if err := ctx.Err(); err != nil {
		return Issued{}, err
	}
	if userID == "" {
		return Issued{}, ErrInvalid
	}
	identifier, err := newIdentifier()
	if err != nil {
		return Issued{}, err
	}
	digest, err := identifier.digest()
	if err != nil {
		return Issued{}, err
	}
	now := service.now().UTC()
	record := Session{
		ID: rand.Text(), UserID: userID, TokenDigest: digest,
		CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(service.config.AbsoluteLifetime),
	}
	if err := service.store.Create(ctx, record); err != nil {
		return Issued{}, storageError(ctx, err)
	}
	return Issued{identifier: identifier, expiresAt: record.ExpiresAt}, nil
}

func (service *Service) Validate(ctx context.Context, identifier Identifier) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	digest, err := identifier.digest()
	if err != nil {
		return "", ErrInvalid
	}
	var userID string
	err = service.store.Update(ctx, digest, func(record *Session) error {
		now := service.now().UTC()
		if record == nil || record.ID == "" || record.UserID == "" || record.TokenDigest != digest ||
			record.CreatedAt.IsZero() || record.LastSeenAt.Before(record.CreatedAt) ||
			!record.ExpiresAt.After(record.CreatedAt) || now.Before(record.LastSeenAt) {
			return ErrInvalid
		}
		if !record.RevokedAt.IsZero() || !now.Before(record.ExpiresAt) ||
			!now.Before(record.LastSeenAt.Add(service.config.IdleTimeout)) {
			return ErrInvalid
		}
		record.LastSeenAt = now
		userID = record.UserID
		return nil
	})
	if err != nil {
		return "", storageError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return userID, nil
}

func (service *Service) Logout(ctx context.Context, identifier Identifier) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	digest, err := identifier.digest()
	if err != nil {
		return nil
	}
	err = service.store.Update(ctx, digest, func(record *Session) error {
		if record == nil || record.TokenDigest != digest {
			return ErrInvalid
		}
		if record.RevokedAt.IsZero() {
			record.RevokedAt = service.now().UTC()
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return storageError(ctx, err)
}

func (service *Service) RevokeUser(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if userID == "" {
		return ErrInvalid
	}
	return storageError(ctx, service.store.RevokeUser(ctx, userID, service.now().UTC()))
}

func storageError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
		return ErrInvalid
	}
	return ErrUnavailable
}
