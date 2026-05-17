-- Track edits on text messages.
--   edited_at:    nullable; non-NULL iff the message has been edited at
--                 least once. Lets the client render "(已编辑)" tags.
--   edit_count:   how many times edited. Capped client-side at a small
--                 number but kept on server for moderation review.
-- We don't preserve previous text versions — the design treats edits
-- as in-place rewrites with a "this was edited" marker, matching
-- WeChat / Slack style. If a full revision history is later needed,
-- a separate message_edits table can be added.
ALTER TABLE messages
  ADD COLUMN IF NOT EXISTS edited_at  TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS edit_count INT NOT NULL DEFAULT 0;
