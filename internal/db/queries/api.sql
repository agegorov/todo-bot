-- name: ListTasksForBoard :many
SELECT t.id, t.title, t.notes, t.priority, t.deadline,
       t.column_id, t.delegated_to, t.is_recurring, t.created_at,
       p.name AS project_name, p.color AS project_color,
       COALESCE(array_agg(tg.name ORDER BY tg.name) FILTER (WHERE tg.name IS NOT NULL), '{}') AS tags
FROM tasks t
JOIN projects p ON p.id = t.project_id
LEFT JOIN task_tags tt ON tt.task_id = t.id
LEFT JOIN tags tg ON tg.id = tt.tag_id
WHERE t.done_at IS NULL AND t.user_id = $1
GROUP BY t.id, p.name, p.color
ORDER BY t.priority, t.deadline NULLS LAST, t.created_at;

-- name: MoveTaskToColumn :exec
UPDATE tasks SET column_id = $2 WHERE id = $1 AND user_id = $3;

-- name: ListColumns :many
SELECT * FROM board_columns WHERE user_id = $1 ORDER BY position, id;

-- name: CreateColumn :one
INSERT INTO board_columns (name, color, position, user_id)
VALUES ($1, $2, (SELECT COALESCE(MAX(position),0)+1 FROM board_columns WHERE user_id = $3), $3)
RETURNING *;

-- name: UpdateColumn :exec
UPDATE board_columns SET name = $2, color = $3 WHERE id = $1 AND user_id = $4;

-- name: DeleteColumn :exec
DELETE FROM board_columns WHERE id = $1 AND user_id = $2;

-- name: ReorderColumns :exec
UPDATE board_columns SET position = $2 WHERE id = $1 AND user_id = $3;

-- name: UpdateTask :exec
UPDATE tasks SET title = $2, notes = $3, priority = $4, deadline = $5 WHERE id = $1 AND user_id = $6;

-- name: DeleteTaskTags :exec
DELETE FROM task_tags WHERE task_id = $1;
