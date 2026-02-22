-- name: UpsertIPTC :exec
INSERT INTO iptc_metadata (file_id, title, description, keywords, creator,
                           copyright, credit, source, created_date)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(file_id) DO UPDATE SET
  title=excluded.title,
  description=excluded.description,
  keywords=excluded.keywords,
  creator=excluded.creator,
  copyright=excluded.copyright,
  credit=excluded.credit,
  source=excluded.source,
  created_date=excluded.created_date;

-- name: GetIPTCByFile :one
SELECT * FROM iptc_metadata WHERE file_id = ?;

-- name: DeleteIPTC :exec
DELETE FROM iptc_metadata WHERE file_id = ?;

-- name: InsertIPTCKeyword :exec
INSERT INTO iptc_keywords (id, file_id, keyword)
VALUES (?, ?, ?);

-- name: GetIPTCKeywords :many
SELECT * FROM iptc_keywords WHERE file_id = ?;

-- name: DeleteIPTCKeyword :exec
DELETE FROM iptc_keywords WHERE id = ?;
