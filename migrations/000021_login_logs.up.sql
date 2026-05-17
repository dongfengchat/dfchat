-- Per-event login history. One row per attempt (success or failure)
-- so we can show users a "where you logged in from" timeline AND give
-- admins forensic data when investigating an account.
--
-- We keep failures too — a streak of failed-from-strange-IP followed
-- by a success is exactly the signal a phishing victim should see in
-- their settings page.
--
-- TTL: 90 days (cleaned by the auth sweeper). For account-takeover
-- forensics this is plenty; legal hold past 90 days would be a
-- different process entirely.

CREATE TABLE IF NOT EXISTS login_logs (
  id          BIGSERIAL PRIMARY KEY,
  user_id     BIGINT REFERENCES users(id) ON DELETE CASCADE,
  -- The literal string the user typed at login (could be username,
  -- email or account_no). Useful for forensics on failed attempts where
  -- we don't have a user_id (login → no row matched → user_id NULL).
  login_input VARCHAR(128),
  success     BOOLEAN NOT NULL,
  ip          VARCHAR(45),
  user_agent  TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Recent-history-for-user: by far the dominant query path. Filtered
-- index keeps it small.
CREATE INDEX IF NOT EXISTS idx_login_logs_user_recent
  ON login_logs (user_id, created_at DESC)
  WHERE user_id IS NOT NULL;

-- For sweeping old rows.
CREATE INDEX IF NOT EXISTS idx_login_logs_created_at
  ON login_logs (created_at);
