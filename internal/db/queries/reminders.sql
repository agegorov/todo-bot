-- name: CreateReminder :one
INSERT INTO reminders (task_id, remind_at)
VALUES ($1, $2)
RETURNING *;

-- chat_id — куда слать напоминание в Telegram: приоритет привязанному веб-юзеру
-- (users.telegram_id), иначе telegram_user_id самой задачи (создана ботом, ещё не
-- привязана). NULL — у задачи нет телеграм-получателя (создана только через веб).
-- name: ListDueReminders :many
SELECT r.*, t.title AS task_title, t.deadline AS task_deadline,
       COALESCE(u.telegram_id, t.telegram_user_id) AS chat_id
FROM reminders r
JOIN tasks t ON t.id = r.task_id
LEFT JOIN users u ON u.id = t.user_id
WHERE r.sent = FALSE AND r.remind_at <= NOW();

-- name: MarkReminderSent :exec
UPDATE reminders SET sent = TRUE WHERE id = $1;

-- Для еженедельного дайджеста: по одной строке на каждого привязанного к Telegram
-- пользователя с числом его просроченных задач (0, если их нет).
-- name: ListLinkedUsersOverdueCounts :many
SELECT u.telegram_id,
       COUNT(t.id) FILTER (WHERE t.done_at IS NULL AND t.deadline < NOW()) AS overdue_count
FROM users u
LEFT JOIN tasks t ON t.user_id = u.id
WHERE u.telegram_id IS NOT NULL
GROUP BY u.telegram_id;
