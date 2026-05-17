ALTER TABLE live_rooms
  DROP COLUMN IF EXISTS chat_subscribers_only,
  DROP COLUMN IF EXISTS slow_mode_seconds,
  DROP COLUMN IF EXISTS pinned_danmaku_text,
  DROP COLUMN IF EXISTS pinned_danmaku_sender,
  DROP COLUMN IF EXISTS pinned_danmaku_color,
  DROP COLUMN IF EXISTS pinned_danmaku_at;
