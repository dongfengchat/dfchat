-- Admin flag for management UI
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE;

-- Channels: a Discord-style multi-channel structure inside a group.
-- For MVP we only support text channels (type=1); voice (2) is reserved.
CREATE TABLE IF NOT EXISTS channels (
  id         BIGSERIAL PRIMARY KEY,
  group_id   BIGINT       NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  type       SMALLINT     NOT NULL DEFAULT 1, -- 1:text 2:voice 3:announce
  name       VARCHAR(64)  NOT NULL,
  topic      VARCHAR(255),
  position   INT          NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_channels_group ON channels(group_id, position);

-- Backfill: every existing group gets a default "general" channel
-- and a c_<channelId> conversation. Members copied from group_members.
DO $$
DECLARE
  g RECORD;
  new_channel_id BIGINT;
  conv_id TEXT;
BEGIN
  FOR g IN SELECT id FROM groups LOOP
    -- skip if a channel already exists
    IF EXISTS (SELECT 1 FROM channels WHERE group_id = g.id) THEN
      CONTINUE;
    END IF;
    INSERT INTO channels (group_id, name, position) VALUES (g.id, 'general', 0)
      RETURNING id INTO new_channel_id;
    conv_id := 'c_' || new_channel_id::text;
    INSERT INTO conversations (id, type) VALUES (conv_id, 2) ON CONFLICT DO NOTHING;
    INSERT INTO conversation_seq (conversation_id, last_seq) VALUES (conv_id, 0) ON CONFLICT DO NOTHING;
    INSERT INTO conversation_members (conversation_id, user_id)
      SELECT conv_id, user_id FROM group_members WHERE group_id = g.id
      ON CONFLICT DO NOTHING;
  END LOOP;
END$$;
