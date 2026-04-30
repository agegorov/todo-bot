-- name: ListTasksForBoard :many
SELECT t.id, t.title, t.notes, t.priority, t.deadline,
       t.column_id, t.delegated_to, t.is_recurring, t.created_at, t.done_at,
       p.name AS project_name, p.color AS project_color,
       COALESCE(array_agg(tg.name ORDER BY tg.name) FILTER (WHERE tg.name IS NOT NULL), '{}') AS tags
FROM tasks t
JOIN projects p ON p.id = t.project_id
LEFT JOIN task_tags tt ON tt.task_id = t.id
LEFT JOIN tags tg ON tg.id = tt.tag_id
WHERE t.user_id = $1
GROUP BY t.id, p.name, p.color
ORDER BY t.priority, t.deadline NULLS LAST, t.created_at;

-- name: MoveTaskToColumn :exec
UPDATE tasks SET
    column_id = $2,
    done_at = CASE
        WHEN $2 = (SELECT bc.id FROM board_columns bc WHERE bc.user_id = $3 AND bc.system_kind = 'done')
            THEN COALESCE(tasks.done_at, NOW())
        ELSE NULL
    END
WHERE tasks.id = $1 AND tasks.user_id = $3;

-- name: MoveOrphanTasksToColumn :exec
UPDATE tasks SET column_id = $1 WHERE user_id = $2 AND column_id IS NULL;

-- name: CompleteTaskForUser :exec
UPDATE tasks SET
    column_id = (SELECT bc.id FROM board_columns bc WHERE bc.user_id = $2 AND bc.system_kind = 'done'),
    done_at = COALESCE(tasks.done_at, NOW())
WHERE tasks.id = $1 AND tasks.user_id = $2;

-- name: ListColumns :many
SELECT * FROM board_columns WHERE user_id = $1 ORDER BY position, id;

-- name: CreateColumn :one
INSERT INTO board_columns (name, color, position, user_id)
VALUES ($1, $2, (SELECT COALESCE(MAX(position),0)+1 FROM board_columns WHERE user_id = $3), $3)
RETURNING *;

-- name: UpdateColumn :exec
UPDATE board_columns SET name = $2, color = $3 WHERE id = $1 AND user_id = $4;

-- name: DeleteColumn :exec
DELETE FROM board_columns WHERE id = $1 AND user_id = $2 AND system_kind IS NULL;

-- name: GetTodoColumn :one
SELECT * FROM board_columns WHERE user_id = $1 AND system_kind = 'todo' LIMIT 1;

-- name: GetDoneColumn :one
SELECT * FROM board_columns WHERE user_id = $1 AND system_kind = 'done' LIMIT 1;

-- name: EnsureTodoColumn :one
INSERT INTO board_columns (name, color, position, user_id, system_kind)
VALUES ('📥 TO DO', '#60a5fa', 0, $1, 'todo')
ON CONFLICT (user_id, system_kind) WHERE system_kind IS NOT NULL
DO UPDATE SET name = board_columns.name
RETURNING *;

-- name: EnsureDoneColumn :one
INSERT INTO board_columns (name, color, position, user_id, system_kind)
VALUES ('✅ Done', '#4ade80',
        (SELECT COALESCE(MAX(position),0)+1 FROM board_columns WHERE user_id = $1),
        $1, 'done')
ON CONFLICT (user_id, system_kind) WHERE system_kind IS NOT NULL
DO UPDATE SET name = board_columns.name
RETURNING *;

-- name: ReorderColumns :exec
UPDATE board_columns SET position = $2 WHERE id = $1 AND user_id = $3;

-- name: UpdateTask :exec
UPDATE tasks SET title = $2, notes = $3, priority = $4, deadline = $5 WHERE id = $1 AND user_id = $6;

-- name: DeleteTaskTags :exec
DELETE FROM task_tags WHERE task_id = $1;
