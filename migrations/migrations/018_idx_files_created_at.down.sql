-- Rollback: Remove files(created_at) index.

DROP INDEX IF EXISTS idx_files_created_at;
