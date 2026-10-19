-- 0001_init: the platform-independent permission schema as of the baseline.
-- Files themselves live on the real filesystem under the server's root;
-- this database only holds principals, memberships, per-path ACLs, auth
-- factors, sessions and the forensic event trail.

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
-- active: suspend-instead-of-delete kill switch; rows and history are kept.
CREATE TABLE IF NOT EXISTS users (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	tenant_id  INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
	username   TEXT NOT NULL COLLATE NOCASE,
	pass_hash  TEXT NOT NULL,
	is_admin   INTEGER NOT NULL DEFAULT 0,
	is_super   INTEGER NOT NULL DEFAULT 0,
	active     INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	expires_at INTEGER,
	UNIQUE (tenant_id, username)
);

CREATE TABLE IF NOT EXISTS groups (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
	name      TEXT NOT NULL COLLATE NOCASE,
	active    INTEGER NOT NULL DEFAULT 1,
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

-- Append-only forensic trail for every authz/authn DML. Deliberately has no
-- FKs: events must outlive the deletion of their actors and targets.
-- tenant_id scopes visibility for tenant admins (0 = platform-level or an
-- unauthenticated attempt against an unknown tenant).
CREATE TABLE IF NOT EXISTS events (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	ts          INTEGER NOT NULL,
	actor_id    INTEGER,
	actor       TEXT NOT NULL DEFAULT '',
	tenant_id   INTEGER NOT NULL DEFAULT 0,
	action      TEXT NOT NULL,
	target_type TEXT NOT NULL DEFAULT '',
	target_id   INTEGER NOT NULL DEFAULT 0,
	target_name TEXT NOT NULL DEFAULT '',
	path        TEXT NOT NULL DEFAULT '',
	details     TEXT NOT NULL DEFAULT '',
	ip          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_events_tenant_ts ON events (tenant_id, ts);

CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
);
