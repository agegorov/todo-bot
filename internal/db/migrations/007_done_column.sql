-- Заменяем булевый is_system на system_kind: NULL | 'todo' | 'done'
ALTER TABLE board_columns ADD COLUMN system_kind TEXT
    CHECK (system_kind IN ('todo', 'done'));

-- Существующие системные колонки становятся todo
UPDATE board_columns SET system_kind = 'todo' WHERE is_system = TRUE;

-- Старый индекс/колонка больше не нужны
DROP INDEX IF EXISTS idx_one_system_column_per_user;
ALTER TABLE board_columns DROP COLUMN is_system;

-- Уникальность: одна колонка каждого system_kind на пользователя
CREATE UNIQUE INDEX idx_unique_system_kind_per_user
ON board_columns (user_id, system_kind) WHERE system_kind IS NOT NULL;

-- Создаём Done колонку для всех существующих пользователей
INSERT INTO board_columns (name, color, position, user_id, system_kind)
SELECT '✅ Done', '#4ade80',
       (SELECT COALESCE(MAX(position),0)+1 FROM board_columns bc2 WHERE bc2.user_id = u.id),
       u.id, 'done'
FROM users u
WHERE NOT EXISTS (
    SELECT 1 FROM board_columns WHERE user_id = u.id AND system_kind = 'done'
);
