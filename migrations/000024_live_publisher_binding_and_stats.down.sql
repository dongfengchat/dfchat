ALTER TABLE live_rooms
  DROP COLUMN IF EXISTS current_publish_client_id,
  DROP COLUMN IF EXISTS peak_viewers,
  DROP COLUMN IF EXISTS total_danmaku,
  DROP COLUMN IF EXISTS duration_seconds;
