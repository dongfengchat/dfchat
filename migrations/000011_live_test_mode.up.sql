-- Test-broadcast mode: new rooms default to private. Owner can preview
-- their own stream end-to-end; nobody else sees the room in /live/rooms
-- listing. Owner flips is_test → false ("上线公开") to publish.
ALTER TABLE live_rooms
  ADD COLUMN IF NOT EXISTS is_test BOOLEAN NOT NULL DEFAULT TRUE;

-- Existing pre-test-mode rooms were public — keep them public.
UPDATE live_rooms SET is_test = FALSE WHERE created_at < NOW() - INTERVAL '1 hour';

-- Discover query filters by (status, is_test). Composite covers it.
CREATE INDEX IF NOT EXISTS idx_live_rooms_public_live
  ON live_rooms (status, viewer_count DESC) WHERE NOT is_test;
