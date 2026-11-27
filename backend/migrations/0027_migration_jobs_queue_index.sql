CREATE INDEX IF NOT EXISTS migration_jobs_queue_idx
    ON migration_jobs (status, next_retry_at, enqueued_at);
