-- Extra live-room features:
--   1. Followers — get notified when a host goes live
--   2. Persisted danmaku — viewers who join late can see recent chat
--   3. Bans — host can mute/kick disruptive viewers
--   4. Scheduled streams — host can plan a future broadcast

-- 1. Followers
CREATE TABLE IF NOT EXISTS live_room_followers (
  room_id     BIGINT NOT NULL REFERENCES live_rooms(id) ON DELETE CASCADE,
  user_id     BIGINT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (room_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_live_followers_user ON live_room_followers (user_id);

-- 2. Persisted danmaku (ring buffer — we keep the most recent N per room).
CREATE TABLE IF NOT EXISTS live_danmaku (
  id          BIGSERIAL PRIMARY KEY,
  room_id     BIGINT NOT NULL REFERENCES live_rooms(id) ON DELETE CASCADE,
  sender_id   BIGINT NOT NULL,
  content     VARCHAR(200) NOT NULL,
  color       VARCHAR(16),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_live_danmaku_room_created
  ON live_danmaku (room_id, created_at DESC);

-- 3. Bans (mute / kick). is_kick=false → can still watch but can't send danmaku.
CREATE TABLE IF NOT EXISTS live_room_bans (
  room_id     BIGINT NOT NULL REFERENCES live_rooms(id) ON DELETE CASCADE,
  user_id     BIGINT NOT NULL,
  banned_by   BIGINT NOT NULL,
  is_kick     BOOLEAN NOT NULL DEFAULT FALSE, -- true → also disconnect from watching
  reason      TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (room_id, user_id)
);

-- 4. Scheduled broadcast time (NULL = no schedule).
ALTER TABLE live_rooms
  ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMPTZ;

-- Discover queries that want upcoming + live + recent need a partial index.
CREATE INDEX IF NOT EXISTS idx_live_rooms_scheduled
  ON live_rooms (scheduled_at)
  WHERE scheduled_at IS NOT NULL AND status != 1;
