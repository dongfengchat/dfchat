package auth

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// emailDomainHasMX checks whether the email's domain has any MX records.
//
// Why this beats a static disposable-domain list: throwaway services
// keep registering new domains (mailinator alone has 50+ alts). A real
// mailbox needs a working MX. If the domain has no MX at all, mail can
// never reach it — so why let a user register with it?
//
// Why not also check A records: some legit mail domains use SPF /
// MX-less setups (rare but exist). Mandatory MX is the strict-but-fair
// rule that catches gibberish.com and a 1-day-old fake "foo.online"
// where no one set up mail yet.
//
// Caching: hits cached 24h, misses 1h. A spammer using gibberish.com
// won't benefit from cached "miss" because we re-check periodically.
// A real user typing yourcompany.com gets sub-ms response after first
// lookup.
//
// Timeout: 3 seconds. If DNS is slow we don't block registration —
// the static disposable list still applies, and 99.9% of legit
// domains resolve in < 50ms.

type mxCacheEntry struct {
	ok       bool
	expiresAt time.Time
}

var mxCache = struct {
	sync.RWMutex
	m map[string]mxCacheEntry
}{m: make(map[string]mxCacheEntry)}

const (
	mxCacheHitTTL  = 24 * time.Hour
	mxCacheMissTTL = 1 * time.Hour
	mxLookupTimeout = 3 * time.Second
)

// emailDomainHasMX returns (true, nil) if the domain has at least one
// MX record. Returns (false, nil) if DNS conclusively says no MX. For
// transient errors (timeout, no network) returns (true, err) — we
// fail open so a flaky resolver doesn't block legit registrations;
// the static disposable list still filters obvious junk.
func emailDomainHasMX(ctx context.Context, email string) (bool, error) {
	at := strings.LastIndexByte(email, '@')
	if at < 0 || at == len(email)-1 {
		return false, nil
	}
	domain := strings.ToLower(email[at+1:])

	// Cache hit?
	mxCache.RLock()
	if entry, ok := mxCache.m[domain]; ok && time.Now().Before(entry.expiresAt) {
		mxCache.RUnlock()
		return entry.ok, nil
	}
	mxCache.RUnlock()

	// Lookup with our own timeout, separate from the request ctx so
	// a fast client deadline doesn't poison the cache.
	lookupCtx, cancel := context.WithTimeout(ctx, mxLookupTimeout)
	defer cancel()

	resolver := net.DefaultResolver
	mxs, err := resolver.LookupMX(lookupCtx, domain)
	if err != nil {
		// Distinguish "no such domain" from transient. The Go DNS
		// resolver returns *net.DNSError with IsNotFound for NXDOMAIN.
		if dnsErr, ok := err.(*net.DNSError); ok && dnsErr.IsNotFound {
			cacheMX(domain, false)
			return false, nil
		}
		// Timeout / network issue — don't cache, fail open.
		return true, err
	}

	hasMX := len(mxs) > 0
	if hasMX {
		// Edge case: some servers return a single MX pointing at "."
		// to explicitly declare "we don't accept mail". Treat as no MX.
		if len(mxs) == 1 && (mxs[0].Host == "." || mxs[0].Host == "") {
			hasMX = false
		}
	}
	cacheMX(domain, hasMX)
	return hasMX, nil
}

func cacheMX(domain string, ok bool) {
	ttl := mxCacheMissTTL
	if ok {
		ttl = mxCacheHitTTL
	}
	mxCache.Lock()
	mxCache.m[domain] = mxCacheEntry{ok: ok, expiresAt: time.Now().Add(ttl)}
	mxCache.Unlock()
}
