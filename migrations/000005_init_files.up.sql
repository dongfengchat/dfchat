CREATE TABLE IF NOT EXISTS files (
  id          BIGSERIAL PRIMARY KEY,
  user_id     BIGINT       NOT NULL REFERENCES users(id),
  name        VARCHAR(255) NOT NULL,
  mime_type   VARCHAR(128),
  size_bytes  BIGINT       NOT NULL DEFAULT 0,
  storage_key VARCHAR(255) NOT NULL UNIQUE,
  url         TEXT         NOT NULL,
  thumbnail   TEXT,
  status      SMALLINT     NOT NULL DEFAULT 0, -- 0:pending 1:confirmed 2:rejected
  created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_files_user ON files(user_id);
