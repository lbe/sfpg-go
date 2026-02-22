-- name: GetLoginAttempt :one
SELECT username, failed_attempts, locked_until, last_attempt_at
FROM login_attempts
WHERE username = ?;

-- name: UpsertLoginAttempt :exec
INSERT INTO login_attempts (username, failed_attempts, last_attempt_at, locked_until)
VALUES (?, ?, ?, ?)
ON CONFLICT(username) DO UPDATE SET
  failed_attempts = excluded.failed_attempts,
  last_attempt_at = excluded.last_attempt_at,
  locked_until = excluded.locked_until;

-- name: ClearLoginAttempts :exec
DELETE FROM login_attempts WHERE username = ?;

-- name: UnlockAccount :exec
UPDATE login_attempts
SET failed_attempts = 0, locked_until = NULL
WHERE username = ?;

