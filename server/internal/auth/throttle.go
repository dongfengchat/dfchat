package auth

import (
	"sync"
	"time"
)

// In-memory throttling for registration / draw abuse and login lockout.
//
// Why not Redis: the API is a single monolith, attackers don't care
// about our restart windows, and a 100k-entry map is < 10MB. We sweep
// stale entries every 5 minutes so memory stays flat. If we ever scale
// out, swap the map for Redis under the same interface.
//
// Why not just lean on RateLimitStrict: per-IP rate limit is 1 r/s with
// burst 3 — it stops bursts but happily lets a patient script do 86400
// registrations / day from one IP. These counters add an absolute
// 24-hour cap, which is what spam actually cares about.

// ipDailyCap returns true and rejects if the (ip, action) pair has
// already hit `limit` actions in the past 24 hours. Otherwise it
// increments the counter and returns false (caller proceeds).
//
// counters reset 24h after first hit (per-IP, per-action).
//
// Typical limits:
//   register: 5 / 24h   — humans register from same IP once or twice
//   draw:     30 / 24h  — humans draw a few times max; 30 covers
//                         shared Wi-Fi (cafes, offices, family).

type ipBucket struct {
	count   int
	resetAt time.Time
}

var ipDailyState = struct {
	sync.Mutex
	m map[string]map[string]*ipBucket // ip -> action -> bucket
}{m: make(map[string]map[string]*ipBucket)}

// Sweep stale entries every 5 minutes so the map can't grow forever.
func init() {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		for range t.C {
			now := time.Now()
			ipDailyState.Lock()
			for ip, actions := range ipDailyState.m {
				for action, b := range actions {
					if now.After(b.resetAt) {
						delete(actions, action)
					}
				}
				if len(actions) == 0 {
					delete(ipDailyState.m, ip)
				}
			}
			ipDailyState.Unlock()
		}
	}()
}

// hitIPDailyCap increments the (ip, action) counter and reports whether
// it has now exceeded `limit`. Caller should reject before doing any
// expensive work when this returns over=true.
func hitIPDailyCap(ip, action string, limit int) (count int, over bool) {
	ipDailyState.Lock()
	defer ipDailyState.Unlock()

	actions, ok := ipDailyState.m[ip]
	if !ok {
		actions = make(map[string]*ipBucket)
		ipDailyState.m[ip] = actions
	}
	b, ok := actions[action]
	now := time.Now()
	if !ok || now.After(b.resetAt) {
		b = &ipBucket{resetAt: now.Add(24 * time.Hour)}
		actions[action] = b
	}
	b.count++
	return b.count, b.count > limit
}

// peekIPDailyCap returns the current count without incrementing. Used
// for surfacing remaining quota.
func peekIPDailyCap(ip, action string) int {
	ipDailyState.Lock()
	defer ipDailyState.Unlock()
	actions, ok := ipDailyState.m[ip]
	if !ok {
		return 0
	}
	b, ok := actions[action]
	if !ok || time.Now().After(b.resetAt) {
		return 0
	}
	return b.count
}

// rollbackIPDailyCap walks the counter back by 1. Used when an action
// failed AFTER we incremented (e.g. db error during register), so we
// don't burn the user's quota for our own bug.
func rollbackIPDailyCap(ip, action string) {
	ipDailyState.Lock()
	defer ipDailyState.Unlock()
	actions, ok := ipDailyState.m[ip]
	if !ok {
		return
	}
	b, ok := actions[action]
	if !ok {
		return
	}
	if b.count > 0 {
		b.count--
	}
}

// ===================================================================
// Per-account login lockout
// ===================================================================
//
// After 5 failed login attempts on the same login string (username /
// email / account_no) within a 15-minute window, the account is locked
// for 15 more minutes. A successful login resets the counter.
//
// Keyed by lower-cased login string so "ALICE" and "alice" share state.
//
// Note this is independent of the per-IP RateLimitStrict — that stops
// an attacker hammering ONE IP. This stops them hammering ONE ACCOUNT
// from MANY IPs (botnet password spray against your CEO's account_no).

const (
	loginFailWindow   = 15 * time.Minute
	loginFailLimit    = 5
	loginLockDuration = 15 * time.Minute
)

type loginFailBucket struct {
	count       int
	firstFailAt time.Time
	lockedUntil time.Time
}

var loginFailState = struct {
	sync.Mutex
	m map[string]*loginFailBucket // lower(login) -> bucket
}{m: make(map[string]*loginFailBucket)}

func init() {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		for range t.C {
			now := time.Now()
			loginFailState.Lock()
			for k, b := range loginFailState.m {
				// Stale entries: window expired AND lockout expired.
				if now.Sub(b.firstFailAt) > loginFailWindow && now.After(b.lockedUntil) {
					delete(loginFailState.m, k)
				}
			}
			loginFailState.Unlock()
		}
	}()
}

// isLoginLocked reports whether the given login is currently locked
// (and how long left). If not locked but the window expired, the
// counter is reset transparently.
func isLoginLocked(login string) (bool, time.Duration) {
	key := normalizeLoginKey(login)
	loginFailState.Lock()
	defer loginFailState.Unlock()
	b, ok := loginFailState.m[key]
	if !ok {
		return false, 0
	}
	now := time.Now()
	if now.Before(b.lockedUntil) {
		return true, b.lockedUntil.Sub(now)
	}
	// Lockout expired; reset for fresh tries.
	if now.Sub(b.firstFailAt) > loginFailWindow {
		delete(loginFailState.m, key)
	}
	return false, 0
}

// recordLoginFailure increments the failure counter for `login`. If it
// crosses the limit, the bucket is locked for loginLockDuration.
func recordLoginFailure(login string) {
	key := normalizeLoginKey(login)
	loginFailState.Lock()
	defer loginFailState.Unlock()
	now := time.Now()
	b, ok := loginFailState.m[key]
	if !ok || now.Sub(b.firstFailAt) > loginFailWindow {
		b = &loginFailBucket{firstFailAt: now}
		loginFailState.m[key] = b
	}
	b.count++
	if b.count >= loginFailLimit {
		b.lockedUntil = now.Add(loginLockDuration)
	}
}

// recordLoginSuccess clears the failure counter for `login`. Called
// after a successful authentication so honest typos don't accumulate.
func recordLoginSuccess(login string) {
	key := normalizeLoginKey(login)
	loginFailState.Lock()
	defer loginFailState.Unlock()
	delete(loginFailState.m, key)
}

func normalizeLoginKey(s string) string {
	// Case-insensitive bucket key so "ALICE" / "alice" share state.
	// Trim spaces so " alice " and "alice" share too.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		out = append(out, c)
	}
	return string(out)
}
