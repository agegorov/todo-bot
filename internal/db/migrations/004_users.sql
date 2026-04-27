CREATE TABLE users (
    id         BIGSERIAL PRIMARY KEY,
    google_id  TEXT UNIQUE NOT NULL,
    email      TEXT NOT NULL,
    name       TEXT NOT NULL,
    avatar     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE sessions (
    token      TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sessions_expires ON sessions(expires_at);

-- Добавляем user_id к колонкам и задачам (nullable — существующие данные достанутся первому вошедшему)
ALTER TABLE board_columns ADD COLUMN user_id BIGINT REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE tasks         ADD COLUMN user_id BIGINT REFERENCES users(id) ON DELETE CASCADE;
