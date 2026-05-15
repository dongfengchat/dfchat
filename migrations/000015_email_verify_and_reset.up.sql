-- Email verification + password reset tokens.
-- One row per token, deleted on use or expiry sweep.

CREATE TABLE IF NOT EXISTS email_verify_tokens (
  token       VARCHAR(64) PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at  TIMESTAMPTZ NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_email_verify_tokens_user ON email_verify_tokens (user_id);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
  token       VARCHAR(64) PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at  TIMESTAMPTZ NOT NULL,
  used        BOOLEAN NOT NULL DEFAULT FALSE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_user ON password_reset_tokens (user_id);
