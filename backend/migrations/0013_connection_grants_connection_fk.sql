DO $migration$
BEGIN
    DELETE FROM connection_grants cg
    WHERE NOT EXISTS (
        SELECT 1 FROM managed_db_connections c WHERE c.id = cg.database_id
    );
    ALTER TABLE connection_grants
        ADD CONSTRAINT connection_grants_database_id_fkey
        FOREIGN KEY (database_id) REFERENCES managed_db_connections(id) ON DELETE CASCADE;
END
$migration$;
