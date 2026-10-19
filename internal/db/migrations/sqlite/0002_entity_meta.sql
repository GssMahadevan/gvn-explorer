-- 0002_entity_meta: human context and kill switches for every entity kind.
-- description: free text for operators ("customer acme vault", "build
-- agents", "Q3 contractor account"). Shown and searchable in list UIs.
-- tenants.active: tenant-level suspend-instead-of-delete, same semantics as
-- users.active/groups.active: rows and history are kept, but logins, live
-- sessions and API keys of that tenant stop working.

ALTER TABLE tenants ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN active INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE groups ADD COLUMN description TEXT NOT NULL DEFAULT '';
