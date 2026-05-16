-- Groups can now be "archived" — read-only memorials of the past chats
-- when the only owner deletes their account and no admin or member
-- remains to inherit ownership. Existing rows default to non-archived
-- (still active).
ALTER TABLE groups
  ADD COLUMN IF NOT EXISTS is_archived BOOLEAN NOT NULL DEFAULT FALSE;
