-- Rollback: Drop file_folder_index table and any stale rebuild leftovers.

DROP TABLE IF EXISTS file_folder_index_to_be_dropped;
DROP TABLE IF EXISTS file_folder_index_new;
DROP INDEX IF EXISTS idx_file_folder_index_folder_id;
DROP TABLE IF EXISTS file_folder_index;