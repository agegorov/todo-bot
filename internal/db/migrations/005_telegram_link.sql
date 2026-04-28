-- Привязка Telegram аккаунта к веб-пользователю
ALTER TABLE users ADD COLUMN telegram_id BIGINT UNIQUE;

-- Задачи из бота хранят telegram_user_id вместо NULL
ALTER TABLE tasks ADD COLUMN telegram_user_id BIGINT;

-- Одноразовые токены для привязки
CREATE TABLE link_tokens (
    token      TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '15 minutes',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
