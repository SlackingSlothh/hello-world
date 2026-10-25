package manageddb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidUpdate = errors.New("invalid connection update")
var ErrConnectionFailed = errors.New("could not verify replacement connection")

type ConnectionUpdate struct {
	Name   *string
	Driver *string
	DSN    string
	Pool   *PoolConfig
}

func (r *Registry) Update(ctx context.Context, id string, change ConnectionUpdate, save func(context.Context, Config) error) (Connection, error) {
	r.mu.Lock()
	var retired *sql.DB
	defer func() {
		r.mu.Unlock()
		if retired != nil {
			_ = retired.Close()
		}
	}()
	entry, ok := r.connections[id]
	if !ok {
		return Connection{}, ErrNotFound
	}
	if change.Name == nil && change.Driver == nil && change.DSN == "" && change.Pool == nil {
		return Connection{}, fmt.Errorf("%w: provide name, driver, DSN, or pool", ErrInvalidUpdate)
	}
	config := entry.config
	if change.Name != nil {
		config.Name = strings.TrimSpace(*change.Name)
		if config.Name == "" || len(config.Name) > 100 || !utf8.ValidString(config.Name) || strings.ContainsFunc(config.Name, unicode.IsControl) {
			return Connection{}, fmt.Errorf("%w: name must be 1–100 bytes without control characters", ErrInvalidUpdate)
		}
	}
	if change.Driver != nil {
		config.Driver = normalizeDriver(*change.Driver)
	}
	if config.Driver != entry.config.Driver && change.DSN == "" {
		return Connection{}, fmt.Errorf("%w: a new DSN is required when changing the database engine", ErrInvalidUpdate)
	}
	if change.DSN != "" {
		config.DSN = change.DSN
	}
	if change.Pool != nil {
		config.Pool = normalizedPool(*change.Pool)
	}
	if err := validateConfig(config); err != nil {
		return Connection{}, fmt.Errorf("%w: %s", ErrInvalidUpdate, err)
	}
	next := entry.db
	replacement := config.DSN != entry.config.DSN || config.Driver != entry.config.Driver
	if replacement {
		driverName, _ := sqlDriverName(config.Driver)
		var err error
		next, err = r.open(driverName, config.DSN)
		if err != nil {
			return Connection{}, ErrConnectionFailed
		}
		applyPool(next, config.Pool)
		if err := r.ping(ctx, next); err != nil {
			_ = next.Close()
			return Connection{}, ErrConnectionFailed
		}
	}
	if save != nil {
		if err := save(ctx, config); err != nil {
			if replacement {
				_ = next.Close()
			}
			return Connection{}, err
		}
	}
	old := entry.db
	entry.config = config
	entry.db = next
	if next != nil {
		applyPool(next, config.Pool)
	}
	view := publicConnection(entry)
	if replacement {
		retired = old
	}
	return view, nil
}
