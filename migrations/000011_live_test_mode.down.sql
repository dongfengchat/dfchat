DROP INDEX IF EXISTS idx_live_rooms_public_live;
ALTER TABLE live_rooms DROP COLUMN IF EXISTS is_test;
