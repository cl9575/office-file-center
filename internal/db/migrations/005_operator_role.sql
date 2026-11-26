CREATE TABLE users_new (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE,
    display_name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'operator', 'editor', 'viewer')),
    must_change_password INTEGER NOT NULL DEFAULT 0,
    disabled INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    is_owner INTEGER NOT NULL DEFAULT 0,
    deleted_at TEXT NOT NULL DEFAULT '',
    first_login INTEGER NOT NULL DEFAULT 0
);

INSERT INTO users_new
    (id, username, display_name, password_hash, role, must_change_password, disabled,
     created_at, updated_at, created_by, is_owner, deleted_at, first_login)
SELECT
    id, username, display_name, password_hash, role, must_change_password, disabled,
    created_at, updated_at, created_by, is_owner, deleted_at, first_login
FROM users;

DROP TABLE sessions;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE UNIQUE INDEX ux_users_owner ON users(is_owner) WHERE is_owner = 1;
CREATE INDEX idx_users_deleted ON users(deleted_at);
