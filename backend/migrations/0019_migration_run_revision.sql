ALTER TABLE migration_runs
    ADD COLUMN revision BIGINT NOT NULL CHECK (revision > 0);
