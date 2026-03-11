-- name: SetModuleState :exec
INSERT INTO module_state (name, is_active, last_started_at, last_finished_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
  is_active = excluded.is_active,
  last_started_at = COALESCE(excluded.last_started_at, module_state.last_started_at),
  last_finished_at = COALESCE(excluded.last_finished_at, module_state.last_finished_at);

-- name: GetModuleState :one
SELECT name, is_active, last_started_at, last_finished_at
FROM module_state
WHERE name = ?;
