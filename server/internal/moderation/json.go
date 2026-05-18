package moderation

import "encoding/json"

// Indirected so tests / future swaps can plug in a stricter parser.
// Kept tiny because the body we parse is itself tiny (single JSON line).
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
