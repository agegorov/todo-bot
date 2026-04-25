ALTER TABLE tasks ADD COLUMN status TEXT NOT NULL DEFAULT 'todo'
    CHECK (status IN ('todo', 'in_progress', 'done'));
