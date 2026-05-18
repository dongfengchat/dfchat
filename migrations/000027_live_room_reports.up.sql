-- User-reports queue for live rooms. Viewers tap "举报" inside a
-- broadcast and we drop a row here; the admin "直播管理 / 举报队列"
-- page renders pending ones with the room thumbnail so a human can
-- decide in under 5 seconds: force-end / ban-room / dismiss.
--
-- Also used by the AI-agent moderation worker (Phase B): the worker
-- INSERTs rows with reporter_id IS NULL so they look like a "system
-- reported this" entry in the same queue. Single review surface,
-- two producers (humans + AI).

CREATE TABLE IF NOT EXISTS live_room_reports (
  id            BIGSERIAL PRIMARY KEY,
  room_id       BIGINT NOT NULL REFERENCES live_rooms(id) ON DELETE CASCADE,
  -- NULL when produced by an AI moderation worker (or anonymous
  -- channel we might add later); FK SET NULL keeps the report alive
  -- even if the reporter's account is deleted.
  reporter_id   BIGINT REFERENCES users(id) ON DELETE SET NULL,
  -- Short enum-style category so dashboards can filter cleanly.
  -- Front-end picker drives this — keep additions to a single place.
  --   nsfw      色情/低俗
  --   violence  暴力/血腥
  --   politics  政治敏感
  --   gambling  赌博
  --   fraud     诈骗/违规广告
  --   other     其他
  reason        VARCHAR(32) NOT NULL,
  -- Free-form user note. Truncated to 1k chars at the API layer; the
  -- column is TEXT to leave headroom for translated-back text from
  -- the AI worker's "why I flagged this" explanation.
  note          TEXT,
  -- Snapshot taken at the time of report so the admin doesn't lose
  -- the evidence if the broadcast ends before they review it. NULL
  -- if no thumbnail was available (race with stream ending) — the
  -- admin page falls back to the room's stored cover_url.
  thumbnail_url TEXT,
  -- 0 待审 / 1 已处置 / 2 已驳回. Default 0 so a fresh report shows
  -- up in the queue automatically. Indexed on (status, created_at)
  -- so the "newest pending first" query is one fast index scan.
  status        SMALLINT NOT NULL DEFAULT 0,
  reviewed_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
  reviewed_at   TIMESTAMPTZ,
  -- What the admin did. Mirrors action names from the admin handler
  -- so the audit trail in this table matches audit_logs entries.
  --   force_ended  / banned_owner  / no_action
  action_taken  VARCHAR(32),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_live_reports_status_time
  ON live_room_reports (status, created_at DESC);

-- Rate-limit dedupe helper: same reporter spamming the same room
-- for the same reason within a short window will hit this partial
-- unique index and the INSERT will conflict (caller treats conflict
-- as "ok, already counted"). NULL reporter_id (AI) is excluded so
-- the worker can flag the same room multiple times across passes.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_live_report_recent
  ON live_room_reports (room_id, reporter_id, reason)
  WHERE reporter_id IS NOT NULL AND status = 0;
