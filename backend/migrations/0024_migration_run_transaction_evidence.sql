ALTER TABLE migration_runs
    ADD COLUMN target_xact_id TEXT,
    ADD COLUMN target_identity TEXT,
    ADD CONSTRAINT migration_runs_target_transaction_evidence CHECK (
        (target_xact_id IS NULL AND target_identity IS NULL)
        OR (target_xact_id IS NOT NULL AND target_identity IS NOT NULL AND sql_dispatch_started IS TRUE)
    );
