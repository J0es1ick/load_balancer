package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisTakeScript = `
local current = redis.call('TIME')
local now_ms = current[1] * 1000 + current[2] / 1000
local values = redis.call('HMGET', KEYS[1], 'tokens', 'last_refill_ms')
local capacity = tonumber(ARGV[1])
local refill_per_ms = tonumber(ARGV[2]) / 1000
local tokens = capacity

if values[1] then
  tokens = math.min(capacity, tonumber(values[1]) + math.max(0, now_ms - tonumber(values[2])) * refill_per_ms)
end

local allowed = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
end

redis.call('HSET', KEYS[1],
  'capacity', capacity,
  'tokens', tokens,
  'refill_per_second', ARGV[2],
  'last_refill_ms', now_ms,
  'last_seen_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return {allowed, tostring(tokens)}
`

const redisPeekScript = `
local current = redis.call('TIME')
local now_ms = current[1] * 1000 + current[2] / 1000
local values = redis.call('HMGET', KEYS[1], 'tokens', 'last_refill_ms')
local capacity = tonumber(ARGV[1])
local refill_per_ms = tonumber(ARGV[2]) / 1000
local tokens = capacity
if values[1] then
  tokens = math.min(capacity, tonumber(values[1]) + math.max(0, now_ms - tonumber(values[2])) * refill_per_ms)
end
return tostring(tokens)
`

type RedisOptions struct {
	Address      string
	Password     string
	Database     int
	PoolSize     int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	Retention    time.Duration
	Prefix       string
}

type RedisStore struct {
	client    *redis.Client
	retention time.Duration
	prefix    string
	take      *redis.Script
	peek      *redis.Script
}

func NewRedisStore(_ context.Context, options RedisOptions) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr: options.Address, Password: options.Password, DB: options.Database,
		PoolSize: options.PoolSize, DialTimeout: options.DialTimeout,
		ReadTimeout: options.ReadTimeout, WriteTimeout: options.WriteTimeout,
	})
	if options.Retention <= 0 {
		options.Retention = 24 * time.Hour
	}
	if options.Prefix == "" {
		options.Prefix = "load-balancer:ratelimit:"
	}
	return &RedisStore{client: client, retention: options.Retention, prefix: options.Prefix, take: redis.NewScript(redisTakeScript), peek: redis.NewScript(redisPeekScript)}, nil
}

func (store *RedisStore) Name() string { return "redis" }

func (store *RedisStore) Take(ctx context.Context, key string, policy Policy) (LimitDecision, error) {
	result, err := store.take.Run(ctx, store.client, []string{store.key(key)}, policy.Capacity, policy.RefillPerSecond, store.retention.Milliseconds()).Result()
	if err != nil {
		return LimitDecision{}, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 2 {
		return LimitDecision{}, fmt.Errorf("unexpected Redis limiter response %T", result)
	}
	allowed, err := redisNumber(values[0])
	if err != nil {
		return LimitDecision{}, err
	}
	tokens, err := redisNumber(values[1])
	if err != nil {
		return LimitDecision{}, err
	}
	return LimitDecision{Allowed: allowed == 1, Bucket: BucketState{Capacity: policy.Capacity, Tokens: tokens, RefillPerSecond: policy.RefillPerSecond, Storage: store.Name()}}, nil
}

func (store *RedisStore) Peek(ctx context.Context, key string, policy Policy) (BucketState, error) {
	result, err := store.peek.Run(ctx, store.client, []string{store.key(key)}, policy.Capacity, policy.RefillPerSecond).Result()
	if err != nil {
		return BucketState{}, err
	}
	tokens, err := redisNumber(result)
	if err != nil {
		return BucketState{}, err
	}
	return BucketState{Capacity: policy.Capacity, Tokens: tokens, RefillPerSecond: policy.RefillPerSecond, Storage: store.Name()}, nil
}

func (store *RedisStore) Reset(ctx context.Context, key string, policy Policy) (BucketState, error) {
	now := time.Now().UnixMilli()
	pipe := store.client.TxPipeline()
	pipe.HSet(ctx, store.key(key), map[string]any{
		"capacity": policy.Capacity, "tokens": policy.Capacity,
		"refill_per_second": policy.RefillPerSecond, "last_refill_ms": now, "last_seen_ms": now,
	})
	pipe.PExpire(ctx, store.key(key), store.retention)
	if _, err := pipe.Exec(ctx); err != nil {
		return BucketState{}, err
	}
	return BucketState{Capacity: policy.Capacity, Tokens: float64(policy.Capacity), RefillPerSecond: policy.RefillPerSecond, Storage: store.Name()}, nil
}

func (store *RedisStore) Healthy(ctx context.Context) error            { return store.client.Ping(ctx).Err() }
func (store *RedisStore) Cleanup(context.Context, time.Duration) error { return nil }
func (store *RedisStore) Close() error                                 { return store.client.Close() }
func (store *RedisStore) key(key string) string                        { return store.prefix + key }

func redisNumber(value any) (float64, error) {
	switch typed := value.(type) {
	case int64:
		return float64(typed), nil
	case string:
		return strconv.ParseFloat(typed, 64)
	case []byte:
		return strconv.ParseFloat(string(typed), 64)
	default:
		return 0, fmt.Errorf("unexpected Redis number %T", value)
	}
}
