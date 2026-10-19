-- 0001_init: Postgres starting point for the permission schema, kept
-- deliberately shape-identical to the sqlite baseline so the application
-- layer does not change when this engine is wired up:
--   * ids are BIGINT (Go scans int64); DB.Insert appends "RETURNING id".
--   * created_at/expires_at keep the app's own formats (RFC3339 text, unix
--     seconds) instead of TIMESTAMPTZ, so query predicates stay identical.
--   * booleans stay 0/1 integers (same b2i helpers) until a real pg rollout
--     can flip columns and Go types together.
-- Remaining work for the pg path (tracked in README "Migrations"):
--   * case-insensitive matching: this schema enforces it with LOWER() unique
--     indexes; call sites must compare LOWER(col) = LOWER(?) (or the citext
--     extension may be enabled by an operator with suitable privileges).
--   * the migration runner sends whole files per Exec; pgx's extended
--     protocol needs one statement per exec — add splitting when wiring pg.

CREATE TABLE IF NOT EXISTS tenants (
	id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	name       TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tenants_name ON tenants (LOWER(name));

CREATE TABLE IF NOT EXISTS users (
	id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	tenant_id  BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
	username   TEXT NOT NULL,
	pass_hash  TEXT NOT NULL,
	is_admin   INTEGER NOT NULL DEFAULT 0,
	is_super   INTEGER NOT NULL DEFAULT 0,
	active     INTEGER NOT NULL DEFAULT 1,
	description TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	expires_at BIGINT
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_tenant_username ON users (tenant_id, LOWER(username));

CREATE TABLE IF NOT EXISTS groups (
	id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	tenant_id  BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	active     INTEGER NOT NULL DEFAULT 1,
	description TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (id)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_groups_tenant_name ON groups (tenant_id, LOWER(name));

CREATE TABLE IF NOT EXISTS group_members (
	group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	user_id  BIGINT NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
	PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS perms (
	id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	path           TEXT NOT NULL,
	principal_type TEXT NOT NULL CHECK (principal_type IN ('user','group')),
	principal_id   BIGINT NOT NULL,
	can_read       INTEGER NOT NULL DEFAULT 0,
	can_write      INTEGER NOT NULL DEFAULT 0,
	can_delete     INTEGER NOT NULL DEFAULT 0,
	created_at     TEXT NOT NULL,
	expires_at     BIGINT,
	UNIQUE (path, principal_type, principal_id)
);

CREATE TABLE IF NOT EXISTS auth_factors (
	id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	method       TEXT NOT NULL CHECK (method IN ('apikey','ed25519','oauth2')),
	alg          TEXT NOT NULL DEFAULT '',
	label        TEXT NOT NULL DEFAULT '',
	secret       TEXT NOT NULL,
	created_at   TEXT NOT NULL,
	expires_at   BIGINT,
	last_used_at BIGINT,
	UNIQUE (user_id, method, label)
);
CREATE INDEX IF NOT EXISTS idx_factors_lookup ON auth_factors (method, secret);

CREATE TABLE IF NOT EXISTS events (
	id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	ts          BIGINT NOT NULL,
	actor_id    BIGINT,
	actor       TEXT NOT NULL DEFAULT '',
	tenant_id   BIGINT NOT NULL DEFAULT 0,
	action      TEXT NOT NULL,
	target_type TEXT NOT NULL DEFAULT '',
	target_id   BIGINT NOT NULL DEFAULT 0,
	target_name TEXT NOT NULL DEFAULT '',
	path        TEXT NOT NULL DEFAULT '',
	details     TEXT NOT NULL DEFAULT '',
	ip          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_events_tenant_ts ON events (tenant_id, ts);

CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
);
