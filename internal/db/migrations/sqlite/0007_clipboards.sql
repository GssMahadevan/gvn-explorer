-- 0007_clipboards: cross-device clipboards as link feeds.
-- A clipboard is a name inside a tenant that holds links to files —
-- not the bytes themselves. Posting a link requires read on the target
-- at post time; opening one re-checks the target's ACL every time, so
-- a video shared once follows its file's permissions forever, and a
-- deleted file shows as a dead link rather than a phantom copy.
--
-- "mime" is what a device (the thin Android app, a script) reads to
-- know how to treat the feed: a small explicit allow-list of base
-- media types, never a free-form type — same policy as the inline
-- preview allow-lists. Items carry their own content_type, classified
-- from the target's extension at post time, so mixed feeds stay
-- self-describing.
--
-- Items expire (default now+24h, server-wide knob) and are purged
-- lazily when a feed is read — no cron, no sweeper. Timestamps are
-- unix milliseconds, matching 0006.

CREATE TABLE IF NOT EXISTS clipboards (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	tenant_id  INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,                -- one per tenant; appears as a directory name under /tenants/{T}/clipboards
	mime       TEXT NOT NULL DEFAULT '*/*',  -- declared kind: text/plain image/* video/* audio/* application/pdf */*
	created_by TEXT NOT NULL,               -- username; kept after suspend (same display rule as drafts)
	created_at INTEGER NOT NULL,            -- unix ms
	UNIQUE (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS clipboard_items (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	clipboard_id INTEGER NOT NULL REFERENCES clipboards(id) ON DELETE CASCADE,
	path         TEXT NOT NULL,             -- target file path at post time; re-resolved on every read (renames break the link — dead, honest)
	content_type TEXT NOT NULL,            -- classified from extension at post time
	posted_by    TEXT NOT NULL,            -- username
	posted_at    INTEGER NOT NULL,         -- unix ms
	expires_at   INTEGER NOT NULL          -- unix ms; lazy purge on read
);
CREATE INDEX IF NOT EXISTS clipboard_items_clip ON clipboard_items (clipboard_id, id);
