package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimit returns gin middleware that throttles requests per client IP.
//
// `rps` is the steady-state rate (requests per second) and `burst` is the
// largest momentary spike allowed. A 5/10 setup means "5 req/s steady, up
// to 10 in a brief burst" which is generous for chat UIs and tight enough
// to make scripted abuse painful.
//
// Implementation: in-memory map of IP → *rate.Limiter. We don't use Redis
// — the server is a single monolith and the map is reset on restart,
// which is fine for the use case (rate-limit, not quota). We periodically
// sweep idle entries so the map can't grow forever.
func RateLimit(rps float64, burst int) gin.HandlerFunc {
	type entry struct {
		lim  *rate.Limiter
		seen time.Time
	}
	var (
		mu      sync.Mutex
		buckets = make(map[string]*entry)
	)

	// Sweep every 5 min, drop entries idle > 15 min.
	go func() {
		t := time.NewTicker(5 * time.Minute)
		for range t.C {
			cutoff := time.Now().Add(-15 * time.Minute)
			mu.Lock()
			for k, e := range buckets {
				if e.seen.Before(cutoff) {
					delete(buckets, k)
				}
			}
			mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		ip := c.ClientIP()
		mu.Lock()
		e, ok := buckets[ip]
		if !ok {
			e = &entry{lim: rate.NewLimiter(rate.Limit(rps), burst)}
			buckets[ip] = e
		}
		e.seen = time.Now()
		allowed := e.lim.Allow()
		mu.Unlock()

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code":    42901,
				"message": "rate limit exceeded, slow down",
			})
			return
		}
		c.Next()
	}
}

// RateLimitStrict is for sensitive endpoints (login, register, password
// reset): 1 req/s steady, burst 3. Per-IP, same sweep semantics.
func RateLimitStrict() gin.HandlerFunc {
	return RateLimit(1, 3)
}

// RateLimitPerUser is the same algorithm as RateLimit but buckets by
// the authenticated user id (gin context "userID") rather than client
// IP. Use it on logged-in endpoints where a single user behind a NAT
// shouldn't be able to outrun their own limit by switching IPs, and
// equally where shared NAT users shouldn't share each other's limit
// (multiple roommates on one IP all chatting at once).
//
// Falls back to ClientIP if userID isn't set (defensive — every wired
// route should already be behind RequireAuth, so this branch is dead).
func RateLimitPerUser(rps float64, burst int) gin.HandlerFunc {
	type entry struct {
		lim  *rate.Limiter
		seen time.Time
	}
	var (
		mu      sync.Mutex
		buckets = make(map[string]*entry)
	)
	go func() {
		t := time.NewTicker(5 * time.Minute)
		for range t.C {
			cutoff := time.Now().Add(-15 * time.Minute)
			mu.Lock()
			for k, e := range buckets {
				if e.seen.Before(cutoff) {
					delete(buckets, k)
				}
			}
			mu.Unlock()
		}
	}()
	return func(c *gin.Context) {
		key := c.ClientIP()
		if v, ok := c.Get("userID"); ok {
			if uid, ok := v.(int64); ok && uid > 0 {
				key = "u:" + strconv.FormatInt(uid, 10)
			}
		}
		mu.Lock()
		e, ok := buckets[key]
		if !ok {
			e = &entry{lim: rate.NewLimiter(rate.Limit(rps), burst)}
			buckets[key] = e
		}
		e.seen = time.Now()
		allowed := e.lim.Allow()
		mu.Unlock()
		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code":    42901,
				"message": "rate limit exceeded, slow down",
			})
			return
		}
		c.Next()
	}
}
