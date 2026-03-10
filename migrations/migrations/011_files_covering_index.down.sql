-- Rollback: restore files(folder_id, filename) index and remove covering index.
-- Order is intentional: recreate old index first, then drop the covering index.

CREATE INDEX idx_files_folder_id_filename
    ON files(folder_id, filename);

DROP INDEX IF EXISTS idx_files_folder_id_filename_covering;
