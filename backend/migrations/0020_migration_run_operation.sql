ALTER TABLE migration_runs
    ADD COLUMN operation TEXT NOT NULL DEFAULT 'APPLY'
        CHECK (operation IN ('APPLY', 'ROLLBACK', 'FORWARD')),
    ADD COLUMN rollback_target_run_id BIGINT,
    ADD CONSTRAINT migration_runs_id_migration_unique UNIQUE (id, migration_id),
    ADD CONSTRAINT migration_runs_rollback_target_fk
        FOREIGN KEY (rollback_target_run_id, migration_id)
        REFERENCES migration_runs (id, migration_id),
    ADD CONSTRAINT migration_runs_rollback_target_check
        CHECK ((operation = 'ROLLBACK') = (rollback_target_run_id IS NOT NULL)
            AND rollback_target_run_id IS DISTINCT FROM id);
