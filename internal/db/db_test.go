package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"lanfile/internal/db"

	_ "modernc.org/sqlite"
)

func TestOwnerMigrationBackfillsEarliestAdmin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			display_name TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
			must_change_password INTEGER NOT NULL DEFAULT 0,
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			created_by TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE sessions (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX idx_sessions_user ON sessions(user_id)`,
		`CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			parent_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL COLLATE NOCASE,
			kind TEXT NOT NULL CHECK (kind IN ('file', 'folder')),
			current_version_id TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 0,
			deleted_at TEXT,
			deleted_by TEXT,
			delete_batch TEXT,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			updated_by TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX ux_nodes_active_name ON nodes(parent_id, name) WHERE deleted_at IS NULL`,
		`CREATE INDEX idx_nodes_parent ON nodes(parent_id) WHERE deleted_at IS NULL`,
		`CREATE INDEX idx_nodes_deleted ON nodes(delete_batch)`,
		`CREATE TABLE audit (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			actor_name TEXT NOT NULL,
			action TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id TEXT NOT NULL DEFAULT '',
			target_name TEXT NOT NULL DEFAULT '',
			target_path TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL,
			ip TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`INSERT INTO schema_migrations(name, applied_at) VALUES('001_init.sql', datetime('now'))`,
		`INSERT INTO users(id, username, display_name, password_hash, role, disabled, created_at, updated_at) VALUES
			('u1', 'firstadmin', '第一管理员', 'hash', 'admin', 0, '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
			('u2', 'secondadmin', '第二管理员', 'hash', 'admin', 0, '2026-02-01T00:00:00.000Z', '2026-02-01T00:00:00.000Z'),
			('u3', 'editor', '编辑者', 'hash', 'editor', 0, '2026-03-01T00:00:00.000Z', '2026-03-01T00:00:00.000Z')`,
		`INSERT INTO sessions(token_hash, user_id, created_at, expires_at) VALUES('token', 'u1', '2026-01-01T00:00:00.000Z', '2099-01-01T00:00:00.000Z')`,
	}
	for _, statement := range statements {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var count int
	if err := database.QueryRow(`SELECT COUNT(1) FROM users WHERE is_owner = 1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one owner, got %d", count)
	}
	var owner string
	if err := database.QueryRow(`SELECT username FROM users WHERE is_owner = 1`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "firstadmin" {
		t.Fatalf("expected earliest admin as owner, got %s", owner)
	}
	var sessions int
	if err := database.QueryRow(`SELECT COUNT(1) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("role migration should clear sessions, got %d", sessions)
	}
	if _, err := database.Exec(`INSERT INTO users
		(id, username, display_name, password_hash, role, created_at, updated_at)
		VALUES('u4', 'operator', '操作员', 'hash', 'operator', '2026-04-01T00:00:00.000Z', '2026-04-01T00:00:00.000Z')`); err != nil {
		t.Fatalf("operator role should be accepted: %v", err)
	}
	var rootCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM nodes WHERE space = 'secure' AND parent_id = '' AND name = '权限区'`).Scan(&rootCount); err != nil {
		t.Fatal(err)
	}
	if rootCount != 1 {
		t.Fatalf("expected one secure zone root, got %d", rootCount)
	}
}
