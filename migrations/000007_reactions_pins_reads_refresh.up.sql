-- Reactions: any user can react with multiple distinct emoji to any message.
CREATE TABLE IF NOT EXISTS message_reactions (
  message_id BIGINT       NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  user_id    BIGINT       NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
  emoji      VARCHAR(16)  NOT NULL,
  created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
  PRIMARY KEY (message_id, user_id, emoji)
);
CREATE INDEX IF NOT EXISTS idx_reactions_message ON message_reactions(message_id);

-- Pins: per conversation, a small set of pinned messages.
CREATE TABLE IF NOT EXISTS message_pins (
  conversation_id VARCHAR(64) NOT NULL,
  message_id      BIGINT      NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  pinned_by       BIGINT      NOT NULL REFERENCES users(id),
  pinned_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (conversation_id, message_id)
);
CREATE INDEX IF NOT EXISTS idx_pins_conv ON message_pins(conversation_id, pinned_at DESC);

-- Per-member read cursor. Lets the server compute "has the peer seen msg X"
-- and broadcast read events. (Client also tracks this locally for unread badges,
-- but the server-side copy enables read receipts across devices and to peers.)
ALTER TABLE conversation_members
  ADD COLUMN IF NOT EXISTS last_read_seq BIGINT NOT NULL DEFAULT 0;

-- Refresh tokens for /auth/refresh flow. Single-use; rotated on each refresh.
CREATE TABLE IF NOT EXISTS refresh_tokens (
  token      VARCHAR(64)  PRIMARY KEY,
  user_id    BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device     VARCHAR(255),
  expires_at TIMESTAMPTZ  NOT NULL,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_refresh_user ON refresh_tokens(user_id);
