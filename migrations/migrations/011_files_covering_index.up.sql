-- Migration: Replace files(folder_id, filename) with covering index for hot gallery query.
-- Order is intentional: create new index first, then drop the old index.

CREATE INDEX IF NOT EXISTS idx_files_folder_id_filename_covering
    ON files (
       folder_id
     , filename
     , id
     , path_id
     , size_bytes
     , mtime
     , md5
     , phash
     , mime_type
     , width
     , height
     , created_at
     , updated_at
  );

DROP INDEX IF EXISTS idx_files_folder_id_filename;
