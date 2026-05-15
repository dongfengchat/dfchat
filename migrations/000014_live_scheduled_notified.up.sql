-- Mark each scheduled-stream "reminder push" as sent so the per-minute
-- scan in the API doesn't notify followers multiple times.
ALTER TABLE live_rooms
  ADD COLUMN IF NOT EXISTS scheduled_notified BOOLEAN NOT NULL DEFAULT FALSE;

-- Reset notified when host changes scheduled_at — handled in repo.SetScheduled.
-- Existing rows with no scheduled_at are unaffected; the scan never picks them.
