// Package wsbus is a tiny per-user fan-out bus.
// MVP-only: single-process, in-memory. Replace with NATS subjects
// when the gateway moves to its own process (design doc §5.3.2).
package wsbus

import (
	"sync"
)

type Event struct {
	UserID  int64  `json:"-"`
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

type Bus struct {
	mu       sync.RWMutex
	subs     map[int64]map[int]chan Event
	nextSubID int
}

func New() *Bus {
	return &Bus{subs: make(map[int64]map[int]chan Event)}
}

// Subscribe returns a buffered channel of events delivered to userID, plus
// an unsubscribe function. Buffer is small; slow consumers drop events.
func (b *Bus) Subscribe(userID int64) (<-chan Event, func()) {
	ch := make(chan Event, 32)
	b.mu.Lock()
	id := b.nextSubID
	b.nextSubID++
	if b.subs[userID] == nil {
		b.subs[userID] = make(map[int]chan Event)
	}
	b.subs[userID][id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if m, ok := b.subs[userID]; ok {
			delete(m, id)
			if len(m) == 0 {
				delete(b.subs, userID)
			}
		}
		b.mu.Unlock()
		close(ch)
	}
}

// Publish delivers ev to all subscribers of userID. Non-blocking: drops
// events for subscribers whose buffer is full.
func (b *Bus) Publish(userID int64, ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs[userID] {
		select {
		case ch <- ev:
		default:
			// slow consumer — drop
		}
	}
}

// HasSubscribers reports whether at least one WS connection is open for userID.
// Used by the presence endpoint and friend list to surface online state.
func (b *Bus) HasSubscribers(userID int64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[userID]) > 0
}

// OnlineUserIDs returns a snapshot of the user IDs that have at least one
// active subscriber. Caller-allocated; safe to mutate.
func (b *Bus) OnlineUserIDs() []int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ids := make([]int64, 0, len(b.subs))
	for id, m := range b.subs {
		if len(m) > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}
