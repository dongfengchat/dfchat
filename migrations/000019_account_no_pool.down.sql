DROP INDEX IF EXISTS idx_account_no_selections_expires;
DROP INDEX IF EXISTS idx_account_no_selections_ip;
DROP TABLE IF EXISTS account_no_selections;
DROP INDEX IF EXISTS idx_account_no_pool_free;
DROP TABLE IF EXISTS account_no_pool;
DROP TABLE IF EXISTS account_no_segments;

-- Best-effort restore: re-add default. The sequence (kept around) still
-- has its last value, so new inserts pick up where the pool flow stopped.
ALTER TABLE users ALTER COLUMN account_no
  SET DEFAULT nextval('users_account_no_seq');

-- Note: cannot easily roll back the nwsky 10001 change automatically
-- since their old number (100011) is in the pool now. Operator can
-- UPDATE users SET account_no = 100011 WHERE username = 'nwsky'; if needed.
