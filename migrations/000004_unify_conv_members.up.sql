-- Unify access control: conversation_members becomes single source of truth.
-- Backfill so every existing group has a conversation row and every group member
-- is also a conversation member.

INSERT INTO conversations (id, type)
SELECT 'g_' || g.id::text, 2 FROM groups g
ON CONFLICT (id) DO NOTHING;

INSERT INTO conversation_seq (conversation_id, last_seq)
SELECT 'g_' || g.id::text, 0 FROM groups g
ON CONFLICT (conversation_id) DO NOTHING;

INSERT INTO conversation_members (conversation_id, user_id)
SELECT 'g_' || gm.group_id::text, gm.user_id FROM group_members gm
ON CONFLICT DO NOTHING;
