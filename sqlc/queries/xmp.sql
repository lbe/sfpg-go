-- name: UpsertXMPProperty :exec
INSERT INTO xmp_properties (id, file_id, namespace, property, value)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  file_id=excluded.file_id,
  namespace=excluded.namespace,
  property=excluded.property,
  value=excluded.value;

-- name: GetXMPPropertiesByFile :many
SELECT * FROM xmp_properties WHERE file_id = ?;

-- name: DeleteXMPProperty :exec
DELETE FROM xmp_properties WHERE id = ?;

-- name: UpsertXMPRaw :exec
INSERT INTO xmp_raw (file_id, raw_xml)
VALUES (?, ?)
ON CONFLICT(file_id) DO UPDATE SET raw_xml=excluded.raw_xml;

-- name: GetXMPRaw :one
SELECT * FROM xmp_raw WHERE file_id = ?;

-- name: DeleteXMPRaw :exec
DELETE FROM xmp_raw WHERE file_id = ?;
