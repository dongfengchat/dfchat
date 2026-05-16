-- Record the client IP that originated a registration so admins can
-- spot abuse patterns (e.g. one /24 mass-creating accounts). Nullable
-- because existing users predate this column.
ALTER TABLE users ADD COLUMN IF NOT EXISTS registered_from_ip VARCHAR(45);
CREATE INDEX IF NOT EXISTS idx_users_registered_from_ip
  ON users (registered_from_ip)
  WHERE registered_from_ip IS NOT NULL;
