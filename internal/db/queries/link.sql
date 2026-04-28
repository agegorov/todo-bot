-- name: CreateLinkToken :one
INSERT INTO link_tokens (token, user_id)
VALUES ($1, $2)
RETURNING *;

-- name: GetLinkToken :one
SELECT lt.*, u.email, u.name
FROM link_tokens lt
JOIN users u ON u.id = lt.user_id
WHERE lt.token = $1 AND lt.expires_at > NOW();

-- name: DeleteLinkToken :exec
DELETE FROM link_tokens WHERE token = $1;

-- name: SetUserTelegramID :exec
UPDATE users SET telegram_id = $2 WHERE id = $1;

-- name: ClaimTasksByTelegram :exec
UPDATE tasks SET user_id = $1
WHERE telegram_user_id = $2 AND user_id IS NULL;

-- name: GetUserByTelegramID :one
SELECT * FROM users WHERE telegram_id = $1;
