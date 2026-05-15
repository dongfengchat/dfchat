-- Down: remove group conversation_members entries. Conversation rows for groups
-- are kept (harmless) — re-applying the up migration is idempotent.
DELETE FROM conversation_members
WHERE conversation_id LIKE 'g\_%' ESCAPE '\';
