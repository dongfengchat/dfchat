-- Hardens the live-streaming flow against two classes of bug:
--
--   1. Stream takeover: if the stream key leaks (and it does — it ends
--      up in the HLS playback URL viewers fetch), today anyone who knows
--      it can RTMP-push to that key and the SRS hook will SetLive again,
--      swapping the broadcast under the owner's feet. We now bind the
--      first publisher's SRS client_id to the room and reject subsequent
--      on_publish events for the same key from a different client_id
--      until the original session ends.
--
--   2. Stats lost on stream end: the old SetEnded only flipped status +
--      ended_at. Recap UI couldn't show peak viewers, total danmaku, or
--      stream duration. We finalize those into dedicated columns now so
--      "live ended" pages can render proper numbers without scanning
--      danmaku rows on every load.

ALTER TABLE live_rooms
  -- Bound to the active SRS publisher client (cleared on unpublish).
  -- Non-NULL = stream slot occupied; reject second publisher.
  ADD COLUMN IF NOT EXISTS current_publish_client_id VARCHAR(64),
  -- Peak concurrent viewers (max of viewer_count over the live session)
  -- Snapshotted into the row on SetEnded so it survives stream restarts.
  ADD COLUMN IF NOT EXISTS peak_viewers      INT NOT NULL DEFAULT 0,
  -- Cumulative danmaku count for the most recent broadcast. Bumped by
  -- the realtime handler on every successful danmaku send; reset on
  -- SetLive (a new broadcast starts fresh).
  ADD COLUMN IF NOT EXISTS total_danmaku     INT NOT NULL DEFAULT 0,
  -- Computed at SetEnded as (ended_at - started_at) in seconds.
  ADD COLUMN IF NOT EXISTS duration_seconds  INT NOT NULL DEFAULT 0;
