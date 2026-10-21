-- 0004_pairings: short-lived join invites for device pairing.
-- An admin opens an invite (free-text description, optional preset
-- username, optional "also issue an API key" flag); the server generates
-- a 256-bit claim token (travels inside the QR code) plus a 7-character
-- short code (typed by hand where cameras are awkward). Both are stored
-- hashed — same model as auth_factors.secret: a stolen database never
-- holds a redeemable invite.
-- Redemption creates a plain (non-admin) account in the tenant and marks
-- the invite used: single-use, self-expiring (expires_at, unix seconds),
-- and revocable while still unused (revoked_at — the usual
-- suspend-rather-than-delete pattern, history stays queryable).

CREATE TABLE IF NOT EXISTS pairings (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
	token_hash      TEXT NOT NULL UNIQUE,     -- sha256 hex of the claim token
	code_hash       TEXT NOT NULL UNIQUE,     -- sha256 hex of the short code
	description     TEXT NOT NULL,            -- what the admin is pairing, e.g. "Mia's phone"
	username        TEXT,                     -- preset login; NULL = derived at join
	apikey          INTEGER NOT NULL DEFAULT 0, -- 1 = also issue an API key on join
	user_expires_at INTEGER,                  -- expiry for the *created account*; NULL = never
	created_by      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at      TEXT NOT NULL,            -- RFC3339 UTC (same convention as users)
	expires_at      INTEGER NOT NULL,         -- unix seconds; invite lifetime, default 10 min
	used_at         TEXT,                     -- redemption time; NULL = unused
	used_by         INTEGER REFERENCES users(id) ON DELETE SET NULL, -- who joined
	revoked_at      TEXT                      -- admin cancel; NULL = live
);
CREATE INDEX IF NOT EXISTS pairings_tenant_created ON pairings (tenant_id, created_at);
