-- name: CreateReminder :one
INSERT INTO reminders (task_id, remind_at)
VALUES ($1, $2)
RETURNING *;

-- name: ListDueReminders :many
SELECT r.*, t.title AS task_title, t.deadline AS task_deadline
FROM reminders r
JOIN tasks t ON t.id = r.task_id
WHERE r.sent = FALSE AND r.remind_at <= NOW();

-- name: MarkReminderSent :exec
UPDATE reminders SET sent = TRUE WHERE id = $1;
