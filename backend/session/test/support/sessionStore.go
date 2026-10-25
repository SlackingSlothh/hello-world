package support

import (
	"context"
	"errors"
	"sync"
	"time"

	"hello-world/session"
)

type Store struct {
	mu          sync.Mutex
	records     map[string]session.Session
	CommitError error
}

func NewStore() *Store {
	return &Store{records: make(map[string]session.Session)}
}

func clone(record session.Session) session.Session {
	record.EncryptedIdentifier = append([]byte(nil), record.EncryptedIdentifier...)
	record.PreviousIdentifiers = append([]session.PreviousIdentifier(nil), record.PreviousIdentifiers...)
	return record
}

func (store *Store) Create(ctx context.Context, record session.Session) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, exists := store.records[record.ID]; exists {
		return errors.New("duplicate session")
	}
	store.records[record.ID] = clone(record)
	return nil
}

func (store *Store) Update(ctx context.Context, digest session.Digest, change func(*session.Session) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	for id, record := range store.records {
		matches := record.TokenDigest == digest
		for _, previous := range record.PreviousIdentifiers {
			matches = matches || previous.Digest == digest
		}
		if !matches {
			continue
		}
		updated := clone(record)
		if err := change(&updated); err != nil {
			return err
		}
		if store.CommitError != nil {
			return store.CommitError
		}
		store.records[id] = clone(updated)
		return nil
	}
	return session.ErrNotFound
}

func (store *Store) RevokeUser(ctx context.Context, userID string, revokedAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	for id, record := range store.records {
		if record.UserID == userID && record.RevokedAt.IsZero() {
			record.RevokedAt = revokedAt
			store.records[id] = record
		}
	}
	return nil
}

func (store *Store) RequireRotation(ctx context.Context, userID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	for id, record := range store.records {
		if record.UserID == userID && record.RevokedAt.IsZero() {
			record.RotationRequired = true
			store.records[id] = record
		}
	}
	return nil
}

func (store *Store) Records() []session.Session {
	store.mu.Lock()
	defer store.mu.Unlock()
	records := make([]session.Session, 0, len(store.records))
	for _, record := range store.records {
		records = append(records, clone(record))
	}
	return records
}
