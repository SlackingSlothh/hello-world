CREATE TABLE migration_request_approvals (
    request_id BIGINT NOT NULL REFERENCES migration_requests(id) ON DELETE CASCADE,
    group_id TEXT NOT NULL REFERENCES access_groups(id) ON DELETE CASCADE,
    reviewer_id TEXT NOT NULL REFERENCES platform_users(id) ON DELETE RESTRICT,
    approved_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (request_id, group_id)
);
