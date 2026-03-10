-- Migration 012: Remove thumbnail_blobs from main DB (moved to thumbs.db)
DROP TABLE IF EXISTS thumbnail_blobs;
