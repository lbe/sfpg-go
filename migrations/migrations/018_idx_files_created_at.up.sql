-- Migration: Add index on files(created_at) for efficient time-based queries.

CREATE INDEX IF NOT EXISTS idx_files_created_at
    ON files(created_at);
