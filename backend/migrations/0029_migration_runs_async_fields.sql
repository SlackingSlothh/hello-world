ALTER TABLE migration_runs
    DROP CONSTRAINT IF EXISTS migration_runs_operation_check,
    ADD CONSTRAINT migration_runs_operation_check
        CHECK (operation IN ('APPLY', 'ROLLBACK', 'FORWARD', 'RESUME_BATCH', 'REVERT_BATCH')),
    ADD COLUMN IF NOT EXISTS job_id UUID REFERENCES migration_jobs(job_id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS execution_uuid UUID;
