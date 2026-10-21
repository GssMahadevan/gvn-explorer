-- 0006_ts_millis: event and draft timestamps become unix milliseconds.
-- Second granularity cannot order events from the same second (a scripted
-- burst, a double-click) nor timestamp a draft save honestly; the audit
-- trail and the GUI's auto-save line deserve millisecond truth. The API
-- keeps its long-standing seconds fields ("ts", "updated_at", floored)
-- and adds exact ones ("ts_ms", "updated_at_ms"), so existing clients
-- and contract runs keep passing verbatim. fs mtimes are already exact
-- (RFC3339 mod_time); /fs/content and listings now also publish the
-- same instant as plain numbers ("mtime_ms", "mod_time_ms") — no new
-- columns needed for those.
--
-- Rows written before this migration hold seconds, so multiply them.
-- The comparison distinguishes the two units permanently: a seconds
-- timestamp stays below 1e11 until the year 5138, while a milliseconds
-- one is ~1.7e12 today — which also keeps the statement idempotent.
UPDATE events SET ts = ts * 1000 WHERE ts < 100000000000;
UPDATE file_drafts SET updated_at = updated_at * 1000 WHERE updated_at < 100000000000;
