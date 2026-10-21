-- 0006_ts_millis: event and draft timestamps become unix milliseconds
-- (see the sqlite twin for the full rationale). Same unit-distinguishing
-- guard, hence idempotent.
UPDATE events SET ts = ts * 1000 WHERE ts < 100000000000;
UPDATE file_drafts SET updated_at = updated_at * 1000 WHERE updated_at < 100000000000;
