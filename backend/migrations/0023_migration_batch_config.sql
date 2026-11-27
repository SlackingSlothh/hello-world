ALTER TABLE migrations
    ADD COLUMN IF NOT EXISTS execution_mode TEXT NOT NULL DEFAULT 'normal'
        CHECK (execution_mode IN ('normal', 'batch')),
    ADD COLUMN IF NOT EXISTS batch_by_attribute TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS batch_size INTEGER CHECK (batch_size IS NULL OR batch_size > 0),
    ADD CONSTRAINT migrations_batch_config_check
        CHECK ((execution_mode = 'normal') OR (batch_by_attribute ~ '[^[:space:]]' AND batch_size IS NOT NULL AND batch_size > 0));
