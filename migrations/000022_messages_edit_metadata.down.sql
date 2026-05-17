ALTER TABLE messages
  DROP COLUMN IF EXISTS edited_at,
  DROP COLUMN IF EXISTS edit_count;
