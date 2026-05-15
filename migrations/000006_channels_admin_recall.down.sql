DROP TABLE IF EXISTS channels;
ALTER TABLE users DROP COLUMN IF EXISTS is_admin;
DELETE FROM conversation_members WHERE conversation_id LIKE 'c\_%' ESCAPE '\';
DELETE FROM conversations         WHERE id              LIKE 'c\_%' ESCAPE '\';
