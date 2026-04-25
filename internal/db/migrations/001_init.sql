CREATE TABLE projects (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    color      TEXT NOT NULL DEFAULT '#808080',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO projects (name, color) VALUES
    ('Inbox',    '#808080'),
    ('Work',     '#4A90D9'),
    ('Home',     '#7ED321'),
    ('Personal', '#F5A623');

CREATE TABLE tasks (
    id          BIGSERIAL PRIMARY KEY,
    project_id  BIGINT NOT NULL REFERENCES projects(id) ON DELETE SET NULL,
    title       TEXT NOT NULL,
    notes       TEXT,
    priority    SMALLINT NOT NULL DEFAULT 2, -- 1=high 2=medium 3=low
    deadline    TIMESTAMPTZ,
    done_at     TIMESTAMPTZ,
    delegated_to TEXT,
    is_recurring BOOLEAN NOT NULL DEFAULT FALSE,
    recur_rule  TEXT, -- cron expression
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE tags (
    id   BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE task_tags (
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    tag_id  BIGINT NOT NULL REFERENCES tags(id)  ON DELETE CASCADE,
    PRIMARY KEY (task_id, tag_id)
);

CREATE TABLE reminders (
    id         BIGSERIAL PRIMARY KEY,
    task_id    BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    remind_at  TIMESTAMPTZ NOT NULL,
    sent       BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX idx_tasks_deadline   ON tasks(deadline) WHERE done_at IS NULL;
CREATE INDEX idx_reminders_unsent ON reminders(remind_at) WHERE sent = FALSE;
