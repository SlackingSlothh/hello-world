CREATE INDEX IF NOT EXISTS migration_jobs_database_idx
    ON migration_jobs (database_id, enqueued_at DESC);
