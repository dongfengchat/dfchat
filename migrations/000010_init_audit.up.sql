-- Audit log: write-only ledger of privileged actions for after-the-fact
-- forensics and dispute resolution. Insert from admin handlers (and any
-- other handler that does something users could later challenge).
CREATE TABLE IF NOT EXISTS audit_logs (
  id           BIGSERIAL PRIMARY KEY,
  actor_id     BIGINT       NOT NULL,                -- user performing the action
  action       VARCHAR(64)  NOT NULL,                -- e.g. 'user.ban', 'live.delete'
  target_kind  VARCHAR(32),                          -- 'user' | 'group' | 'message' | ...
  target_id    BIGINT,
  ip           INET,
  user_agent   TEXT,
  metadata     JSONB,                                -- before/after diff, free-form
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_actor_created
  ON audit_logs (actor_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_action_created
  ON audit_logs (action, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_target
  ON audit_logs (target_kind, target_id);
