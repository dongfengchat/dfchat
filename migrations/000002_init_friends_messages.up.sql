-- Friendships (simplified MVP: direct bidirectional add, no request/accept flow)
CREATE TABLE IF NOT EXISTS friendships (
  user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  friend_id  BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status     SMALLINT    NOT NULL DEFAULT 1,   -- 0:pending 1:accepted 2:blocked
  remark     VARCHAR(64),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, friend_id),
  CHECK (user_id <> friend_id)
);
CREATE INDEX IF NOT EXISTS idx_friendships_friend ON friendships(friend_id);

-- Conversations: id is canonical form, e.g. p_<smallId>_<largeId> for private
CREATE TABLE IF NOT EXISTS conversations (
  id              VARCHAR(64) PRIMARY KEY,
  type            SMALLINT    NOT NULL,        -- 1:private 2:group 3:channel
  last_message_id BIGINT,
  last_message_at TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Conversation members: who can see/access this conversation
CREATE TABLE IF NOT EXISTS conversation_members (
  conversation_id VARCHAR(64) NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  user_id         BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  joined_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_conv_members_user ON conversation_members(user_id);

-- Messages: single table for MVP. Shard later per design doc 7.1.7
CREATE TABLE IF NOT EXISTS messages (
  id              BIGSERIAL PRIMARY KEY,
  conversation_id VARCHAR(64) NOT NULL,
  sender_id       BIGINT      NOT NULL REFERENCES users(id),
  type            VARCHAR(20) NOT NULL,
  content         JSONB       NOT NULL,
  seq             BIGINT      NOT NULL,
  reply_to        BIGINT,
  mentions        BIGINT[],
  is_recalled     BOOLEAN     NOT NULL DEFAULT FALSE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_messages_conv_seq ON messages(conversation_id, seq DESC);
CREATE INDEX IF NOT EXISTS idx_messages_created  ON messages(created_at DESC);

-- Per-conversation seq generator
CREATE TABLE IF NOT EXISTS conversation_seq (
  conversation_id VARCHAR(64) PRIMARY KEY,
  last_seq        BIGINT NOT NULL DEFAULT 0
);
