-- name: CreateTask :one
INSERT INTO tasks (project_id, title, notes, priority, deadline, delegated_to, is_recurring, recur_rule)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetTask :one
SELECT t.*, p.name AS project_name
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE t.id = $1;

-- name: ListOpenTasks :many
SELECT t.*, p.name AS project_name
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE t.done_at IS NULL
ORDER BY t.priority, t.deadline NULLS LAST, t.created_at;

-- name: ListTodayTasks :many
SELECT t.*, p.name AS project_name
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE t.done_at IS NULL
  AND t.deadline < NOW() + INTERVAL '24 hours'
ORDER BY t.deadline NULLS LAST;

-- name: ListOverdueTasks :many
SELECT t.*, p.name AS project_name
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE t.done_at IS NULL
  AND t.deadline < NOW()
ORDER BY t.deadline;

-- name: CompleteTask :exec
UPDATE tasks SET done_at = NOW() WHERE id = $1;

-- name: DeleteTask :exec
DELETE FROM tasks WHERE id = $1;

-- name: ListProjects :many
SELECT * FROM projects ORDER BY id;
