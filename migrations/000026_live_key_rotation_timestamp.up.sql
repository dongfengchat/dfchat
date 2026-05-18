-- Tracks when a room's stream_key was last rotated. Used by the
-- background sweeper to distinguish "stream ended but key still
-- bound to whoever last published" (needs rotation after grace
-- period) from "owner already pressed Stop / Reset, key fresh".
--
-- Without this column the sweeper would re-rotate every minute
-- forever on any ended room, generating useless churn + log spam.
--
-- Why: the previous design rotated the key inside on_unpublish, so
-- any OBS network blip (which fires on_unpublish after a 7 s gap)
-- invalidated the streamer's key — streamers had to copy a new one
-- before resuming. New design rotates only after a 5-minute idle
-- window, so brief disconnects + OBS reconnects keep working.

ALTER TABLE live_rooms
  ADD COLUMN IF NOT EXISTS last_key_rotation_at TIMESTAMPTZ;

-- Backfill: existing rows have keys older than any future rotation,
-- so seeding to created_at gives the sweeper a sane "key vs ended_at"
-- comparison on day 1.
UPDATE live_rooms
   SET last_key_rotation_at = created_at
 WHERE last_key_rotation_at IS NULL;
