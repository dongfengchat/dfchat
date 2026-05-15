CREATE TABLE IF NOT EXISTS users (
  id              BIGSERIAL PRIMARY KEY,
  username        VARCHAR(32)  UNIQUE NOT NULL,
  email           VARCHAR(128) UNIQUE NOT NULL,
  password_hash   VARCHAR(128) NOT NULL,
  nickname        VARCHAR(64)  NOT NULL,
  avatar_url      TEXT,
  bio             VARCHAR(255),
  status          SMALLINT     NOT NULL DEFAULT 0,    -- 0:normal 1:disabled 2:deleted
  email_verified  BOOLEAN      NOT NULL DEFAULT FALSE,
  last_login_at   TIMESTAMPTZ,
  last_login_ip   VARCHAR(45),
  created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_users_email  ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);

CREATE OR REPLACE FUNCTION trigger_set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS set_updated_at ON users;
CREATE TRIGGER set_updated_at
  BEFORE UPDATE ON users
  FOR EACH ROW
  EXECUTE FUNCTION trigger_set_updated_at();
