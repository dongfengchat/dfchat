package message

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	ConvTypePrivate int16 = 1
	ConvTypeGroup   int16 = 2

	// How long after sending a message the author may still recall it.
	RecallWindowSeconds = 120
	// How long after sending the author may still edit the text body.
	// Generous compared to recall (5 min) — typos are common, and a
	// stricter window forces users to recall+resend, which spams the UI.
	EditWindowSeconds = 300

	// MentionEveryone is the sentinel user_id stored in messages.mentions
	// for @everyone. Real users.id values are positive integers, so 0
	// is a safe out-of-band marker that survives existing BIGINT[]
	// storage without a migration.
	MentionEveryone int64 = 0
)

type Message struct {
	ID             int64           `json:"id,string"`
	ConversationID string          `json:"conversationId"`
	SenderID       int64           `json:"senderId,string"`
	Type           string          `json:"type"`
	Content        json.RawMessage `json:"content"`
	Seq            int64           `json:"seq"`
	Mentions       []int64         `json:"mentions,omitempty"`
	ReplyTo        *int64          `json:"replyTo,omitempty"`
	Reactions      []ReactionCount `json:"reactions,omitempty"`
	IsRecalled     bool            `json:"isRecalled"`
	EditedAt       *time.Time      `json:"editedAt,omitempty"`
	EditCount      int             `json:"editCount,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
}

// ReactionCount summarises a single emoji on a single message.
type ReactionCount struct {
	Emoji     string  `json:"emoji"`
	Count     int     `json:"count"`
	UserIDs   []int64 `json:"userIds,omitempty"` // who reacted; helps client toggle "did I react?"
}

type Pin struct {
	ConversationID string    `json:"conversationId"`
	MessageID      int64     `json:"messageId,string"`
	PinnedBy       int64     `json:"pinnedBy,string"`
	PinnedAt       time.Time `json:"pinnedAt"`
	// Snapshot of the message at pin time so the client can render a preview.
	Message *Message `json:"message,omitempty"`
}

// PrivateConvID returns the canonical conversation id for a pair of users (order-independent).
func PrivateConvID(a, b int64) string {
	if a > b {
		a, b = b, a
	}
	return fmt.Sprintf("p_%d_%d", a, b)
}

// recalledContent is the body emitted in place of the original payload
// for recalled messages. We keep `isRecalled=true` alongside so the
// client UI knows to render "此消息已撤回".
var recalledContent = json.RawMessage(`{}`)

// RedactRecalled mutates the slice in place, replacing the content of
// any message with is_recalled=true. Mentions / replyTo / reactions are
// kept (they're already-public metadata: who was @mentioned, what was
// quoted). The original text body would otherwise leak via /list and
// /pins endpoints despite the "recalled" flag.
func RedactRecalled(msgs []*Message) {
	for _, m := range msgs {
		if m != nil && m.IsRecalled {
			m.Content = recalledContent
		}
	}
}
