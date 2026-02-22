-- Rollback: Drop all tables and views
DROP VIEW IF EXISTS qc_folder_path_subset_file_path;
DROP VIEW IF EXISTS qc_file_path_subset_file_name;
DROP VIEW IF EXISTS folder_tile_exists_view;
DROP VIEW IF EXISTS thumbnail_exists_view;
DROP VIEW IF EXISTS file_view;
DROP VIEW IF EXISTS folder_view;

DROP TABLE IF EXISTS xmp_raw;
DROP TABLE IF EXISTS xmp_properties;
DROP TABLE IF EXISTS iptc_keywords;
DROP TABLE IF EXISTS iptc_metadata;
DROP TABLE IF EXISTS exif_metadata;
DROP TABLE IF EXISTS thumbnail_blobs;
DROP TABLE IF EXISTS thumbnails;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS folders;
DROP TABLE IF EXISTS folder_paths;
DROP TABLE IF EXISTS file_paths;
DROP TABLE IF EXISTS invalid_files;
DROP TABLE IF EXISTS login_attempts;
DROP TABLE IF EXISTS http_cache;
DROP TABLE IF EXISTS config;
