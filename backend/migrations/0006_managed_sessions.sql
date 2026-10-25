CREATE TABLE IF NOT EXISTS managed_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    token_digest BYTEA NOT NULL UNIQUE,
    encrypted_identifier BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    last_rotated_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NULL,
    rotation_required BOOLEAN NOT NULL DEFAULT FALSE
);
