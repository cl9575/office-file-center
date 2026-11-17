ALTER TABLE users ADD COLUMN is_owner INTEGER NOT NULL DEFAULT 0;

UPDATE users SET is_owner = 1 WHERE id = (
    SELECT id FROM users
    WHERE role = 'admin'
    ORDER BY disabled ASC, created_at ASC, id ASC
    LIMIT 1
);

CREATE UNIQUE INDEX ux_users_owner ON users(is_owner) WHERE is_owner = 1;
