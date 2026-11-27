CREATE UNIQUE INDEX migration_runs_one_running_per_database
    ON migration_runs (database_id) WHERE status = 'RUNNING';
