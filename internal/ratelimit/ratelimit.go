// Package ratelimit provides a fixed-window / token-bucket style rate limiter
// keyed by an arbitrary string (e.g. client IP or device key). It is safe for
// concurrent use.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a token-bucket rate limiter per key. It is safe for concurrent use.
type Limiter struct {
	mu sync.Mutex
	// capacity is the maximum token balance (burst) per key.
	capacity float64
	// refillPerSec is tokens added per second per key.
	refillPerSec float64
	// buckets holds the current token balance per key.
	buckets map[string]float64
	// lastRefill holds the last token refill time per key.
	lastRefill map[string]time.Time
	now        func() time.Time

	// maxKeys caps the number of tracked keys; when exceeded, keys idle for
	// longer than idleTTL are evicted so a flood of distinct keys (e.g. many
	// client IPs on a public server) cannot grow the map without bound.
	maxKeys int
	// idleTTL is how long a key may go unused before it becomes evictable.
	idleTTL time.Duration
}

const (
	defaultMaxKeys = 10000
	defaultIdleTTL = 15 * time.Minute
)

// New creates a Limiter. capacity is the burst; refillPerSec is the steady
// refill rate. capacity=0 denies everything; refillPerSec<=0 gives burst only.
func New(capacity, refillPerSec float64) *Limiter {
	return &Limiter{
		capacity:     capacity,
		refillPerSec: refillPerSec,
		buckets:      make(map[string]float64),
		lastRefill:   make(map[string]time.Time),
		now:          time.Now,
		maxKeys:      defaultMaxKeys,
		idleTTL:      defaultIdleTTL,
	}
}

// Allow reports whether key may proceed, consuming one token when true.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.capacity <= 0 {
		return false
	}

	now := l.now()
	l.evictIdle(now)
	tokens, ok := l.buckets[key]
	if !ok {
		tokens = l.capacity
		l.buckets[key] = tokens
	}
	last := l.lastRefill[key]
	if !last.IsZero() {
		elapsed := now.Sub(last).Seconds()
		if elapsed > 0 {
			add := elapsed * l.refillPerSec
			tokens += add
			if tokens > l.capacity {
				tokens = l.capacity
			}
		}
	}
	l.lastRefill[key] = now
	l.buckets[key] = tokens

	if tokens >= 1 {
		l.buckets[key] = tokens - 1
		return true
	}
	return false
}

// evictIdle drops tracked keys that were unused for longer than idleTTL, but
// only once the map has reached maxKeys — the common small deployment never
// pays the sweep cost. Idle keys are the ones whose persisted tokens are full
// anyway, so evicting them loses no burst capacity.
func (l *Limiter) evictIdle(now time.Time) {
	if len(l.buckets) < l.maxKeys {
		return
	}
	for key, last := range l.lastRefill {
		if now.Sub(last) > l.idleTTL {
			delete(l.buckets, key)
			delete(l.lastRefill, key)
		}
	}
}

// ShouldLimit reports whether a request from key should be rejected as
// rate-limited: it is true only when a limiter is configured AND the key has
// exceeded its budget. A nil limiter (rate limiting disabled) never limits.
// This lets middleware pass through requests when limiting is not configured.
func ShouldLimit(l *Limiter, key string) bool {
	if l == nil {
		return false
	}
	return !l.Allow(key)
}
