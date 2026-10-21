-- 0004_pairings: short-lived join invites for device pairing — see the
-- sqlite file for the design; kept shape-identical (app-level formats,
-- BIGINT ids, hash-only secrets) so the application layer does not change
-- per engine.

CREATE TABLE IF NOT EXISTS pairings (
	id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	tenant_id       BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
	token_hash      TEXT NOT NULL UNIQUE,
	code_hash       TEXT NOT NULL UNIQUE,
	description     TEXT NOT NULL,
	username        TEXT,
	apikey          INTEGER NOT NULL DEFAULT 0,
	user_expires_at BIGINT,
	created_by      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at      TEXT NOT NULL,
	expires_at      BIGINT NOT NULL,
	used_at         TEXT,
	used_by         BIGINT REFERENCES users(id) ON DELETE SET NULL,
	revoked_at      TEXT
);
CREATE INDEX IF NOT EXISTS pairings_tenant_created ON pairings (tenant_id, created_at);
