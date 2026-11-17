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
			username TEXT NOT NULL,
			role TEXT NOT NULL,
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`INSERT INTO schema_migrations(name, applied_at) VALUES('001_init.sql', datetime('now'))`,
		`INSERT INTO users(id, username, role, disabled, created_at) VALUES
			('u1', 'firstadmin', 'admin', 0, '2026-01-01T00:00:00.000Z'),
			('u2', 'secondadmin', 'admin', 0, '2026-02-01T00:00:00.000Z'),
			('u3', 'editor', 'editor', 0, '2026-03-01T00:00:00.000Z')`,
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
}
