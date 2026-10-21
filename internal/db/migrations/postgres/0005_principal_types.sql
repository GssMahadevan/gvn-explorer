-- 0005_principal_types: mirror of the sqlite migration of the same number.
-- 'tenant' (principal_id = tenant id) and 'all' (reserved id 0) extend the
-- grant model; see the sqlite file for the reasoning. Postgres can edit
-- CHECK constraints in place, so no table rebuild is needed.

ALTER TABLE perms DROP CONSTRAINT perms_principal_type_check;
ALTER TABLE perms ADD CONSTRAINT perms_principal_type_check CHECK (principal_type IN ('user','group','tenant','all'));
