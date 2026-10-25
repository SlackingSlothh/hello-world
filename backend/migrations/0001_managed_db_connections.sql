CREATE TABLE IF NOT EXISTS managed_db_connections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    driver TEXT NOT NULL,
    dsn_ciphertext BYTEA NOT NULL,
    pool_max_open INTEGER NOT NULL DEFAULT 0,
    pool_max_idle INTEGER NOT NULL DEFAULT 0,
    pool_max_lifetime_seconds BIGINT NOT NULL DEFAULT 0,
    pool_max_idle_time_seconds BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
