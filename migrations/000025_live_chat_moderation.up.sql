-- Live chat moderation features for stream owners:
--
--   chat_subscribers_only — if true, only followers can send danmaku
--                           (the existing /live/rooms/:id/follow). Lets
--                           a host curb drive-by trolls who wandered
--                           into Discover.
--   slow_mode_seconds     — per-room cooldown between messages from the
--                           same viewer. 0 disables. Enforced by the
--                           realtime danmaku handler in-memory, so it
--                           survives stream restarts via this column
--                           but doesn't survive api restart (acceptable
--                           — fresh stream usually has fewer trolls).
--   pinned_danmaku_*      — single pinned message at the top of the
--                           chat. Set by the owner via a new endpoint,
--                           cleared when the room ends. We snapshot the
--                           text + sender so the pinned message survives
--                           even if the original danmaku row is gone.
ALTER TABLE live_rooms
  ADD COLUMN IF NOT EXISTS chat_subscribers_only BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS slow_mode_seconds     INT     NOT NULL DEFAULT 0
    CHECK (slow_mode_seconds >= 0 AND slow_mode_seconds <= 300),
  ADD COLUMN IF NOT EXISTS pinned_danmaku_text   TEXT,
  ADD COLUMN IF NOT EXISTS pinned_danmaku_sender BIGINT REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS pinned_danmaku_color  VARCHAR(16),
  ADD COLUMN IF NOT EXISTS pinned_danmaku_at     TIMESTAMPTZ;
