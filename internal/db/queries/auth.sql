-- name: UpsertUser :one
INSERT INTO users (google_id, email, name, avatar)
VALUES ($1, $2, $3, $4)
ON CONFLICT (google_id) DO UPDATE
    SET email  = EXCLUDED.email,
        name   = EXCLUDED.name,
        avatar = EXCLUDED.avatar
RETURNING *;

-- name: CreateSession :one
INSERT INTO sessions (token, user_id, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetSession :one
SELECT s.*, u.email, u.name, u.avatar
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token = $1 AND s.expires_at > NOW();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = $1;

-- name: CleanExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < NOW();

-- name: ClaimOrphanData :exec
UPDATE board_columns SET user_id = $1 WHERE user_id IS NULL;

-- name: ClaimOrphanTasks :exec
UPDATE tasks SET user_id = $1 WHERE user_id IS NULL;
