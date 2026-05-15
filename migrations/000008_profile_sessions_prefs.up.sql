-- Track when a refresh token was last successfully used; the devices page
-- relies on this to show "last active 5 minutes ago".
ALTER TABLE refresh_tokens
  ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;

-- Per-member conversation preferences. For now just "muted" — extend with
-- pinned-to-top / custom notification sound when needed.
ALTER TABLE conversation_members
  ADD COLUMN IF NOT EXISTS muted BOOLEAN NOT NULL DEFAULT FALSE;
