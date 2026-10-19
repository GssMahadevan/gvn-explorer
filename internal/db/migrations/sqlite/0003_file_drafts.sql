-- 0003_file_drafts: the working copies behind the draft/publish edit model
-- (the YAML config editor). GUI edits are saved here per path; the file on
-- disk changes only when a draft is committed. One draft per path is the
-- whole concurrency story: two admins editing the same file share one
-- draft, last writer wins.
-- This table is the only place the server stores file *content* — the
-- deliberate exception to the "SQLite holds permissions only" rule.

CREATE TABLE IF NOT EXISTS file_drafts (
	path       TEXT PRIMARY KEY,  -- logical path under the root, normalized
	content    TEXT NOT NULL,     -- full working copy (never a diff)
	updated_by TEXT NOT NULL,     -- "tenant/username" snapshot of the last writer
	updated_at INTEGER NOT NULL   -- unix seconds
);
