-- Системная колонка (например TO DO) — её нельзя удалять, в неё попадают новые задачи
ALTER TABLE board_columns ADD COLUMN is_system BOOLEAN NOT NULL DEFAULT FALSE;

-- Гарантируем что у каждого пользователя ровно одна системная колонка:
-- первая по позиции становится системной
UPDATE board_columns SET is_system = TRUE
WHERE id IN (
    SELECT DISTINCT ON (user_id) id
    FROM board_columns
    WHERE user_id IS NOT NULL
    ORDER BY user_id, position, id
);

-- Тем пользователям, у кого нет ни одной колонки — создаём системную
INSERT INTO board_columns (name, color, position, user_id, is_system)
SELECT '📥 TO DO', '#60a5fa', 0, u.id, TRUE
FROM users u
WHERE NOT EXISTS (SELECT 1 FROM board_columns WHERE user_id = u.id);

-- Уникальность: одна системная колонка на пользователя
CREATE UNIQUE INDEX idx_one_system_column_per_user
ON board_columns (user_id) WHERE is_system = TRUE;
