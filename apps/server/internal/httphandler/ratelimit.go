package httphandler

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is an in-memory token bucket keyed by client IP. Used to
// throttle unauthenticated endpoints (login, setup) against brute-force.
//
// Defaults: capacity 5, refill 1 token per minute. A failed attempt
// consumes one token; a successful attempt resets the IP's bucket to full
// (so a legit user who mistyped a password once isn't locked out by
// residual drain). Entries unused for 30 minutes are GC'd on next Allow.
type rateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	capacity int
	refill   time.Duration
	idleTTL  time.Duration
	lastGC   time.Time
}

type bucket struct {
	tokens     int
	lastRefill time.Time
	lastUsed   time.Time
}

func newLoginRateLimiter() *rateLimiter {
	return &rateLimiter{
		buckets:  make(map[string]*bucket),
		capacity: 5,
		refill:   time.Minute,
		idleTTL:  30 * time.Minute,
		lastGC:   time.Now(),
	}
}

// Allow reports whether the caller at ip should be permitted. It does NOT
// consume a token — call Consume after a failed attempt, or Reset after a
// successful one.
func (rl *rateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.gcLocked()
	b := rl.getLocked(ip)
	rl.refillLocked(b)
	return b.tokens > 0
}

// Consume decrements the bucket for ip (clamped at 0).
func (rl *rateLimiter) Consume(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b := rl.getLocked(ip)
	rl.refillLocked(b)
	if b.tokens > 0 {
		b.tokens--
	}
	b.lastUsed = time.Now()
}

// Reset refills the bucket for ip. Called after a successful authentication.
func (rl *rateLimiter) Reset(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b := rl.getLocked(ip)
	b.tokens = rl.capacity
	b.lastRefill = time.Now()
	b.lastUsed = time.Now()
}

// RetryAfter returns how long the caller should wait before their next token
// is available. Returns 0 if tokens are already available.
func (rl *rateLimiter) RetryAfter(ip string) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b := rl.getLocked(ip)
	rl.refillLocked(b)
	if b.tokens > 0 {
		return 0
	}
	wait := rl.refill - time.Since(b.lastRefill)
	if wait < 0 {
		wait = rl.refill
	}
	return wait
}

func (rl *rateLimiter) getLocked(ip string) *bucket {
	b, ok := rl.buckets[ip]
	if !ok {
		b = &bucket{tokens: rl.capacity, lastRefill: time.Now(), lastUsed: time.Now()}
		rl.buckets[ip] = b
	}
	return b
}

func (rl *rateLimiter) refillLocked(b *bucket) {
	if b.tokens >= rl.capacity {
		b.lastRefill = time.Now()
		return
	}
	elapsed := time.Since(b.lastRefill)
	if elapsed >= rl.refill {
		add := int(elapsed / rl.refill)
		b.tokens += add
		if b.tokens > rl.capacity {
			b.tokens = rl.capacity
		}
		b.lastRefill = b.lastRefill.Add(time.Duration(add) * rl.refill)
	}
}

func (rl *rateLimiter) gcLocked() {
	if time.Since(rl.lastGC) < 5*time.Minute {
		return
	}
	cutoff := time.Now().Add(-rl.idleTTL)
	for k, b := range rl.buckets {
		if b.lastUsed.Before(cutoff) {
			delete(rl.buckets, k)
		}
	}
	rl.lastGC = time.Now()
}

// clientIP returns the caller's IP as a string suitable for rate-limit
// keying. Prefers the leftmost X-Forwarded-For entry when present (we run
// behind Caddy on the default install), falling back to RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// XFF is a comma-separated list; the leftmost is the original client.
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
