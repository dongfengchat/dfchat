ALTER TABLE conversation_members DROP COLUMN IF EXISTS muted;
ALTER TABLE refresh_tokens       DROP COLUMN IF EXISTS last_used_at;
