-- Group polish: announcement + notify mode + bookkeeping
ALTER TABLE groups
  ADD COLUMN IF NOT EXISTS announcement TEXT,
  ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Per-user notify mode for each group (0=all, 1=mention only, 2=muted).
-- We attach it to the existing conversation_preferences mute model so the
-- client UI can share a single store key.
CREATE TABLE IF NOT EXISTS group_notify_modes (
  group_id    BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  user_id     BIGINT NOT NULL,
  mode        SMALLINT NOT NULL DEFAULT 0,  -- 0 all, 1 mention-only, 2 muted
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (group_id, user_id)
);
