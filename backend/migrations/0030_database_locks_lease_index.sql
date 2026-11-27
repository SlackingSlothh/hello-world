CREATE INDEX IF NOT EXISTS database_locks_lease_idx
    ON database_locks (lock_state, lease_expires_at);
