-- 0003_file_drafts: working copies for the draft/publish edit model,
-- shape-identical to the sqlite migration (see its comments for intent).

CREATE TABLE IF NOT EXISTS file_drafts (
	path       TEXT PRIMARY KEY,
	content    TEXT NOT NULL,
	updated_by TEXT NOT NULL,
	updated_at BIGINT NOT NULL
);
