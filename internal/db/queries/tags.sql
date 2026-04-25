-- name: UpsertTag :one
INSERT INTO tags (name) VALUES ($1)
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING *;

-- name: AttachTag :exec
INSERT INTO task_tags (task_id, tag_id) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: GetTaskTags :many
SELECT tg.name FROM tags tg
JOIN task_tags tt ON tt.tag_id = tg.id
WHERE tt.task_id = $1;
