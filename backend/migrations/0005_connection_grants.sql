CREATE TABLE IF NOT EXISTS connection_grants (
    group_id TEXT NOT NULL REFERENCES access_groups(id) ON DELETE CASCADE,
    database_id TEXT NOT NULL,
    access_level SMALLINT NOT NULL CHECK (access_level BETWEEN 0 AND 2),
    PRIMARY KEY (group_id, database_id)
);
