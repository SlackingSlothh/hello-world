ALTER TABLE migration_requests
    ADD COLUMN job_id UUID UNIQUE REFERENCES migration_jobs(job_id);
