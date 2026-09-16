package gateway

import (
	"sync"
	"time"
)

type routeLimiter struct {
	mu         sync.Mutex
	capacity   float64
	refillRate float64
	tokens     float64
	updatedAt  time.Time
}

func newRouteLimiter(capacity int, refill float64) *routeLimiter {
	return &routeLimiter{capacity: float64(capacity), refillRate: refill, tokens: float64(capacity), updatedAt: time.Now()}
}

func (limiter *routeLimiter) allow(now time.Time) bool {
	if limiter == nil {
		return true
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.tokens += now.Sub(limiter.updatedAt).Seconds() * limiter.refillRate
	if limiter.tokens > limiter.capacity {
		limiter.tokens = limiter.capacity
	}
	limiter.updatedAt = now
	if limiter.tokens < 1 {
		return false
	}
	limiter.tokens--
	return true
}
