-- Two-in-one moderation infrastructure update:
--
-- 1. live_rooms.banned_reason — surfaces to the streamer in Studio
--    when their room is in status=3, so they know WHY (currently we
--    silently flip status and the streamer's OBS just fails to
--    connect with no explanation).
--
-- 2. live_room_review_state — per-room scheduling state for the
--    adaptive-decay moderation worker. Long-clean rooms drift to
--    longer review intervals (up to 15 min) so a GPU-constrained
--    LM Studio doesn't get overrun when more than ~4 broadcasts
--    are active simultaneously. Any flag instantly resets the
--    streak so suspicious rooms snap back to 1-min review.

ALTER TABLE live_rooms
  ADD COLUMN IF NOT EXISTS banned_reason TEXT;

CREATE TABLE IF NOT EXISTS live_room_review_state (
  room_id        BIGINT PRIMARY KEY REFERENCES live_rooms(id) ON DELETE CASCADE,
  -- Consecutive clean verdicts since the last flag (or stream start).
  -- Resets to 0 on any flag. Drives the decay interval.
  clean_streak   INT NOT NULL DEFAULT 0,
  -- When the worker should next consider this room. Workers pick
  -- rooms with next_due_at <= now() ORDER BY next_due_at ASC, then
  -- update this value after the review completes.
  next_due_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- When the most recent review happened (any verdict, clean or flag).
  last_check_at  TIMESTAMPTZ,
  -- When the most recent flag happened. NULL = never flagged this
  -- broadcast. Surfaced in admin UI so reviewers know the room's
  -- recent moderation history without joining live_ai_verdicts.
  last_flag_at   TIMESTAMPTZ
);

-- Fast "what to check next" query for the worker.
CREATE INDEX IF NOT EXISTS idx_live_review_due
  ON live_room_review_state (next_due_at);
