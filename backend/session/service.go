package session

import (
	"context"
	"crypto/rand"
	"errors"
	"time"
)

var errDifferentUser = errors.New("session belongs to another user")

type Validated struct {
	UserID      string
	Replacement *Issued `json:"-"`
}

type Service struct {
	store            Store
	config           Config
	identifierCipher *IdentifierCipher
	now              func() time.Time
}

func NewService(store Store, config Config, identifierCipher *IdentifierCipher, now func() time.Time) (*Service, error) {
	if store == nil {
		return nil, errors.New("session service requires a store")
	}
	if identifierCipher == nil || identifierCipher.aead == nil {
		return nil, errors.New("session service requires an identifier cipher")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, config: config, identifierCipher: identifierCipher, now: now}, nil
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
		CreatedAt: now, LastSeenAt: now, LastRotatedAt: now,
		ExpiresAt: now.Add(service.config.AbsoluteLifetime),
	}
	record.EncryptedIdentifier, err = service.identifierCipher.Encrypt(record.ID, identifier)
	if err != nil {
		return Issued{}, err
	}
	if err := service.store.Create(ctx, record); err != nil {
		return Issued{}, storageError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Issued{}, err
	}
	return Issued{identifier: identifier, expiresAt: record.ExpiresAt}, nil
}

func (service *Service) StartAfterLogin(ctx context.Context, userID string, previous Identifier) (Issued, error) {
	if userID == "" {
		return Issued{}, ErrInvalid
	}
	result, err := service.validate(ctx, previous, true, userID)
	if err == nil {
		if result.Replacement == nil {
			return Issued{}, ErrUnavailable
		}
		return *result.Replacement, nil
	}
	if errors.Is(err, errDifferentUser) {
		if err := service.Logout(ctx, previous); err != nil {
			return Issued{}, err
		}
		return service.Create(ctx, userID)
	}
	if errors.Is(err, ErrInvalid) {
		return service.Create(ctx, userID)
	}
	return Issued{}, err
}

func (service *Service) Validate(ctx context.Context, identifier Identifier) (Validated, error) {
	return service.validate(ctx, identifier, false, "")
}

func (service *Service) Rotate(ctx context.Context, identifier Identifier) (Validated, error) {
	return service.validate(ctx, identifier, true, "")
}

func (service *Service) validate(ctx context.Context, identifier Identifier, force bool, expectedUserID string) (Validated, error) {
	if err := ctx.Err(); err != nil {
		return Validated{}, err
	}
	digest, err := identifier.digest()
	if err != nil {
		return Validated{}, ErrInvalid
	}
	var result Validated
	err = service.store.Update(ctx, digest, func(record *Session) error {
		now := service.now().UTC()
		if err := service.checkSession(record, digest, now); err != nil {
			return err
		}
		if expectedUserID != "" && record.UserID != expectedUserID {
			return errDifferentUser
		}
		current, err := service.identifierCipher.Decrypt(record.ID, record.TokenDigest, record.EncryptedIdentifier)
		if err != nil {
			return err
		}
		isCurrent := record.TokenDigest == digest
		due := !now.Before(record.LastRotatedAt.Add(service.config.RotationInterval))
		if record.RotationRequired || (isCurrent && (force || due)) {
			current, err = service.rotate(record, now)
			if err != nil {
				return err
			}
		}
		if record.TokenDigest != digest || force {
			result.Replacement = &Issued{identifier: current, expiresAt: record.ExpiresAt}
		}
		record.LastSeenAt = now
		result.UserID = record.UserID
		return nil
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Validated{}, ctxErr
	}
	if errors.Is(err, errDifferentUser) {
		return Validated{}, errDifferentUser
	}
	if err != nil {
		return Validated{}, storageError(ctx, err)
	}
	if result.UserID == "" {
		return Validated{}, ErrUnavailable
	}
	return result, nil
}

func (service *Service) checkSession(record *Session, digest Digest, now time.Time) error {
	if record == nil || record.ID == "" || record.UserID == "" ||
		record.CreatedAt.IsZero() || record.LastSeenAt.Before(record.CreatedAt) ||
		record.LastRotatedAt.Before(record.CreatedAt) || record.LastRotatedAt.After(record.LastSeenAt) ||
		!record.ExpiresAt.After(record.CreatedAt) || now.Before(record.LastSeenAt) {
		return ErrInvalid
	}
	if !record.RevokedAt.IsZero() || !now.Before(record.ExpiresAt) ||
		!now.Before(record.LastSeenAt.Add(service.config.IdleTimeout)) ||
		!acceptsIdentifier(record, digest, now) {
		return ErrInvalid
	}
	return nil
}

func acceptsIdentifier(record *Session, digest Digest, now time.Time) bool {
	if record.TokenDigest == digest {
		return true
	}
	for _, previous := range record.PreviousIdentifiers {
		if previous.Digest == digest && now.Before(previous.ValidUntil) {
			return true
		}
	}
	return false
}

func (service *Service) rotate(record *Session, now time.Time) (Identifier, error) {
	identifier, err := newIdentifier()
	if err != nil {
		return Identifier{}, err
	}
	digest, err := identifier.digest()
	if err != nil {
		return Identifier{}, err
	}
	encrypted, err := service.identifierCipher.Encrypt(record.ID, identifier)
	if err != nil {
		return Identifier{}, err
	}
	previous := make([]PreviousIdentifier, 0, len(record.PreviousIdentifiers)+1)
	for _, alias := range record.PreviousIdentifiers {
		if now.Before(alias.ValidUntil) {
			previous = append(previous, alias)
		}
	}
	validUntil := now.Add(service.config.GracePeriod)
	if record.ExpiresAt.Before(validUntil) {
		validUntil = record.ExpiresAt
	}
	previous = append(previous, PreviousIdentifier{Digest: record.TokenDigest, ValidUntil: validUntil})
	record.PreviousIdentifiers = previous
	record.TokenDigest = digest
	record.EncryptedIdentifier = encrypted
	record.LastRotatedAt = now
	record.RotationRequired = false
	return identifier, nil
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
		if record == nil || !acceptsIdentifier(record, digest, service.now().UTC()) {
			return ErrNotFound
		}
		if record.RevokedAt.IsZero() {
			record.RevokedAt = service.now().UTC()
		}
		return nil
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
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

func (service *Service) RequireRotation(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if userID == "" {
		return ErrInvalid
	}
	return storageError(ctx, service.store.RequireRotation(ctx, userID))
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
