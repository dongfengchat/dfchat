-- Public-facing account number, distinct from the internal PK.
--
-- Why a separate column instead of bumping users.id:
--   - users.id is referenced by ~20 FK tables (messages, friendships,
--     groups, channels, sessions, audit log, …). Modifying it would
--     require ON UPDATE CASCADE on every FK and risk silent corruption.
--   - Industry-standard separation: internal row id stays small + dense
--     and is never shown; public id is what humans see, type into login
--     forms, send to support, etc. Same pattern as QQ numbers, Stripe
--     ids, Slack ids, etc.
--
-- Existing users get account_no = 100000 + id so their numbers also
-- look "real" (6 digits) rather than 1-2 digits. New users get the
-- next sequence value, which starts at 100100 to leave buffer over
-- any backfilled values.

ALTER TABLE users ADD COLUMN account_no BIGINT;

-- Backfill: existing 6 test users get 100001..100007 etc.
UPDATE users SET account_no = 100000 + id WHERE account_no IS NULL;

ALTER TABLE users ALTER COLUMN account_no SET NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_account_no_unique UNIQUE (account_no);

CREATE INDEX IF NOT EXISTS idx_users_account_no ON users(account_no);

-- Sequence for new users. START WITH 100100 so we don't collide with
-- the backfill range; the gap is intentional buffer.
CREATE SEQUENCE IF NOT EXISTS users_account_no_seq START WITH 100100 OWNED BY users.account_no;
ALTER TABLE users ALTER COLUMN account_no SET DEFAULT nextval('users_account_no_seq');
