// Package db opens the SQLite database and owns the platform-independent
// permission schema: users, groups, memberships, per-path ACLs, sessions.
// The files themselves live on the real filesystem under the server's root.
package db

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	username   TEXT NOT NULL UNIQUE COLLATE NOCASE,
	pass_hash  TEXT NOT NULL,
	is_admin   INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS groups (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE COLLATE NOCASE
);

CREATE TABLE IF NOT EXISTS group_members (
	group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	user_id  INTEGER NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
	PRIMARY KEY (group_id, user_id)
);

-- Per-path ACL grants. path is the root-relative logical path ("/" = root).
CREATE TABLE IF NOT EXISTS perms (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	path           TEXT NOT NULL,
	principal_type TEXT NOT NULL CHECK (principal_type IN ('user','group')),
	principal_id   INTEGER NOT NULL,
	can_read       INTEGER NOT NULL DEFAULT 0,
	can_write      INTEGER NOT NULL DEFAULT 0,
	can_delete     INTEGER NOT NULL DEFAULT 0,
	created_at     TEXT NOT NULL,
	UNIQUE (path, principal_type, principal_id)
);

CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
);
`

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*sql.DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, err
	}
	// Single connection: correct for ":memory:" (per-connection DBs otherwise)
	// and avoids SQLITE_BUSY contention for file databases at this scale.
	conn.SetMaxOpenConns(1)
	return conn, nil
}
