-- Migration: Create and backfill file_folder_index table.
-- Replaces per-request window-function queries over file_view with a
-- materialized table rebuilt after each discovery run.

CREATE TABLE file_folder_index (
    file_id     INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    folder_id   INTEGER NOT NULL,
    image_index INTEGER NOT NULL,
    image_count INTEGER NOT NULL,
    prev_id     INTEGER,
    next_id     INTEGER,
    first_id    INTEGER NOT NULL,
    last_id     INTEGER NOT NULL
);

CREATE INDEX idx_file_folder_index_folder_id ON file_folder_index(folder_id);

-- Exclude orphan files (folder_id IS NULL) because the table requires a folder.
INSERT INTO file_folder_index
    (file_id, folder_id, image_index, image_count, prev_id, next_id, first_id, last_id)
SELECT fv.id
     , fv.folder_id
     , CAST(ROW_NUMBER() OVER (PARTITION BY fv.folder_id ORDER BY fv.filename, fv.id) AS INTEGER)
     , CAST(COUNT(*) OVER (PARTITION BY fv.folder_id) AS INTEGER)
     , CAST(LAG(fv.id) OVER (PARTITION BY fv.folder_id ORDER BY fv.filename, fv.id) AS INTEGER)
     , CAST(LEAD(fv.id) OVER (PARTITION BY fv.folder_id ORDER BY fv.filename, fv.id) AS INTEGER)
     , CAST(FIRST_VALUE(fv.id) OVER (PARTITION BY fv.folder_id ORDER BY fv.filename, fv.id) AS INTEGER)
     , CAST(LAST_VALUE(fv.id) OVER (
         PARTITION BY fv.folder_id ORDER BY fv.filename, fv.id
         ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
       ) AS INTEGER)
  FROM file_view fv
 WHERE fv.folder_id IS NOT NULL;