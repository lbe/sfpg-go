-- name: UpsertConfigValueOnly :exec
INSERT INTO config (key, value, created_at, updated_at)
VALUES (?, ?, ?, ?)
    ON CONFLICT(key) 
    DO UPDATE SET value=excluded.value
                , updated_at=excluded.updated_at   
            WHERE value IS NOT excluded.value;

-- name: GetConfigValueByKey :one
SELECT value 
  FROM config
 WHERE key = ?;

-- name: GetConfigs :many
SELECT "key", value, created_at, updated_at, type, category, requires_restart, description, default_value, help_text, example_value
  FROM config;

