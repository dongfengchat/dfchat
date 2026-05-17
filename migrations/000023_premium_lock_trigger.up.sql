-- Database-side guarantee that premium account numbers stay locked.
--
-- Without this, the lock rule only lives in the Go code's
-- isLockedPattern. A future PR that weakens the function, a manual
-- `psql` INSERT by an operator, or any other writer would silently
-- leak premium numbers into the random pool. The trigger makes that
-- structurally impossible — even bypassing the API.
--
-- Rule mirrored from server/internal/auth/account_no.go isLockedPattern:
--   Last 4 digits of account_no are all identical → is_locked := TRUE.
--
-- Fires on INSERT only. UPDATE is intentionally untouched so the admin
-- flows that legitimately clear is_locked (release a number, grant a
-- premium number to a user) keep working.

CREATE OR REPLACE FUNCTION enforce_account_no_premium_lock()
RETURNS TRIGGER AS $$
DECLARE
  s         TEXT;
  last_char CHAR(1);
  len_s     INT;
BEGIN
  s := NEW.account_no::TEXT;
  len_s := length(s);
  IF len_s >= 4 THEN
    last_char := substr(s, len_s, 1);
    IF substr(s, len_s - 3, 1) = last_char
       AND substr(s, len_s - 2, 1) = last_char
       AND substr(s, len_s - 1, 1) = last_char THEN
      NEW.is_locked := TRUE;
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS account_no_pool_premium_lock ON account_no_pool;
CREATE TRIGGER account_no_pool_premium_lock
  BEFORE INSERT ON account_no_pool
  FOR EACH ROW
  EXECUTE FUNCTION enforce_account_no_premium_lock();
