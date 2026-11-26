package manageddb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotFound      = errors.New("managed database connection not found")
	ErrAlreadyExists = errors.New("managed database connection already exists")
	ErrDisconnected  = errors.New("managed database connection is disconnected")
)

// PoolConfig controls a database/sql connection pool. A zero limit means
// database/sql's default (unlimited for max open, no idle connections for max idle).
type PoolConfig struct {
	MaxOpen            int           `json:"maxOpen"`
	MaxIdle            int           `json:"maxIdle"`
	MaxLifetime        time.Duration `json:"-"`
	MaxIdleTime        time.Duration `json:"-"`
	MaxLifetimeSeconds int64         `json:"maxLifetimeSeconds,omitempty"`
	MaxIdleTimeSeconds int64         `json:"maxIdleTimeSeconds,omitempty"`
}

// Config contains credentials and therefore must never be serialized or logged.
type Config struct {
	ID     string     `json:"id,omitempty"`
	Name   string     `json:"name"`
	Driver string     `json:"driver"`
	DSN    string     `json:"-"`
	Pool   PoolConfig `json:"pool"`
}

// Connection is the safe, public view of a managed connection.
type Connection struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Driver    string      `json:"driver"`
	Connected bool        `json:"connected"`
	CreatedAt time.Time   `json:"createdAt"`
	Pool      PoolConfig  `json:"pool"`
	Stats     sql.DBStats `json:"poolStats"`
}

// DBOpener creates a database/sql pool for an already allowlisted SQL driver.
type DBOpener func(driverName, dsn string) (*sql.DB, error)

type managedConnection struct {
	config    Config
	db        *sql.DB
	createdAt time.Time
}

// Registry owns the lifetime of all runtime database handles. Use NewRegistry
// so the allowlisted SQL drivers are registered before connections are opened.
type Registry struct {
	mu          sync.RWMutex
	connections map[string]*managedConnection
	open        DBOpener
	pingTimeout time.Duration
}

// NewRegistry creates an empty registry using the built-in PostgreSQL and
// MySQL/MariaDB driver mappings.
func NewRegistry() *Registry {
	return NewRegistryWithOpener(sql.Open)
}

// NewRegistryWithOpener lets an embedding application provide its own opener.
// Public driver names are still validated against the built-in allowlist.
func NewRegistryWithOpener(open DBOpener) *Registry {
	if open == nil {
		open = sql.Open
	}
	return &Registry{
		connections: make(map[string]*managedConnection),
		open:        open,
		pingTimeout: 5 * time.Second,
	}
}

// Add opens, configures, and verifies a connection before making it available.
// The returned value contains no DSN or password.
func (r *Registry) Add(ctx context.Context, config Config) (Connection, error) {
	config.Driver = normalizeDriver(config.Driver)
	config.Pool = normalizedPool(config.Pool)
	if err := validateConfig(config); err != nil {
		return Connection{}, err
	}
	driverName, ok := sqlDriverName(config.Driver)
	if !ok {
		return Connection{}, fmt.Errorf("unsupported database driver %q", config.Driver)
	}
	db, err := r.open(driverName, config.DSN)
	if err != nil {
		return Connection{}, fmt.Errorf("open database: %w", err)
	}
	applyPool(db, config.Pool)
	if err := r.ping(ctx, db); err != nil {
		_ = db.Close()
		return Connection{}, fmt.Errorf("ping database: %w", err)
	}

	if config.ID == "" {
		config.ID, err = newID()
		if err != nil {
			_ = db.Close()
			return Connection{}, fmt.Errorf("create connection ID: %w", err)
		}
	}
	now := time.Now().UTC()
	entry := &managedConnection{config: config, db: db, createdAt: now}
	r.mu.Lock()
	if _, exists := r.connections[config.ID]; exists {
		r.mu.Unlock()
		_ = db.Close()
		return Connection{}, ErrAlreadyExists
	}
	r.connections[config.ID] = entry
	r.mu.Unlock()
	return publicConnection(entry), nil
}

// Restore registers a previously persisted configuration without pinging its
// target. database/sql reconnects on demand, so an unavailable target does not
// prevent the application itself from starting.
func (r *Registry) Restore(config Config) (Connection, error) {
	config.Driver = normalizeDriver(config.Driver)
	config.Pool = normalizedPool(config.Pool)
	if config.ID == "" {
		return Connection{}, errors.New("connection ID is required for restore")
	}
	if err := validateConfig(config); err != nil {
		return Connection{}, err
	}
	driverName, ok := sqlDriverName(config.Driver)
	if !ok {
		return Connection{}, fmt.Errorf("unsupported database driver %q", config.Driver)
	}
	db, err := r.open(driverName, config.DSN)
	if err != nil {
		return Connection{}, fmt.Errorf("open restored database: %w", err)
	}
	applyPool(db, config.Pool)
	entry := &managedConnection{config: config, db: db, createdAt: time.Now().UTC()}
	r.mu.Lock()
	if _, exists := r.connections[config.ID]; exists {
		r.mu.Unlock()
		_ = db.Close()
		return Connection{}, ErrAlreadyExists
	}
	r.connections[config.ID] = entry
	r.mu.Unlock()
	return publicConnection(entry), nil
}

// Get returns safe connection metadata and current pool statistics.
func (r *Registry) Get(id string) (Connection, error) {
	r.mu.RLock()
	entry, ok := r.connections[id]
	if !ok {
		r.mu.RUnlock()
		return Connection{}, ErrNotFound
	}
	view := publicConnection(entry)
	r.mu.RUnlock()
	return view, nil
}

// List returns safe metadata for all registered connections.
func (r *Registry) List() []Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]Connection, 0, len(r.connections))
	for _, entry := range r.connections {
		items = append(items, publicConnection(entry))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

// DB returns the concurrency-safe database/sql pool for internal database
// operations. Callers must not close it; the registry owns that responsibility.
func (r *Registry) DB(id string) (*sql.DB, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.connections[id]
	if !ok {
		return nil, ErrNotFound
	}
	if entry.db == nil {
		return nil, ErrDisconnected
	}
	return entry.db, nil
}

// Connect opens and verifies a previously disconnected connection.
func (r *Registry) Connect(ctx context.Context, id string) (Connection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.connections[id]
	if !ok {
		return Connection{}, ErrNotFound
	}
	if entry.db != nil {
		return publicConnection(entry), nil
	}
	driverName, ok := sqlDriverName(entry.config.Driver)
	if !ok {
		return Connection{}, fmt.Errorf("unsupported database driver %q", entry.config.Driver)
	}
	db, err := r.open(driverName, entry.config.DSN)
	if err != nil {
		return Connection{}, fmt.Errorf("open database: %w", err)
	}
	applyPool(db, entry.config.Pool)
	if err := r.ping(ctx, db); err != nil {
		_ = db.Close()
		return Connection{}, fmt.Errorf("ping database: %w", err)
	}
	entry.db = db
	return publicConnection(entry), nil
}

// Disconnect closes the runtime pool but retains its configuration in the registry.
func (r *Registry) Disconnect(id string) error {
	r.mu.Lock()
	entry, ok := r.connections[id]
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	if entry.db == nil {
		r.mu.Unlock()
		return nil
	}
	db := entry.db
	entry.db = nil
	r.mu.Unlock()
	return db.Close()
}

// ConfigForStorage returns secret-bearing configuration to the persistence
// layer. Do not serialize it or return it from an HTTP handler.
func (r *Registry) ConfigForStorage(id string) (Config, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.connections[id]
	if !ok {
		return Config{}, ErrNotFound
	}
	return entry.config, nil
}

// ConnectionDB returns matching safe metadata and a pool handle from the same
// registry snapshot. Callers must not close the returned handle.
func (r *Registry) ConnectionDB(id string) (Connection, *sql.DB, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.connections[id]
	if !ok {
		return Connection{}, nil, ErrNotFound
	}
	if entry.db == nil {
		return publicConnection(entry), nil, ErrDisconnected
	}
	return publicConnection(entry), entry.db, nil
}

// Test pings a managed connection and returns the round-trip duration.
func (r *Registry) Test(ctx context.Context, id string) (time.Duration, error) {
	db, err := r.DB(id)
	if err != nil {
		return 0, err
	}
	started := time.Now()
	if err := r.ping(ctx, db); err != nil {
		return time.Since(started), fmt.Errorf("ping database: %w", err)
	}
	return time.Since(started), nil
}

// UpdatePool validates and applies pool settings to an existing connection.
func (r *Registry) UpdatePool(id string, pool PoolConfig) (Connection, error) {
	pool = normalizedPool(pool)
	if err := validatePool(pool); err != nil {
		return Connection{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.connections[id]
	if !ok {
		return Connection{}, ErrNotFound
	}
	if entry.db != nil {
		applyPool(entry.db, pool)
	}
	entry.config.Pool = pool
	return publicConnection(entry), nil
}

// Replace validates a new DSN/config and swaps it in only after it can connect.
// Existing in-flight operations may finish on the old pool; it is then closed.
func (r *Registry) Replace(ctx context.Context, id string, config Config) (Connection, error) {
	config.ID = id
	config.Driver = normalizeDriver(config.Driver)
	config.Pool = normalizedPool(config.Pool)
	if err := validateConfig(config); err != nil {
		return Connection{}, err
	}
	driverName, ok := sqlDriverName(config.Driver)
	if !ok {
		return Connection{}, fmt.Errorf("unsupported database driver %q", config.Driver)
	}
	next, err := r.open(driverName, config.DSN)
	if err != nil {
		return Connection{}, fmt.Errorf("open database: %w", err)
	}
	applyPool(next, config.Pool)
	if err := r.ping(ctx, next); err != nil {
		_ = next.Close()
		return Connection{}, fmt.Errorf("ping database: %w", err)
	}

	r.mu.Lock()
	entry, exists := r.connections[id]
	if !exists {
		r.mu.Unlock()
		_ = next.Close()
		return Connection{}, ErrNotFound
	}
	old := entry.db
	entry.config = config
	entry.db = next
	view := publicConnection(entry)
	r.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return view, nil
}

// Remove deregisters and closes a managed connection. sql.DB.Close prevents new
// queries and waits for in-flight work to finish according to database/sql.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	entry, ok := r.connections[id]
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	delete(r.connections, id)
	r.mu.Unlock()
	if entry.db == nil {
		return nil
	}
	return entry.db.Close()
}

// Close closes every registered pool and empties the registry.
func (r *Registry) Close() error {
	r.mu.Lock()
	entries := make([]*managedConnection, 0, len(r.connections))
	for _, entry := range r.connections {
		entries = append(entries, entry)
	}
	r.connections = make(map[string]*managedConnection)
	r.mu.Unlock()

	var errs []error
	for _, entry := range entries {
		if entry.db == nil {
			continue
		}
		if err := entry.db.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Registry) ping(ctx context.Context, db *sql.DB) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.pingTimeout)
		defer cancel()
	}
	return db.PingContext(ctx)
}

func publicConnection(entry *managedConnection) Connection {
	pool := entry.config.Pool
	pool.MaxLifetimeSeconds = int64(pool.MaxLifetime / time.Second)
	pool.MaxIdleTimeSeconds = int64(pool.MaxIdleTime / time.Second)
	var stats sql.DBStats
	connected := entry.db != nil
	if connected {
		stats = entry.db.Stats()
	}
	return Connection{
		ID: entry.config.ID, Name: entry.config.Name, Driver: entry.config.Driver,
		Connected: connected, CreatedAt: entry.createdAt, Pool: pool, Stats: stats,
	}
}

func applyPool(db *sql.DB, pool PoolConfig) {
	db.SetMaxOpenConns(pool.MaxOpen)
	db.SetMaxIdleConns(pool.MaxIdle)
	db.SetConnMaxLifetime(pool.MaxLifetime)
	db.SetConnMaxIdleTime(pool.MaxIdleTime)
}

func validateConfig(config Config) error {
	if config.Name == "" {
		return errors.New("connection name is required")
	}
	if config.DSN == "" {
		return errors.New("database DSN is required")
	}
	if _, ok := sqlDriverName(config.Driver); !ok {
		return fmt.Errorf("unsupported database driver %q", config.Driver)
	}
	return validatePool(config.Pool)
}

func validatePool(pool PoolConfig) error {
	if pool.MaxOpen < 0 || pool.MaxIdle < 0 || pool.MaxLifetime < 0 || pool.MaxIdleTime < 0 ||
		pool.MaxLifetimeSeconds < 0 || pool.MaxIdleTimeSeconds < 0 {
		return errors.New("pool settings cannot be negative")
	}
	if pool.MaxOpen > 0 && pool.MaxIdle > pool.MaxOpen {
		return errors.New("max idle connections cannot exceed max open connections")
	}
	return nil
}

func normalizedPool(pool PoolConfig) PoolConfig {
	if pool.MaxLifetime == 0 && pool.MaxLifetimeSeconds > 0 {
		pool.MaxLifetime = time.Duration(pool.MaxLifetimeSeconds) * time.Second
	}
	if pool.MaxIdleTime == 0 && pool.MaxIdleTimeSeconds > 0 {
		pool.MaxIdleTime = time.Duration(pool.MaxIdleTimeSeconds) * time.Second
	}
	return pool
}

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
