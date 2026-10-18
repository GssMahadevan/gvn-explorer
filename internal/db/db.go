// Package db opens the SQLite database and owns the platform-independent
// permission schema: users, groups, memberships, per-path ACLs, sessions.
// The files themselves live on the real filesystem under the server's root.
package db

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS tenants (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
	created_at TEXT NOT NULL
);

-- usernames and group names are unique per tenant, not globally.
-- is_admin: tenant admin (manages own tenant's users/groups/grants, no file bypass)
-- is_super: platform super admin (tenant CRUD, cross-tenant management, ACL bypass)
-- expires_at: unix seconds; NULL = never. Expired accounts cannot log in and
-- live sessions stop working (checked on every request, no cron needed).
CREATE TABLE IF NOT EXISTS users (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	tenant_id  INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
	username   TEXT NOT NULL COLLATE NOCASE,
	pass_hash  TEXT NOT NULL,
	is_admin   INTEGER NOT NULL DEFAULT 0,
	is_super   INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	expires_at INTEGER,
	UNIQUE (tenant_id, username)
);

CREATE TABLE IF NOT EXISTS groups (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
	name      TEXT NOT NULL COLLATE NOCASE,
	UNIQUE (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS group_members (
	group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	user_id  INTEGER NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
	PRIMARY KEY (group_id, user_id)
);

-- Per-path ACL grants. path is the root-relative logical path ("/" = root).
-- principal_id is the rowid of a users/groups row, which carries the tenant,
-- so the same name in two tenants is two distinct grant subjects.
-- expires_at: unix seconds; NULL = never. Expired grants are ignored by the
-- inheritance engine, so a "3 day share" is just a read grant with an expiry.
CREATE TABLE IF NOT EXISTS perms (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	path           TEXT NOT NULL,
	principal_type TEXT NOT NULL CHECK (principal_type IN ('user','group')),
	principal_id   INTEGER NOT NULL,
	can_read       INTEGER NOT NULL DEFAULT 0,
	can_write      INTEGER NOT NULL DEFAULT 0,
	can_delete     INTEGER NOT NULL DEFAULT 0,
	created_at     TEXT NOT NULL,
	expires_at     INTEGER,
	UNIQUE (path, principal_type, principal_id)
);

-- Alternate sign-in methods, one row per (user, method, label). Designed so
-- future methods slot in without schema change: 'apikey' (live), 'ed25519'
-- (device-signed challenges) and 'oauth2' (alg = provider: google, entra,
-- github...) are reserved. secret: apikey = hex sha256(key), ed25519 = base64
-- pubkey, oauth2 = provider subject id. label lets two apikeys coexist while
-- rotating keys during a firmware rollout.
CREATE TABLE IF NOT EXISTS auth_factors (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	method       TEXT NOT NULL CHECK (method IN ('apikey','ed25519','oauth2')),
	alg          TEXT NOT NULL DEFAULT '',
	label        TEXT NOT NULL DEFAULT '',
	secret       TEXT NOT NULL,
	created_at   TEXT NOT NULL,
	expires_at   INTEGER,
	last_used_at INTEGER,
	UNIQUE (user_id, method, label)
);
CREATE INDEX IF NOT EXISTS idx_factors_lookup ON auth_factors (method, secret);

CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
);
`

// Open opens (creating if needed) the database at path and applies the schema.
// Additive changes migrate in place; the tenant schema itself is not backward
// compatible with pre-tenant databases — delete those and let Open recreate.
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
	// Additive migrations for databases created before expiring access.
	for _, stmt := range []string{
		"ALTER TABLE users ADD COLUMN expires_at INTEGER",
		"ALTER TABLE perms ADD COLUMN expires_at INTEGER",
	} {
		if _, err := conn.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			conn.Close()
			return nil, err
		}
	}
	// Single connection: correct for ":memory:" (per-connection DBs otherwise)
	// and avoids SQLITE_BUSY contention for file databases at this scale.
	conn.SetMaxOpenConns(1)
	return conn, nil
}
