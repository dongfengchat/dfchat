package live

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// Signed HLS playback URLs. The shape is:
//
//   https://dfchat.chat/hls/<stream_key>.m3u8?token=<hex>&exp=<unix_seconds>
//
// where token = hex(HMAC-SHA256(secret, stream_key + "|" + exp)).
//
// Why this exists: without a per-viewer signature on the playback URL,
// anyone who learned a stream URL once (or guessed it) could fetch the
// HLS playlist + segments forever, including for "test-mode" private
// rooms. We don't have an `on_play` SRS hook wired, so the only place
// to enforce auth is at nginx — auth_request to api validates the
// signature before forwarding the request to SRS.
//
// Lifetime: tokens are valid for playTokenTTL. Each viewer hits
// publicDetail / ownerDetail to receive a fresh URL on stream entry,
// and when the existing token nears expiry the client refetches. We
// deliberately don't bind the token to a specific user — that would
// require the player to send Authorization headers on every TS fetch,
// which HLS players don't generally support. The URL itself IS the
// capability; we accept that a determined viewer can re-share a live
// URL within the TTL window. That's the standard HLS-with-signed-URL
// trade-off everyone (CloudFront / Akamai / Mux) makes.

const (
	// playTokenTTL bounds how long a single playback URL is valid.
	// 1 hour balances "viewer doesn't need to refresh constantly" vs
	// "shared-link attack window is small". Live streams typically
	// run 1-2 h, so most viewers won't see a refresh during a single
	// session.
	playTokenTTL = 1 * time.Hour
	// playTokenSkew tolerates clock drift between client + server in
	// the verify path. ±30s is plenty (NTP keeps both inside 1s in
	// practice).
	playTokenSkew = 30 * time.Second
)

var (
	errPlayTokenMissing = errors.New("play token missing")
	errPlayTokenBadExp  = errors.New("play token exp malformed")
	errPlayTokenExpired = errors.New("play token expired")
	errPlayTokenBadSig  = errors.New("play token signature mismatch")
)

// signPlayToken returns (token, expUnix) for the given stream_key. The
// secret should be a stable per-deployment value (we reuse LIVE_SRS_SECRET
// — already required ≥32 chars at boot, so it's strong enough HMAC key).
func signPlayToken(streamKey, secret string, now time.Time) (token string, expUnix int64) {
	expUnix = now.Add(playTokenTTL).Unix()
	token = hmacFor(streamKey, expUnix, secret)
	return token, expUnix
}

// verifyPlayToken returns nil if the token is valid for the given
// stream_key + exp tuple, or one of the err* values describing why
// it isn't. Constant-time HMAC compare via hmac.Equal.
func verifyPlayToken(streamKey, secret, token, expStr string, now time.Time) error {
	if token == "" || expStr == "" {
		return errPlayTokenMissing
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return errPlayTokenBadExp
	}
	// Compare against the signature we'd produce now. hmac.Equal is
	// constant-time. The mac comparison happens BEFORE the exp check
	// to avoid a timing side-channel that leaks "token shape valid but
	// expired" vs "token shape invalid".
	want := hmacFor(streamKey, exp, secret)
	if !hmac.Equal([]byte(want), []byte(token)) {
		return errPlayTokenBadSig
	}
	if exp+int64(playTokenSkew.Seconds()) < now.Unix() {
		return errPlayTokenExpired
	}
	return nil
}

func hmacFor(streamKey string, exp int64, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(streamKey))
	m.Write([]byte{'|'})
	m.Write([]byte(strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(m.Sum(nil))
}
