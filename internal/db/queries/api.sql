-- name: ListTasksByStatus :many
SELECT t.id, t.title, t.notes, t.priority, t.deadline, t.status,
       t.delegated_to, t.is_recurring, t.created_at,
       p.name AS project_name, p.color AS project_color
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE t.done_at IS NULL
ORDER BY t.priority, t.deadline NULLS LAST, t.created_at;

-- name: UpdateTaskStatus :exec
UPDATE tasks SET status = $2 WHERE id = $1;

-- name: GetTaskByID :one
SELECT t.id, t.title, t.notes, t.priority, t.deadline, t.status,
       t.delegated_to, t.is_recurring, t.created_at,
       p.name AS project_name, p.color AS project_color
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE t.id = $1;
