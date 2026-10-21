-- 0005_principal_types: two pseudo-principals join the grant model.
-- 'tenant' rows carry the tenant's id in principal_id and match "any
-- member of tenant N"; 'all' rows use the reserved id 0 (no user, group
-- or tenant has id 0) and match "any authenticated user". They exist to
-- keep tenant defaults and shared-zone access one short grant instead of
-- a row per member. Evaluation order: user > group > tenant > all at
-- each path level (internal/acl.Effective).
--
-- SQLite CHECK constraints cannot be altered in place, so this is the
-- standard rebuild dance: copy, swap, keep ids (grants are referenced by
-- id in audit rows — those must not shift).

CREATE TABLE perms_new (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	path           TEXT NOT NULL,
	principal_type TEXT NOT NULL CHECK (principal_type IN ('user','group','tenant','all')),
	principal_id   INTEGER NOT NULL,
	can_read       INTEGER NOT NULL DEFAULT 0,
	can_write      INTEGER NOT NULL DEFAULT 0,
	can_delete     INTEGER NOT NULL DEFAULT 0,
	created_at     TEXT NOT NULL,
	expires_at     INTEGER,
	UNIQUE (path, principal_type, principal_id)
);
INSERT INTO perms_new (id, path, principal_type, principal_id, can_read, can_write, can_delete, created_at, expires_at)
	SELECT id, path, principal_type, principal_id, can_read, can_write, can_delete, created_at, expires_at FROM perms;
DROP TABLE perms;
ALTER TABLE perms_new RENAME TO perms;
