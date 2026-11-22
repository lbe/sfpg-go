-- name: InsertXMPProperty :exec
INSERT INTO xmp_properties (file_id, namespace, property, value)
VALUES (?, ?, ?, ?);

-- name: GetXMPPropertiesByFile :many
SELECT * FROM xmp_properties WHERE file_id = ?;

-- name: DeleteXMPProperties :exec
DELETE FROM xmp_properties WHERE file_id = ?;

-- name: UpsertXMPRaw :exec
INSERT INTO xmp_raw (file_id, raw_xml)
VALUES (?, ?)
ON CONFLICT(file_id) DO UPDATE SET raw_xml=excluded.raw_xml;

-- name: GetXMPRaw :one
SELECT * FROM xmp_raw WHERE file_id = ?;

-- name: DeleteXMPRaw :exec
DELETE FROM xmp_raw WHERE file_id = ?;
