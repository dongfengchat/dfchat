DROP INDEX IF EXISTS idx_users_registered_from_ip;
ALTER TABLE users DROP COLUMN IF EXISTS registered_from_ip;
