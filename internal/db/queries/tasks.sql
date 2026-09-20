-- name: CreateTask :one
INSERT INTO tasks (project_id, title, notes, priority, deadline, delegated_to, is_recurring, recur_rule, user_id, telegram_user_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetTask :one
SELECT t.*, p.name AS project_name
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE t.id = $1;

-- Telegram-команды (/list, /today, /overdue) видят задачи привязанного веб-аккаунта
-- (user_id = @user_id), а до привязки — свои orphan-задачи (user_id IS NULL и тот же
-- telegram_user_id). Один запрос обслуживает оба случая.

-- name: ListOpenTasksForTelegram :many
SELECT t.*, p.name AS project_name
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE t.done_at IS NULL
  AND (
        (sqlc.narg('user_id')::bigint IS NOT NULL AND t.user_id = sqlc.narg('user_id'))
     OR (t.user_id IS NULL AND t.telegram_user_id = sqlc.arg('telegram_user_id'))
  )
ORDER BY t.priority, t.deadline NULLS LAST, t.created_at;

-- name: ListTodayTasksForTelegram :many
SELECT t.*, p.name AS project_name
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE t.done_at IS NULL
  AND t.deadline < NOW() + INTERVAL '24 hours'
  AND (
        (sqlc.narg('user_id')::bigint IS NOT NULL AND t.user_id = sqlc.narg('user_id'))
     OR (t.user_id IS NULL AND t.telegram_user_id = sqlc.arg('telegram_user_id'))
  )
ORDER BY t.deadline NULLS LAST;

-- name: ListOverdueTasksForTelegram :many
SELECT t.*, p.name AS project_name
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE t.done_at IS NULL
  AND t.deadline < NOW()
  AND (
        (sqlc.narg('user_id')::bigint IS NOT NULL AND t.user_id = sqlc.narg('user_id'))
     OR (t.user_id IS NULL AND t.telegram_user_id = sqlc.arg('telegram_user_id'))
  )
ORDER BY t.deadline;

-- Закрытие задачи непривязанным Telegram-пользователем: только его собственная
-- orphan-задача (иначе после снятия owner-гейта любой смог бы закрыть чужую по id).
-- :execrows — чтобы отличить "не найдена / не твоя" от успеха.

-- name: CompleteOrphanTaskForTelegram :execrows
UPDATE tasks SET done_at = NOW()
WHERE id = sqlc.arg('id')
  AND user_id IS NULL
  AND telegram_user_id = sqlc.arg('telegram_user_id')
  AND done_at IS NULL;

-- name: DeleteTask :exec
DELETE FROM tasks WHERE id = $1 AND (user_id = $2 OR user_id IS NULL);

-- name: ListProjects :many
SELECT * FROM projects ORDER BY id;
