package main

import (
	"sync"
	"time"
)

// limiter is a per-key fixed-window rate limiter. A single process is assumed;
// for multiple instances put this in shared storage (Redis or similar).
type limiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	buckets map[string]*windowBucket
	calls   int
}

type windowBucket struct {
	start time.Time
	count int
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{
		limit:   limit,
		window:  window,
		buckets: make(map[string]*windowBucket),
	}
}

func (l *limiter) allow(key string) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls++
	if l.calls%1024 == 0 {
		for k, b := range l.buckets {
			if now.Sub(b.start) > l.window {
				delete(l.buckets, k)
			}
		}
	}

	b, seen := l.buckets[key]
	if !seen || now.Sub(b.start) >= l.window {
		l.buckets[key] = &windowBucket{start: now, count: 1}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}
