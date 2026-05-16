-- Account-number pool, segment management, and draw sessions.
--
-- Replaces the auto-increment sequence with a "pick from a random draw"
-- flow inspired by QQ:
--   - Numbers are pre-generated into account_no_pool per opened segment.
--   - "Super-premium" numbers (all-same, strict ascending/descending,
--     palindrome, 5+ consecutive same digits) are marked is_locked=true
--     and never appear in random draws — reserved for admin grants /
--     premium sale.
--   - At register time the user calls /auth/account-no/draw which
--     atomically reserves 10 free numbers for 5 minutes. They can
--     refresh up to 3 times (each refresh releases the previous 10 and
--     picks 10 new). They submit register with their chosen number
--     plus the selection token, and the server cross-checks.
--   - 5-digit range (10000-99999) is reserved for admins — never
--     pool-managed. Admins are minted via the admin tool.

CREATE TABLE IF NOT EXISTS account_no_segments (
  segment_no  INT PRIMARY KEY,
  range_start BIGINT NOT NULL,
  range_end   BIGINT NOT NULL,
  state       VARCHAR(16) NOT NULL DEFAULT 'open',
  opened_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  closed_at   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS account_no_pool (
  account_no       BIGINT PRIMARY KEY,
  segment_no       INT NOT NULL REFERENCES account_no_segments(segment_no),
  -- Locked numbers are off the random pool (admin grants / premium sale).
  is_locked        BOOLEAN NOT NULL DEFAULT FALSE,
  -- Held by an in-flight draw/refresh session. NULL = free.
  reserved_until   TIMESTAMPTZ,
  -- NULL until a user registers with this number.
  claimed_user_id  BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Speeds up the "find 10 random free numbers in segment N" query.
CREATE INDEX IF NOT EXISTS idx_account_no_pool_free
  ON account_no_pool (segment_no)
  WHERE claimed_user_id IS NULL AND is_locked = FALSE;

-- Per-IP draw session. Tracks how many refreshes were used and which
-- numbers are currently held so register can cross-check + release the
-- non-chosen siblings.
CREATE TABLE IF NOT EXISTS account_no_selections (
  token            VARCHAR(64) PRIMARY KEY,
  client_ip        VARCHAR(45) NOT NULL,
  refreshes_used   INT NOT NULL DEFAULT 0,
  reserved_nos     BIGINT[] NOT NULL,
  expires_at       TIMESTAMPTZ NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_account_no_selections_ip
  ON account_no_selections (client_ip);
CREATE INDEX IF NOT EXISTS idx_account_no_selections_expires
  ON account_no_selections (expires_at);

-- Drop the auto-increment default. account_no is now always supplied
-- explicitly (either by the pool flow for users, or by admin tool for
-- 5-digit admins). The sequence stays around in case some operator
-- script depends on it.
ALTER TABLE users ALTER COLUMN account_no DROP DEFAULT;

-- Promote nwsky (the founder) to admin and a 5-digit admin number.
-- 10001 = "founder #1". Old number 100011 was a backfilled 100000+id.
-- 5-digit range (10000-99999) is reserved for staff accounts.
UPDATE users SET account_no = 10001, is_admin = true WHERE username = 'nwsky';

-- Seed the first user segment so the pool initializer has something
-- to work with at server startup. Pool rows themselves are populated
-- in Go (need the premium-detection logic).
INSERT INTO account_no_segments (segment_no, range_start, range_end)
VALUES (1, 100000, 109999)
ON CONFLICT (segment_no) DO NOTHING;
