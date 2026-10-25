CREATE TABLE IF NOT EXISTS managed_session_aliases (
    session_id TEXT NOT NULL REFERENCES managed_sessions(id) ON DELETE CASCADE,
    digest BYTEA NOT NULL UNIQUE,
    valid_until TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (session_id, digest)
);
