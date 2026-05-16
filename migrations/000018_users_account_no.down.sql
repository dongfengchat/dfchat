ALTER TABLE users DROP CONSTRAINT IF EXISTS users_account_no_unique;
DROP INDEX IF EXISTS idx_users_account_no;
ALTER TABLE users DROP COLUMN IF EXISTS account_no;
DROP SEQUENCE IF EXISTS users_account_no_seq;
