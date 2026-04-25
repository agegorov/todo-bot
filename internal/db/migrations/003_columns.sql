CREATE TABLE board_columns (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    color      TEXT NOT NULL DEFAULT '#94a3b8',
    position   INT  NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO board_columns (name, color, position) VALUES
    ('To Do',       '#94a3b8', 0),
    ('In Progress', '#60a5fa', 1),
    ('Done',        '#4ade80', 2);

ALTER TABLE tasks
    ADD COLUMN column_id BIGINT REFERENCES board_columns(id) ON DELETE SET DEFAULT DEFAULT 1;

UPDATE tasks SET column_id = (SELECT id FROM board_columns WHERE name = 'To Do')       WHERE status = 'todo';
UPDATE tasks SET column_id = (SELECT id FROM board_columns WHERE name = 'In Progress') WHERE status = 'in_progress';
UPDATE tasks SET column_id = (SELECT id FROM board_columns WHERE name = 'Done')        WHERE status = 'done';

ALTER TABLE tasks ALTER COLUMN column_id SET NOT NULL;
ALTER TABLE tasks DROP COLUMN status;
