package auth

import (
	"sync"
	"time"
)

// RateLimiter is a sliding-window limiter keyed by IP, held in memory with
// periodic cleanup of idle keys.
type RateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

// NewRateLimiter allows `max` attempts per `window` per key.
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	return &RateLimiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *RateLimiter) trim(key string, now time.Time) []time.Time {
	cutoff := now.Add(-l.window)
	h := l.hits[key]
	i := 0
	for i < len(h) && h[i].Before(cutoff) {
		i++
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = h
	}
	return h
}

// Allow records an attempt and reports whether it is within the limit.
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	h := l.trim(key, now)
	if len(h) >= l.max {
		return false
	}
	l.hits[key] = append(h, now)
	return true
}

// Reset clears a key (after a successful login).
func (l *RateLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}

// Cleanup drops expired entries; call periodically.
func (l *RateLimiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k := range l.hits {
		l.trim(k, now)
	}
}
