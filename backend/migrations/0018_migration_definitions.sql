ALTER TABLE migrations
    ADD COLUMN sql_down TEXT NOT NULL DEFAULT '',
    ADD COLUMN irreversible_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    ADD COLUMN frozen_at TIMESTAMPTZ,
    ADD CONSTRAINT migrations_rollback_definition_check
        CHECK ((sql_down ~ '[^[:space:]]') <> (irreversible_reason ~ '[^[:space:]]'));
