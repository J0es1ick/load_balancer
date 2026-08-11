package ratelimit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync/atomic"
	"time"
)

type Policy struct {
	Capacity        int     `json:"capacity"`
	RefillPerSecond float64 `json:"refill_per_second"`
}

type BucketState struct {
	Capacity        int     `json:"capacity"`
	Tokens          float64 `json:"tokens"`
	RefillPerSecond float64 `json:"refill_per_second"`
	Storage         string  `json:"storage"`
	Degraded        bool    `json:"degraded"`
}

type LimitDecision struct {
	Allowed bool        `json:"allowed"`
	Bucket  BucketState `json:"bucket"`
}

type Store interface {
	Name() string
	Take(ctx context.Context, key string, policy Policy) (LimitDecision, error)
	Peek(ctx context.Context, key string, policy Policy) (BucketState, error)
	Reset(ctx context.Context, key string, policy Policy) (BucketState, error)
	Healthy(ctx context.Context) error
	Cleanup(ctx context.Context, olderThan time.Duration) error
	Close() error
}

type Limiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

type DetailedLimiter interface {
	AllowWithState(ctx context.Context, key string) (LimitDecision, error)
}

type RuntimeSettings struct {
	Enabled          bool
	Policy           Policy
	FailureMode      string
	OperationTimeout time.Duration
	IPv4PrefixBits   int
	IPv6PrefixBits   int
}

type TokenBucketLimiter struct {
	primary  Store
	fallback *LocalStore
	settings atomic.Pointer[RuntimeSettings]
}

func NewTokenBucketLimiter(settings RuntimeSettings, primary Store, fallback *LocalStore) (*TokenBucketLimiter, error) {
	applyRuntimeDefaults(&settings)
	if err := validateSettings(settings); err != nil {
		return nil, err
	}
	if primary == nil {
		return nil, fmt.Errorf("rate limit store is required")
	}
	if fallback == nil {
		fallback = NewLocalStore(64)
	}
	limiter := &TokenBucketLimiter{primary: primary, fallback: fallback}
	limiter.settings.Store(cloneSettings(settings))
	return limiter, nil
}

func (limiter *TokenBucketLimiter) Allow(ctx context.Context, key string) (bool, error) {
	decision, err := limiter.AllowWithState(ctx, key)
	return decision.Allowed, err
}

func (limiter *TokenBucketLimiter) AllowWithState(ctx context.Context, key string) (LimitDecision, error) {
	settings := limiter.Settings()
	key = normalizeClientKey(key, settings)
	if !settings.Enabled {
		return LimitDecision{Allowed: true, Bucket: BucketState{Capacity: settings.Policy.Capacity, Tokens: float64(settings.Policy.Capacity), RefillPerSecond: settings.Policy.RefillPerSecond, Storage: "disabled"}}, nil
	}
	operationContext, cancel := context.WithTimeout(ctx, settings.OperationTimeout)
	decision, err := limiter.primary.Take(operationContext, key, settings.Policy)
	cancel()
	if err == nil {
		return decision, nil
	}

	switch settings.FailureMode {
	case "fail-open":
		return LimitDecision{Allowed: true, Bucket: BucketState{
			Capacity: settings.Policy.Capacity, Tokens: float64(settings.Policy.Capacity),
			RefillPerSecond: settings.Policy.RefillPerSecond, Storage: limiter.primary.Name(), Degraded: true,
		}}, nil
	case "local-fallback":
		fallback, fallbackError := limiter.fallback.Take(ctx, key, settings.Policy)
		fallback.Bucket.Degraded = true
		if fallbackError != nil {
			return LimitDecision{}, fmt.Errorf("primary limiter: %v; local fallback: %w", err, fallbackError)
		}
		return fallback, nil
	default:
		return LimitDecision{}, err
	}
}

func (limiter *TokenBucketLimiter) Snapshot(ctx context.Context, key string) (BucketState, error) {
	settings := limiter.Settings()
	key = normalizeClientKey(key, settings)
	if !settings.Enabled {
		return BucketState{Capacity: settings.Policy.Capacity, Tokens: float64(settings.Policy.Capacity), RefillPerSecond: settings.Policy.RefillPerSecond, Storage: "disabled"}, nil
	}
	operationContext, cancel := context.WithTimeout(ctx, settings.OperationTimeout)
	defer cancel()
	state, err := limiter.primary.Peek(operationContext, key, settings.Policy)
	if err == nil {
		return state, nil
	}
	if settings.FailureMode == "local-fallback" {
		state, fallbackError := limiter.fallback.Peek(ctx, key, settings.Policy)
		state.Degraded = true
		return state, fallbackError
	}
	if settings.FailureMode == "fail-open" {
		return BucketState{Capacity: settings.Policy.Capacity, Tokens: float64(settings.Policy.Capacity), RefillPerSecond: settings.Policy.RefillPerSecond, Storage: limiter.primary.Name(), Degraded: true}, nil
	}
	return BucketState{}, err
}

func (limiter *TokenBucketLimiter) Reset(ctx context.Context, key string, capacity int) (BucketState, error) {
	settings := limiter.Settings()
	key = normalizeClientKey(key, settings)
	if capacity < 1 {
		return BucketState{}, fmt.Errorf("rate limit capacity must be positive")
	}
	policy := settings.Policy
	policy.Capacity = capacity
	operationContext, cancel := context.WithTimeout(ctx, settings.OperationTimeout)
	defer cancel()
	return limiter.primary.Reset(operationContext, key, policy)
}

func (limiter *TokenBucketLimiter) Reconfigure(settings RuntimeSettings) error {
	applyRuntimeDefaults(&settings)
	if err := validateSettings(settings); err != nil {
		return err
	}
	limiter.settings.Store(cloneSettings(settings))
	return nil
}

func (limiter *TokenBucketLimiter) Settings() RuntimeSettings {
	settings := limiter.settings.Load()
	if settings == nil {
		return RuntimeSettings{}
	}
	return *settings
}

func (limiter *TokenBucketLimiter) StorageName() string { return limiter.primary.Name() }

func (limiter *TokenBucketLimiter) LocalStats() LocalStoreStats {
	if primary, ok := limiter.primary.(*LocalStore); ok {
		return primary.Stats()
	}
	return limiter.fallback.Stats()
}

func (limiter *TokenBucketLimiter) Healthy(ctx context.Context) error {
	settings := limiter.Settings()
	if !settings.Enabled {
		return nil
	}
	operationContext, cancel := context.WithTimeout(ctx, settings.OperationTimeout)
	defer cancel()
	return limiter.primary.Healthy(operationContext)
}

func (limiter *TokenBucketLimiter) StartCleanupWorker(ctx context.Context, interval, retention time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				operationContext, cancel := context.WithTimeout(ctx, min(interval, 30*time.Second))
				err := limiter.primary.Cleanup(operationContext, retention)
				cancel()
				if err != nil {
					slog.Error("rate limit cleanup failed", "storage", limiter.primary.Name(), "error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (limiter *TokenBucketLimiter) Close() error { return limiter.primary.Close() }

func validateSettings(settings RuntimeSettings) error {
	if settings.Policy.Capacity < 1 || settings.Policy.RefillPerSecond <= 0 || settings.OperationTimeout <= 0 {
		return fmt.Errorf("rate limit policy values must be positive")
	}
	if settings.FailureMode != "fail-open" && settings.FailureMode != "fail-closed" && settings.FailureMode != "local-fallback" {
		return fmt.Errorf("unknown rate limit failure mode %q", settings.FailureMode)
	}
	if settings.IPv4PrefixBits < 1 || settings.IPv4PrefixBits > 32 || settings.IPv6PrefixBits < 1 || settings.IPv6PrefixBits > 128 {
		return fmt.Errorf("rate limit client prefix bits are invalid")
	}
	return nil
}

func applyRuntimeDefaults(settings *RuntimeSettings) {
	if settings.IPv4PrefixBits == 0 {
		settings.IPv4PrefixBits = 32
	}
	if settings.IPv6PrefixBits == 0 {
		settings.IPv6PrefixBits = 64
	}
}

func normalizeClientKey(key string, settings RuntimeSettings) string {
	address, err := netip.ParseAddr(key)
	if err != nil {
		return key
	}
	address = address.Unmap()
	bits := settings.IPv6PrefixBits
	if address.Is4() {
		bits = settings.IPv4PrefixBits
	}
	return netip.PrefixFrom(address, bits).Masked().String()
}

func cloneSettings(settings RuntimeSettings) *RuntimeSettings { return &settings }

func setRateLimitHeaders(writer http.ResponseWriter, state BucketState) {
	remaining := int(math.Floor(max(0, state.Tokens)))
	writer.Header().Set("X-RateLimit-Limit", strconv.Itoa(state.Capacity))
	writer.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	writer.Header().Set("X-RateLimit-Refill-Per-Second", strconv.FormatFloat(state.RefillPerSecond, 'f', -1, 64))
	writer.Header().Set("X-RateLimit-Storage", state.Storage)
	if state.Degraded {
		writer.Header().Set("X-RateLimit-Degraded", "true")
	}
}

func RateLimitMiddleware(limiter Limiter, keyFunc func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			key := keyFunc(request)
			var allowed bool
			var err error
			if detailed, ok := limiter.(DetailedLimiter); ok {
				var decision LimitDecision
				decision, err = detailed.AllowWithState(request.Context(), key)
				allowed = decision.Allowed
				setRateLimitHeaders(writer, decision.Bucket)
			} else {
				allowed, err = limiter.Allow(request.Context(), key)
			}
			if err != nil {
				slog.ErrorContext(request.Context(), "rate limiter unavailable", "error", err)
				writeError(writer, http.StatusServiceUnavailable, "rate limiter unavailable")
				return
			}
			if !allowed {
				writer.Header().Set("Retry-After", "1")
				writeError(writer, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(writer, request)
		})
	}
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"code": status, "message": message})
}
