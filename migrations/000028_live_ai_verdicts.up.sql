-- Audit log of every AI moderation verdict (clean OR flagged).
--
-- The existing live_room_reports queue only stores the cases the AI
-- decided to escalate. For admins to audit AI accuracy — catch
-- silent miss-classifications and labeling so we can swap models
-- or tune thresholds — we need a row PER TICK including clean ones.
--
-- Retention: rotating 7-day window via the background cleanup loop
-- in live.RunVerdictCleanup. Pinned rows survive cleanup so an
-- admin investigating something specific can keep it indefinitely.

CREATE TABLE IF NOT EXISTS live_ai_verdicts (
  id            BIGSERIAL PRIMARY KEY,
  room_id       BIGINT NOT NULL REFERENCES live_rooms(id) ON DELETE CASCADE,
  -- Provider name as the worker logs it, e.g. "openai:gpt-4o-mini",
  -- "anthropic:claude-sonnet-4-7", "openai:gemma-4-31b-it-claude-opus-distill"
  -- (LM Studio uses the OpenAI provider but tagged with its model).
  provider      VARCHAR(128) NOT NULL,
  -- Category that scored highest. Same enum as live_room_reports.reason.
  max_category  VARCHAR(32) NOT NULL,
  max_score     REAL NOT NULL,
  -- All five category scores as JSON for full audit fidelity. Stored
  -- as JSONB so we can later aggregate without parsing TEXT.
  scores        JSONB NOT NULL,
  -- The model's stated reason in Chinese. Truncated to 1k at the
  -- worker layer; column is TEXT for headroom.
  reason        TEXT,
  -- Path to archived JPEG on /var/lib/dfchat/evidence; the file is
  -- captured at decision time so a review weeks later sees the
  -- exact frame, not the live thumbnail that's churned every 30 s.
  thumbnail_url TEXT,
  -- Did the AI itself decide to flag? Independent of admin label.
  flagged       BOOLEAN NOT NULL DEFAULT FALSE,
  -- If the AI flagged, this points at the row in live_room_reports
  -- that was inserted. NULL for clean verdicts. Used for "promote
  -- from clean → flagged" path: if admin labels a clean verdict as
  -- should_flag we create a report and update this fk.
  report_id     BIGINT REFERENCES live_room_reports(id) ON DELETE SET NULL,

  -- Manual override by an admin reviewing the AI's judgment.
  --   NULL            unreviewed
  --   'agree'         AI was right (audit-only)
  --   'should_flag'   AI said clean but admin disagrees (creates report)
  --   'false_positive' AI flagged but admin disagrees (dismisses report)
  manual_label  VARCHAR(32),
  labeled_by    BIGINT REFERENCES users(id) ON DELETE SET NULL,
  labeled_at    TIMESTAMPTZ,

  -- Pinned rows survive the 7-day cleanup sweep. Toggled by admin
  -- when they want to keep something for an investigation /
  -- training-set export.
  pinned        BOOLEAN NOT NULL DEFAULT FALSE,

  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Most queries are "newest first within a window" + label filter +
-- room filter. Composite index covers the common shapes.
CREATE INDEX IF NOT EXISTS idx_live_ai_verdicts_created
  ON live_ai_verdicts (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_live_ai_verdicts_room_time
  ON live_ai_verdicts (room_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_live_ai_verdicts_unlabeled
  ON live_ai_verdicts (created_at DESC)
  WHERE manual_label IS NULL;
-- Cleanup query touches old, unpinned rows; partial index keeps it
-- fast even when the table grows huge.
CREATE INDEX IF NOT EXISTS idx_live_ai_verdicts_cleanup
  ON live_ai_verdicts (created_at)
  WHERE pinned = FALSE;
