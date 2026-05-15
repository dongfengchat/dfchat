-- Live broadcast rooms. Each row is one streamer-owned channel that
-- accepts an RTMP push at rtmp://<host>/live/<stream_key> and exposes an
-- HLS pull at https://<host>/hls/<stream_key>.m3u8.
CREATE TABLE IF NOT EXISTS live_rooms (
  id            BIGSERIAL PRIMARY KEY,
  owner_id      BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title         VARCHAR(128) NOT NULL,
  cover_url     TEXT,
  category      VARCHAR(32),
  stream_key    VARCHAR(64)  UNIQUE NOT NULL,
  status        SMALLINT     NOT NULL DEFAULT 0, -- 0:idle 1:live 2:ended 3:banned
  viewer_count  INT          NOT NULL DEFAULT 0,
  total_views   BIGINT       NOT NULL DEFAULT 0,
  started_at    TIMESTAMPTZ,
  ended_at      TIMESTAMPTZ,
  created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_live_rooms_owner  ON live_rooms(owner_id);
CREATE INDEX IF NOT EXISTS idx_live_rooms_status ON live_rooms(status);

-- Auto-recorded MP4s. SRS writes flv files via its DVR module; a worker
-- transcodes them to mp4 and uploads to MinIO; this row points at the result.
CREATE TABLE IF NOT EXISTS live_recordings (
  id          BIGSERIAL PRIMARY KEY,
  room_id     BIGINT       NOT NULL REFERENCES live_rooms(id) ON DELETE CASCADE,
  file_url    TEXT         NOT NULL,
  duration    INT,
  size_bytes  BIGINT,
  created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_live_rec_room ON live_recordings(room_id, created_at DESC);
