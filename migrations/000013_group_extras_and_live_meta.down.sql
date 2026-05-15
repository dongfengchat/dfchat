DROP TABLE IF EXISTS group_notify_modes;
ALTER TABLE groups DROP COLUMN IF EXISTS updated_at;
ALTER TABLE groups DROP COLUMN IF EXISTS announcement;
