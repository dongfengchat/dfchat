-- Groups (flat groups for MVP; channel-server type=2 is reserved for later)
CREATE TABLE IF NOT EXISTS groups (
  id           BIGSERIAL PRIMARY KEY,
  type         SMALLINT     NOT NULL DEFAULT 1,   -- 1:flat group  2:channel server
  name         VARCHAR(64)  NOT NULL,
  icon_url     TEXT,
  description  TEXT,
  owner_id     BIGINT       NOT NULL REFERENCES users(id),
  member_count INT          NOT NULL DEFAULT 0,
  max_members  INT          NOT NULL DEFAULT 500,
  is_public    BOOLEAN      NOT NULL DEFAULT FALSE,
  invite_code  VARCHAR(16)  UNIQUE NOT NULL,
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS group_members (
  group_id   BIGINT      NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       SMALLINT    NOT NULL DEFAULT 0,    -- 0:member 1:admin 2:owner
  nickname   VARCHAR(64),
  joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_group_members_user ON group_members(user_id);
