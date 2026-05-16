-- Email change confirmation tokens.
-- When a user wants to change their registered email we don't update
-- users.email immediately — instead we email the *new* address with a
-- confirmation link. Only after the user clicks does the swap happen,
-- proving ownership of the new mailbox.
--
-- Schema mirrors password_reset_tokens (single-use, expires_at gated)
-- but carries the proposed new email alongside.
CREATE TABLE IF NOT EXISTS email_change_requests (
  token        VARCHAR(64) PRIMARY KEY,
  user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  new_email    VARCHAR(128) NOT NULL,
  expires_at   TIMESTAMPTZ NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_email_change_requests_user ON email_change_requests (user_id);
