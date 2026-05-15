DROP TABLE IF EXISTS refresh_tokens;
ALTER TABLE conversation_members DROP COLUMN IF EXISTS last_read_seq;
DROP TABLE IF EXISTS message_pins;
DROP TABLE IF EXISTS message_reactions;
