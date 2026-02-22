-- queries for HTTP cache table operations
-- name: GetHttpCacheByKey :one
SELECT * FROM http_cache 
WHERE key = ? AND (expires_at IS NULL OR expires_at > unixepoch())
LIMIT 1;

-- name: UpsertHttpCache :exec
INSERT INTO http_cache (
  key, method, path, query_string, encoding, status, 
  content_type, content_encoding, cache_control, etag, last_modified, vary, 
  body, content_length, created_at, expires_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET 
  status=excluded.status,
  body=excluded.body,
  content_length=excluded.content_length,
  expires_at=excluded.expires_at;

-- name: DeleteHttpCacheByKey :exec
DELETE FROM http_cache WHERE key = ?;

-- name: DeleteHttpCacheExpired :exec
DELETE FROM http_cache 
WHERE expires_at IS NOT NULL AND expires_at <= unixepoch();

-- name: GetHttpCacheSizeBytes :one
SELECT COALESCE(SUM(content_length), 0) as total_bytes FROM http_cache;

-- name: GetHttpCacheOldestCreated :many
SELECT id, created_at, content_length FROM http_cache 
ORDER BY created_at ASC 
LIMIT ?;

-- name: DeleteHttpCacheByID :exec
DELETE FROM http_cache WHERE id = ?;

-- name: ClearHttpCache :exec
DELETE FROM http_cache;

-- name: CountHttpCacheEntries :one
SELECT COUNT(*) as count FROM http_cache;

-- name: HttpCacheExistsByKey :one
SELECT EXISTS(
  SELECT 1 FROM http_cache 
  WHERE key = ? AND (expires_at IS NULL OR expires_at > unixepoch())
) AS found;
