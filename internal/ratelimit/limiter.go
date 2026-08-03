package ratelimit

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Limiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

type BucketState struct {
	Capacity int    `json:"capacity"`
	Tokens   int    `json:"tokens"`
	Rate     string `json:"rate"`
}

type LimitDecision struct {
	Allowed bool        `json:"allowed"`
	Bucket  BucketState `json:"bucket"`
}

type DetailedLimiter interface {
	AllowWithState(ctx context.Context, key string) (LimitDecision, error)
}

type TokenBucketLimiter struct {
	defaultCapacity int
	defaultRate     time.Duration
	storage         Storage
	configMu        sync.RWMutex
}

func NewTokenBucketLimiter(defaultCapacity int, defaultRate time.Duration, storage Storage) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		defaultCapacity: defaultCapacity,
		defaultRate:     defaultRate,
		storage:         storage,
	}
}

func (l *TokenBucketLimiter) Allow(ctx context.Context, key string) (bool, error) {
	decision, err := l.AllowWithState(ctx, key)
	return decision.Allowed, err
}

func (l *TokenBucketLimiter) AllowWithState(ctx context.Context, key string) (LimitDecision, error) {
	l.configMu.RLock()
	defer l.configMu.RUnlock()

	var decision LimitDecision
	err := l.storage.Update(ctx, key, func(b *TokenBucket) (*TokenBucket, error) {
		if b == nil {
			b = NewTokenBucket(l.defaultCapacity, l.defaultRate)
		}
		decision.Allowed = b.Take()
		decision.Bucket = bucketState(b)
		return b, nil
	})

	return decision, err
}

func (l *TokenBucketLimiter) Snapshot(ctx context.Context, key string) (BucketState, error) {
	l.configMu.RLock()
	defer l.configMu.RUnlock()

	state := BucketState{
		Capacity: l.defaultCapacity,
		Tokens:   l.defaultCapacity,
		Rate:     l.defaultRate.String(),
	}

	err := l.storage.Update(ctx, key, func(b *TokenBucket) (*TokenBucket, error) {
		if b == nil {
			b = NewTokenBucket(l.defaultCapacity, l.defaultRate)
		}
		b.refill()
		state = bucketState(b)
		return b, nil
	})

	return state, err
}

func (l *TokenBucketLimiter) Reset(ctx context.Context, key string, capacity int) (BucketState, error) {
	if capacity < 1 {
		return BucketState{}, fmt.Errorf("rate limit capacity must be positive")
	}

	l.configMu.RLock()
	defer l.configMu.RUnlock()

	bucket := NewTokenBucket(capacity, l.defaultRate)
	if err := l.storage.Set(ctx, key, bucket); err != nil {
		return BucketState{}, err
	}

	return bucketState(bucket), nil
}

func (l *TokenBucketLimiter) Reconfigure(ctx context.Context, capacity int, rate time.Duration) error {
	if capacity < 1 || rate <= 0 {
		return fmt.Errorf("rate limit capacity and rate must be positive")
	}

	l.configMu.Lock()
	defer l.configMu.Unlock()

	storage, ok := l.storage.(ReconfigurableStorage)
	if !ok {
		return fmt.Errorf("rate limit storage does not support reconfiguration")
	}
	if err := storage.Reconfigure(ctx, capacity, rate); err != nil {
		return err
	}

	l.defaultCapacity = capacity
	l.defaultRate = rate
	return nil
}

func bucketState(bucket *TokenBucket) BucketState {
	return BucketState{
		Capacity: bucket.capacity,
		Tokens:   bucket.tokens,
		Rate:     bucket.rate.String(),
	}
}

func setRateLimitHeaders(w http.ResponseWriter, state BucketState) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(state.Capacity))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(state.Tokens))
	w.Header().Set("X-RateLimit-Rate", state.Rate)
}

func RateLimitMiddleware(limiter Limiter, keyFunc func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			allowed := false
			var err error

			if detailed, ok := limiter.(DetailedLimiter); ok {
				var decision LimitDecision
				decision, err = detailed.AllowWithState(r.Context(), key)
				allowed = decision.Allowed
				setRateLimitHeaders(w, decision.Bucket)
			} else {
				allowed, err = limiter.Allow(r.Context(), key)
			}

			if err != nil {
				log.Printf("Rate limiter error: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{ "code": 429, "message": "Rate limit exceeded" }`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
