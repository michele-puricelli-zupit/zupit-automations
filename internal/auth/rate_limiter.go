package auth

import (
	"sync"
	"time"
)

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
	limit   int
	window  time.Duration
}

type rateEntry struct {
	count int
	reset time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{entries: make(map[string]rateEntry), limit: limit, window: window}
}

func (l *rateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry.reset.IsZero() || !now.Before(entry.reset) {
		entry = rateEntry{reset: now.Add(l.window)}
	}
	entry.count++
	l.entries[key] = entry
	if len(l.entries) > 2048 {
		for candidate, value := range l.entries {
			if !now.Before(value.reset) {
				delete(l.entries, candidate)
			}
		}
	}
	return entry.count <= l.limit
}
