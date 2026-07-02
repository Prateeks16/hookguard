package auth

import (
	"sync"
	"time"
)

// Limiter is a fixed-window counter per key, sufficient for a single-instance
// self-hosted console (DESIGN.md §5.1) — no distributed store needed.
type Limiter struct {
	max    int
	window time.Duration

	mu   sync.Mutex
	hits map[string][]time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, hits: make(map[string][]time.Time)}
}

// Allow records a hit for key at now and reports whether it is within the
// limit. When not allowed, retryAfter is how long until the oldest hit in
// the window expires.
func (l *Limiter) Allow(key string, now time.Time) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if len(kept) >= l.max {
		l.hits[key] = kept
		retryAfter = l.window - now.Sub(kept[0])
		return false, retryAfter
	}

	kept = append(kept, now)
	l.hits[key] = kept
	return true, 0
}
