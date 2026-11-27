CREATE TABLE IF NOT EXISTS database_locks (
    database_id TEXT PRIMARY KEY REFERENCES managed_db_connections(id) ON DELETE CASCADE,
    job_id UUID NOT NULL REFERENCES migration_jobs(job_id) ON DELETE CASCADE,
    execution_id UUID NOT NULL,
    worker_id TEXT NOT NULL,
    lock_state TEXT NOT NULL CHECK (lock_state IN ('active', 'held_partial', 'held_uncertain')),
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lease_expires_at TIMESTAMPTZ NOT NULL
);
