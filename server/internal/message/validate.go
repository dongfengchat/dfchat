package message

import (
	"encoding/json"
	"errors"
	"unicode/utf8"
)

// Hard caps applied to every send. The JSON payload limits below are
// the **raw** bytes accepted by the API — not the rendered/displayed
// length. text-message content is capped tighter at the field level
// inside validateContent().
const (
	maxContentBytes  = 16 * 1024 // 16 KB JSON payload; rich-media payloads stay slim
	maxTextLen       = 4000      // chars (runes), Telegram is 4096 — matched approx
	maxMentionsPerMsg = 32
)

// allowedMessageTypes is the whitelist of client-settable types. Anything
// outside this set is rejected so clients can't forge "system" messages
// (server-only, used for "X joined the group" etc.).
var allowedMessageTypes = map[string]struct{}{
	"text":       {},
	"image":      {},
	"file":       {},
	"audio":      {},
	"video":      {},
	"sticker":    {},
	"reply":      {}, // a quote-reply payload
	"call":       {}, // call summary card pushed after WebRTC ends
	"livestream": {}, // live-stream announcement card
}

var (
	errContentTooLarge = errors.New("content payload too large")
	errContentBadJSON  = errors.New("content must be valid JSON")
	errTypeNotAllowed  = errors.New("message type not allowed")
	errTextTooLong     = errors.New("text too long")
	errTextEmpty       = errors.New("text must be non-empty")
	errTextInvalidUTF8 = errors.New("text must be valid UTF-8")
)

// validateContent checks the JSON shape of a client-supplied message
// content against its declared type. Rejects:
//   - payloads over maxContentBytes (cheap DoS / fat-finger guard)
//   - types not in allowedMessageTypes (no forging system messages)
//   - malformed JSON (must at least be a valid JSON object)
//   - for text: empty / >maxTextLen runes / non-UTF-8
//
// We don't deep-validate image/file/etc. payload schemas here — those
// reference URL/MIME data that the file subsystem already validates
// at upload time, and the renderer is hardened against unknown keys.
func validateContent(typ string, raw json.RawMessage) error {
	if len(raw) > maxContentBytes {
		return errContentTooLarge
	}
	if _, ok := allowedMessageTypes[typ]; !ok {
		return errTypeNotAllowed
	}
	// Must be a valid JSON object (or array, but we use objects). This
	// also strips any chance of "raw" being a number/string injection.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return errContentBadJSON
	}

	if typ == "text" {
		var body struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return errContentBadJSON
		}
		if body.Text == "" {
			return errTextEmpty
		}
		if !utf8.ValidString(body.Text) {
			return errTextInvalidUTF8
		}
		if utf8.RuneCountInString(body.Text) > maxTextLen {
			return errTextTooLong
		}
	}
	return nil
}
