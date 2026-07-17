package ratelimit

import (
	"time"
)

type TokenBucket struct {
	capacity   int
	tokens     int
	rate       time.Duration
	lastRefill time.Time
}

func NewTokenBucket(capacity int, rate time.Duration) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		tokens:     capacity,
		rate:       rate,
		lastRefill: time.Now(),
	}
}

func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill)

	newTokens := int(elapsed / tb.rate)
	if newTokens > 0 {
		tb.tokens += newTokens
		if tb.tokens > tb.capacity {
			tb.tokens = tb.capacity
		}
		tb.lastRefill = tb.lastRefill.Add(time.Duration(newTokens) * tb.rate)
	}
}

func (tb *TokenBucket) Take() bool {
	tb.refill()

	if tb.tokens > 0 {
		tb.tokens--
		return true
	}
	return false
}
