package message

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// EnsurePrivateConversation creates the conversation row and membership rows if they don't exist.
// Returns the canonical conversationId.
func (r *Repo) EnsurePrivateConversation(ctx context.Context, a, b int64) (string, error) {
	convID := PrivateConvID(a, b)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversations (id, type) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
		convID, ConvTypePrivate); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_members (conversation_id, user_id) VALUES ($1, $2), ($1, $3)
		 ON CONFLICT DO NOTHING`, convID, a, b); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_seq (conversation_id, last_seq) VALUES ($1, 0) ON CONFLICT DO NOTHING`,
		convID); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return convID, nil
}

// EnsureGroupConversation creates the conversation + seq rows for a group if not present.
// Group membership lives in group_members, so we don't touch conversation_members here.
func (r *Repo) EnsureGroupConversation(ctx context.Context, convID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversations (id, type) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
		convID, ConvTypeGroup); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_seq (conversation_id, last_seq) VALUES ($1, 0) ON CONFLICT DO NOTHING`,
		convID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type InsertParams struct {
	ConversationID string
	SenderID       int64
	Type           string
	Content        json.RawMessage
	Mentions       []int64
	ReplyTo        *int64
}

var ErrNotOwner = errors.New("not the message owner")
var ErrRecallWindow = errors.New("recall window expired")
var ErrMessageNotFound = errors.New("message not found")
var ErrAlreadyPinned = errors.New("message already pinned")
var ErrNotPinned = errors.New("message not pinned")

// Recall flips is_recalled if the caller owns the message and it was sent
// within RecallWindowSeconds. Returns the updated message (so caller can
// fan-out a chat.recall event).
func (r *Repo) Recall(ctx context.Context, msgID, userID int64) (*Message, error) {
	var (
		senderID  int64
		createdAt time.Time
		convID    string
	)
	err := r.pool.QueryRow(ctx,
		`SELECT sender_id, created_at, conversation_id FROM messages WHERE id = $1`, msgID,
	).Scan(&senderID, &createdAt, &convID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMessageNotFound
	}
	if err != nil {
		return nil, err
	}
	if senderID != userID {
		return nil, ErrNotOwner
	}
	if time.Since(createdAt) > RecallWindowSeconds*time.Second {
		return nil, ErrRecallWindow
	}
	m := &Message{}
	err = r.pool.QueryRow(ctx,
		`UPDATE messages SET is_recalled = TRUE WHERE id = $1
		 RETURNING id, conversation_id, sender_id, type, content, seq,
		           COALESCE(mentions, '{}'::BIGINT[]), reply_to, is_recalled, edited_at, edit_count, created_at`,
		msgID,
	).Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content,
		&m.Seq, &m.Mentions, &m.ReplyTo, &m.IsRecalled, &m.EditedAt, &m.EditCount, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// Insert atomically allocates seq and inserts the message, also updating conversations.last_message_*.
func (r *Repo) Insert(ctx context.Context, p InsertParams) (*Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var seq int64
	if err := tx.QueryRow(ctx,
		`UPDATE conversation_seq SET last_seq = last_seq + 1
		 WHERE conversation_id = $1 RETURNING last_seq`,
		p.ConversationID).Scan(&seq); err != nil {
		return nil, err
	}

	m := &Message{}
	if err := tx.QueryRow(ctx,
		`INSERT INTO messages (conversation_id, sender_id, type, content, seq, mentions, reply_to)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, conversation_id, sender_id, type, content, seq,
		           COALESCE(mentions, '{}'::BIGINT[]), reply_to, is_recalled, edited_at, edit_count, created_at`,
		p.ConversationID, p.SenderID, p.Type, p.Content, seq, p.Mentions, p.ReplyTo,
	).Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content, &m.Seq, &m.Mentions, &m.ReplyTo, &m.IsRecalled, &m.EditedAt, &m.EditCount, &m.CreatedAt); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE conversations SET last_message_id = $1, last_message_at = $2 WHERE id = $3`,
		m.ID, m.CreatedAt, m.ConversationID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// ConvOfMessage returns the conversation id for a message; used for
// reaction / pin / recall fan-out.
func (r *Repo) ConvOfMessage(ctx context.Context, msgID int64) (string, error) {
	var conv string
	err := r.pool.QueryRow(ctx,
		`SELECT conversation_id FROM messages WHERE id = $1`, msgID).Scan(&conv)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrMessageNotFound
	}
	return conv, err
}

// GetByID hydrates a single message including all metadata. Used by
// the edit handler to validate ownership + recall state + window.
func (r *Repo) GetByID(ctx context.Context, msgID int64) (*Message, error) {
	m := &Message{}
	err := r.pool.QueryRow(ctx,
		`SELECT id, conversation_id, sender_id, type, content, seq,
		        COALESCE(mentions, '{}'::BIGINT[]), reply_to, is_recalled, edited_at, edit_count, created_at
		 FROM messages WHERE id = $1`, msgID,
	).Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content,
		&m.Seq, &m.Mentions, &m.ReplyTo, &m.IsRecalled, &m.EditedAt, &m.EditCount, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMessageNotFound
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// Delete hard-removes a message row. CASCADE on FK constraints drops
// reactions / pins / read receipts that reference it. Caller MUST
// have verified ownership + retention window. After this call the
// server has no copy at all — only clients that synced before the
// delete retain it (in their local archive, if any).
func (r *Repo) Delete(ctx context.Context, msgID int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM messages WHERE id = $1`, msgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMessageNotFound
	}
	return nil
}

// Edit rewrites the content of a text message and bumps edit_count +
// edited_at. Caller must already have checked ownership, window, and
// recall state. Returns the updated row for fan-out.
func (r *Repo) Edit(ctx context.Context, msgID int64, content json.RawMessage) (*Message, error) {
	m := &Message{}
	err := r.pool.QueryRow(ctx,
		`UPDATE messages
		    SET content = $2,
		        edited_at = NOW(),
		        edit_count = edit_count + 1
		  WHERE id = $1
		  RETURNING id, conversation_id, sender_id, type, content, seq,
		            COALESCE(mentions, '{}'::BIGINT[]), reply_to, is_recalled, edited_at, edit_count, created_at`,
		msgID, content,
	).Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content,
		&m.Seq, &m.Mentions, &m.ReplyTo, &m.IsRecalled, &m.EditedAt, &m.EditCount, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMessageNotFound
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// AttachReactions populates msg.Reactions for each message in the slice.
// Returns the same slice for chaining.
func (r *Repo) AttachReactions(ctx context.Context, msgs []*Message) ([]*Message, error) {
	if len(msgs) == 0 {
		return msgs, nil
	}
	ids := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	rows, err := r.pool.Query(ctx,
		`SELECT message_id, emoji, user_id FROM message_reactions
		 WHERE message_id = ANY($1::BIGINT[]) ORDER BY message_id, emoji, created_at`,
		ids)
	if err != nil {
		return msgs, err
	}
	defer rows.Close()

	// (messageID, emoji) → counters
	type key struct {
		mid   int64
		emoji string
	}
	tally := make(map[key]*ReactionCount)
	for rows.Next() {
		var mid int64
		var emoji string
		var uid int64
		if err := rows.Scan(&mid, &emoji, &uid); err != nil {
			return msgs, err
		}
		k := key{mid, emoji}
		rc := tally[k]
		if rc == nil {
			rc = &ReactionCount{Emoji: emoji}
			tally[k] = rc
		}
		rc.Count++
		rc.UserIDs = append(rc.UserIDs, uid)
	}
	if err := rows.Err(); err != nil {
		return msgs, err
	}

	byMsg := make(map[int64][]ReactionCount)
	for k, rc := range tally {
		byMsg[k.mid] = append(byMsg[k.mid], *rc)
	}
	for _, m := range msgs {
		m.Reactions = byMsg[m.ID]
	}
	return msgs, nil
}

// AddReaction is idempotent — returns true if a new row was added.
func (r *Repo) AddReaction(ctx context.Context, msgID, userID int64, emoji string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO message_reactions (message_id, user_id, emoji)
		 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		msgID, userID, emoji)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) RemoveReaction(ctx context.Context, msgID, userID int64, emoji string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM message_reactions WHERE message_id=$1 AND user_id=$2 AND emoji=$3`,
		msgID, userID, emoji)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SummariseReactions returns the full ReactionCount slice for one message.
// Used for chat.reaction events so clients can refresh their view.
func (r *Repo) SummariseReactions(ctx context.Context, msgID int64) ([]ReactionCount, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT emoji, user_id FROM message_reactions WHERE message_id = $1 ORDER BY emoji, created_at`,
		msgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byEmoji := map[string]*ReactionCount{}
	order := []string{}
	for rows.Next() {
		var emoji string
		var uid int64
		if err := rows.Scan(&emoji, &uid); err != nil {
			return nil, err
		}
		rc := byEmoji[emoji]
		if rc == nil {
			rc = &ReactionCount{Emoji: emoji}
			byEmoji[emoji] = rc
			order = append(order, emoji)
		}
		rc.Count++
		rc.UserIDs = append(rc.UserIDs, uid)
	}
	out := make([]ReactionCount, 0, len(order))
	for _, e := range order {
		out = append(out, *byEmoji[e])
	}
	return out, rows.Err()
}

// Pin / Unpin / list ----------------------------------------------------

// CountPins returns how many pinned messages the conversation currently
// has. The caller uses this to enforce a per-conv cap so a chatty group
// can't accumulate thousands of pins.
func (r *Repo) CountPins(ctx context.Context, convID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM message_pins WHERE conversation_id = $1`, convID).Scan(&n)
	return n, err
}

func (r *Repo) Pin(ctx context.Context, convID string, msgID, byUserID int64) error {
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO message_pins (conversation_id, message_id, pinned_by)
		 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		convID, msgID, byUserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyPinned
	}
	return nil
}

func (r *Repo) Unpin(ctx context.Context, convID string, msgID int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM message_pins WHERE conversation_id = $1 AND message_id = $2`,
		convID, msgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotPinned
	}
	return nil
}

func (r *Repo) ListPins(ctx context.Context, convID string) ([]*Pin, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.conversation_id, p.message_id, p.pinned_by, p.pinned_at,
		       m.id, m.conversation_id, m.sender_id, m.type, m.content, m.seq,
		       COALESCE(m.mentions, '{}'::BIGINT[]), m.reply_to, m.is_recalled, m.created_at
		FROM message_pins p
		JOIN messages m ON m.id = p.message_id
		WHERE p.conversation_id = $1
		ORDER BY p.pinned_at DESC`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Pin, 0)
	for rows.Next() {
		p := &Pin{}
		m := &Message{}
		if err := rows.Scan(
			&p.ConversationID, &p.MessageID, &p.PinnedBy, &p.PinnedAt,
			&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content, &m.Seq,
			&m.Mentions, &m.ReplyTo, &m.IsRecalled, &m.EditedAt, &m.EditCount, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		p.Message = m
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkRead updates the caller's last_read_seq for the conversation. Returns
// the new value (which may already have been higher than the input).
func (r *Repo) MarkRead(ctx context.Context, convID string, userID, seq int64) (int64, error) {
	var newSeq int64
	err := r.pool.QueryRow(ctx,
		`UPDATE conversation_members
		   SET last_read_seq = GREATEST(last_read_seq, $3)
		 WHERE conversation_id = $1 AND user_id = $2
		 RETURNING last_read_seq`,
		convID, userID, seq).Scan(&newSeq)
	return newSeq, err
}

// MembersOf returns user ids that belong to a conversation.
func (r *Repo) MembersOf(ctx context.Context, convID string) ([]int64, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT user_id FROM conversation_members WHERE conversation_id = $1`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, 2)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetMuted toggles the per-member mute preference. Used by the conversation
// preferences endpoint to silence noisy groups without losing access.
func (r *Repo) SetMuted(ctx context.Context, convID string, userID int64, muted bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE conversation_members SET muted = $3
		 WHERE conversation_id = $1 AND user_id = $2`,
		convID, userID, muted)
	return err
}

// IsMember reports whether a user belongs to a conversation.
func (r *Repo) IsMember(ctx context.Context, convID string, userID int64) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT 1 FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`,
		convID, userID).Scan(&n)
	if err != nil {
		// pgx.ErrNoRows or other
		return false, nil
	}
	return n == 1, nil
}


// ListAfter returns messages with seq > afterSeq, oldest-first, up to limit.
// Used for catch-up after a client reconnects.
func (r *Repo) ListAfter(ctx context.Context, convID string, afterSeq int64, limit int) ([]*Message, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, conversation_id, sender_id, type, content, seq,
		        COALESCE(mentions, '{}'::BIGINT[]), reply_to, is_recalled, edited_at, edit_count, created_at
		 FROM messages WHERE conversation_id=$1 AND seq > $2
		 ORDER BY seq ASC LIMIT $3`, convID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Message, 0, limit)
	for rows.Next() {
		m := &Message{}
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content,
			&m.Seq, &m.Mentions, &m.ReplyTo, &m.IsRecalled, &m.EditedAt, &m.EditCount, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListAround returns up to `window` messages immediately before and after
// `seq` (inclusive of the target). Ordered ascending (oldest first). Used by
// jump-to-message from search results.
func (r *Repo) ListAround(ctx context.Context, convID string, seq int64, window int) ([]*Message, error) {
	if window <= 0 || window > 200 {
		window = 25
	}
	rows, err := r.pool.Query(ctx, `
		(SELECT id, conversation_id, sender_id, type, content, seq,
		        COALESCE(mentions, '{}'::BIGINT[]), reply_to, is_recalled, edited_at, edit_count, created_at
		 FROM messages WHERE conversation_id=$1 AND seq < $2
		 ORDER BY seq DESC LIMIT $3)
		UNION
		(SELECT id, conversation_id, sender_id, type, content, seq,
		        COALESCE(mentions, '{}'::BIGINT[]), reply_to, is_recalled, edited_at, edit_count, created_at
		 FROM messages WHERE conversation_id=$1 AND seq >= $2
		 ORDER BY seq ASC LIMIT $3)
		ORDER BY seq ASC`, convID, seq, window)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Message, 0)
	for rows.Next() {
		m := &Message{}
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content,
			&m.Seq, &m.Mentions, &m.ReplyTo, &m.IsRecalled, &m.EditedAt, &m.EditCount, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListRecent returns the most recent messages of a conversation (newest first).
func (r *Repo) ListRecent(ctx context.Context, convID string, limit int, beforeSeq int64) ([]*Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const baseQ = `SELECT id, conversation_id, sender_id, type, content, seq,
		        COALESCE(mentions, '{}'::BIGINT[]), reply_to, is_recalled, edited_at, edit_count, created_at FROM messages`
	rows, err := func() (pgx.Rows, error) {
		if beforeSeq > 0 {
			return r.pool.Query(ctx, baseQ+` WHERE conversation_id=$1 AND seq < $2 ORDER BY seq DESC LIMIT $3`,
				convID, beforeSeq, limit)
		}
		return r.pool.Query(ctx, baseQ+` WHERE conversation_id=$1 ORDER BY seq DESC LIMIT $2`, convID, limit)
	}()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Message, 0, limit)
	for rows.Next() {
		m := &Message{}
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content,
			&m.Seq, &m.Mentions, &m.ReplyTo, &m.IsRecalled, &m.EditedAt, &m.EditCount, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
