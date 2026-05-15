DROP INDEX IF EXISTS idx_live_rooms_scheduled;
ALTER TABLE live_rooms DROP COLUMN IF EXISTS scheduled_at;
DROP TABLE IF EXISTS live_room_bans;
DROP INDEX IF EXISTS idx_live_danmaku_room_created;
DROP TABLE IF EXISTS live_danmaku;
DROP INDEX IF EXISTS idx_live_followers_user;
DROP TABLE IF EXISTS live_room_followers;
