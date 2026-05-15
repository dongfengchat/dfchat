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
