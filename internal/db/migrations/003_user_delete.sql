ALTER TABLE users ADD COLUMN deleted_at TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_users_deleted ON users(deleted_at);
