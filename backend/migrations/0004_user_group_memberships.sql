CREATE TABLE IF NOT EXISTS user_group_memberships (
    user_id TEXT NOT NULL REFERENCES platform_users(id) ON DELETE CASCADE,
    group_id TEXT NOT NULL REFERENCES access_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);
