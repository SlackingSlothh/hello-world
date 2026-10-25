package persistence

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"hello-world/manageddb"
)

var ErrInvalidEncryptionKey = errors.New("database encryption key must contain at least 32 bytes")

// ConnectionStore persists the connection metadata and an authenticated,
// encrypted DSN in the application's PostgreSQL database.
type ConnectionStore struct {
	db   *sql.DB
	aead cipher.AEAD
}

func NewConnectionStore(db *sql.DB, key string) (*ConnectionStore, error) {
	if db == nil {
		return nil, errors.New("connection store database is required")
	}
	if len(key) < 32 {
		return nil, ErrInvalidEncryptionKey
	}
	derived := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return nil, fmt.Errorf("initialize connection encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize connection encryption: %w", err)
	}
	return &ConnectionStore{db: db, aead: aead}, nil
}

func (s *ConnectionStore) Save(ctx context.Context, config manageddb.Config) error {
	if config.ID == "" {
		return errors.New("connection ID is required for persistence")
	}
	if config.DSN == "" {
		return errors.New("connection DSN is required for persistence")
	}
	ciphertext, err := s.seal(config.ID, config.DSN)
	if err != nil {
		return fmt.Errorf("encrypt connection DSN: %w", err)
	}
	pool := config.Pool
	maxLifetime := pool.MaxLifetimeSeconds
	if maxLifetime == 0 {
		maxLifetime = int64(pool.MaxLifetime / time.Second)
	}
	maxIdleTime := pool.MaxIdleTimeSeconds
	if maxIdleTime == 0 {
		maxIdleTime = int64(pool.MaxIdleTime / time.Second)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO managed_db_connections
		(id,name,driver,dsn_ciphertext,pool_max_open,pool_max_idle,pool_max_lifetime_seconds,pool_max_idle_time_seconds,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,driver=EXCLUDED.driver,dsn_ciphertext=EXCLUDED.dsn_ciphertext,
		pool_max_open=EXCLUDED.pool_max_open,pool_max_idle=EXCLUDED.pool_max_idle,
		pool_max_lifetime_seconds=EXCLUDED.pool_max_lifetime_seconds,
		pool_max_idle_time_seconds=EXCLUDED.pool_max_idle_time_seconds,updated_at=CURRENT_TIMESTAMP`,
		config.ID, config.Name, config.Driver, ciphertext, config.Pool.MaxOpen, config.Pool.MaxIdle, maxLifetime, maxIdleTime)
	if err != nil {
		return fmt.Errorf("save managed database connection: %w", err)
	}
	return nil
}

func (s *ConnectionStore) LoadAll(ctx context.Context) ([]manageddb.Config, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,driver,dsn_ciphertext,pool_max_open,pool_max_idle,pool_max_lifetime_seconds,pool_max_idle_time_seconds FROM managed_db_connections ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("load managed database connections: %w", err)
	}
	defer rows.Close()
	configs := make([]manageddb.Config, 0)
	for rows.Next() {
		var config manageddb.Config
		var ciphertext []byte
		var lifetime, idleTime int64
		if err := rows.Scan(&config.ID, &config.Name, &config.Driver, &ciphertext, &config.Pool.MaxOpen, &config.Pool.MaxIdle, &lifetime, &idleTime); err != nil {
			return nil, fmt.Errorf("read managed database connection: %w", err)
		}
		dsn, err := s.open(config.ID, ciphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt managed database connection %s: %w", config.ID, err)
		}
		config.DSN = dsn
		config.Pool.MaxLifetimeSeconds = lifetime
		config.Pool.MaxIdleTimeSeconds = idleTime
		configs = append(configs, config)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read managed database connections: %w", err)
	}
	return configs, nil
}

func (s *ConnectionStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM managed_db_connections WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete managed database connection: %w", err)
	}
	return nil
}

func (s *ConnectionStore) seal(id, plaintext string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(plaintext), []byte(id)), nil
}

func (s *ConnectionStore) open(id string, ciphertext []byte) (string, error) {
	nonceSize := s.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("encrypted DSN is truncated")
	}
	nonce, payload := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := s.aead.Open(nil, nonce, payload, []byte(id))
	if err != nil {
		return "", errors.New("encrypted DSN authentication failed")
	}
	return string(plaintext), nil
}
