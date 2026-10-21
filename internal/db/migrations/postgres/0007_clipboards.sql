-- 0007_clipboards: cross-device clipboards as link feeds — see the
-- sqlite file for the design; kept shape-identical (BIGINT ids, same
-- allow-list conventions) so the application layer does not change
-- per engine.

CREATE TABLE IF NOT EXISTS clipboards (
	id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	tenant_id  BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	mime       TEXT NOT NULL DEFAULT '*/*',
	created_by TEXT NOT NULL,
	created_at BIGINT NOT NULL,
	UNIQUE (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS clipboard_items (
	id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	clipboard_id BIGINT NOT NULL REFERENCES clipboards(id) ON DELETE CASCADE,
	path         TEXT NOT NULL,
	content_type TEXT NOT NULL,
	posted_by    TEXT NOT NULL,
	posted_at    BIGINT NOT NULL,
	expires_at   BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS clipboard_items_clip ON clipboard_items (clipboard_id, id);
