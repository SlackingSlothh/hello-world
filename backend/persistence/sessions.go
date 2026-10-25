package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"hello-world/session"
)

// SessionStore persists only one-way digests for browser identifiers. The
// encrypted current identifier is retained server-side for safe token rotation.
type SessionStore struct{ db *sql.DB }

func NewSessionStore(db *sql.DB) (*SessionStore, error) {
	if db == nil {
		return nil, errors.New("session store database is required")
	}
	return &SessionStore{db: db}, nil
}

func (s *SessionStore) Create(ctx context.Context, record session.Session) error {
	if record.ID == "" || record.UserID == "" || len(record.TokenDigest) != 32 || len(record.EncryptedIdentifier) == 0 {
		return session.ErrInvalid
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO managed_sessions
		(id,user_id,token_digest,encrypted_identifier,created_at,last_seen_at,last_rotated_at,expires_at,revoked_at,rotation_required)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, record.ID, record.UserID, record.TokenDigest[:], record.EncryptedIdentifier,
		record.CreatedAt, record.LastSeenAt, record.LastRotatedAt, record.ExpiresAt, nullableTime(record.RevokedAt), record.RotationRequired)
	return err
}

func (s *SessionStore) Update(ctx context.Context, digest session.Digest, change func(*session.Session) error) error {
	if change == nil {
		return session.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	record, err := scanSession(tx.QueryRowContext(ctx, `SELECT s.id,s.user_id,s.token_digest,s.encrypted_identifier,
		s.created_at,s.last_seen_at,s.last_rotated_at,s.expires_at,s.revoked_at,s.rotation_required
		FROM managed_sessions s WHERE s.token_digest=$1 OR EXISTS
		(SELECT 1 FROM managed_session_aliases a WHERE a.session_id=s.id AND a.digest=$1) FOR UPDATE`, digest[:]))
	if errors.Is(err, sql.ErrNoRows) {
		return session.ErrNotFound
	}
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT digest,valid_until FROM managed_session_aliases WHERE session_id=$1`, record.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw []byte
		var alias session.PreviousIdentifier
		if err := rows.Scan(&raw, &alias.ValidUntil); err != nil {
			rows.Close()
			return err
		}
		if len(raw) != len(alias.Digest) {
			rows.Close()
			return errors.New("invalid stored session digest")
		}
		copy(alias.Digest[:], raw)
		record.PreviousIdentifiers = append(record.PreviousIdentifiers, alias)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if err := change(&record); err != nil {
		return err
	}
	if len(record.TokenDigest) != 32 {
		return session.ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `UPDATE managed_sessions SET user_id=$2,token_digest=$3,encrypted_identifier=$4,
		created_at=$5,last_seen_at=$6,last_rotated_at=$7,expires_at=$8,revoked_at=$9,rotation_required=$10 WHERE id=$1`,
		record.ID, record.UserID, record.TokenDigest[:], record.EncryptedIdentifier, record.CreatedAt, record.LastSeenAt, record.LastRotatedAt,
		record.ExpiresAt, nullableTime(record.RevokedAt), record.RotationRequired)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM managed_session_aliases WHERE session_id=$1`, record.ID); err != nil {
		return err
	}
	for _, alias := range record.PreviousIdentifiers {
		if _, err = tx.ExecContext(ctx, `INSERT INTO managed_session_aliases(session_id,digest,valid_until) VALUES($1,$2,$3)`, record.ID, alias.Digest[:], alias.ValidUntil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SessionStore) RevokeUser(ctx context.Context, userID string, revokedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE managed_sessions SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, userID, revokedAt)
	return err
}

func (s *SessionStore) RequireRotation(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE managed_sessions SET rotation_required=TRUE WHERE user_id=$1 AND revoked_at IS NULL`, userID)
	return err
}

type rowScanner interface{ Scan(...any) error }

func scanSession(row rowScanner) (session.Session, error) {
	var record session.Session
	var digest []byte
	var revoked sql.NullTime
	err := row.Scan(&record.ID, &record.UserID, &digest, &record.EncryptedIdentifier, &record.CreatedAt, &record.LastSeenAt, &record.LastRotatedAt, &record.ExpiresAt, &revoked, &record.RotationRequired)
	if err != nil {
		return record, err
	}
	if len(digest) != len(record.TokenDigest) {
		return record, errors.New("invalid stored session digest")
	}
	copy(record.TokenDigest[:], digest)
	if revoked.Valid {
		record.RevokedAt = revoked.Time
	}
	return record, nil
}
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
