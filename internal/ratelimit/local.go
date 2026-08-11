package ratelimit

import (
	"context"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

const defaultLocalMaxBuckets = 100_000

type localBucket struct {
	capacity        int
	tokens          float64
	refillPerSecond float64
	lastRefill      time.Time
	lastSeen        time.Time
}

type localShard struct {
	mu         sync.Mutex
	buckets    map[string]*localBucket
	maxBuckets int
	slots      []string
	positions  map[string]int
	freeSlots  []int
	nextEvict  int
}

type LocalStore struct {
	shards    []localShard
	size      atomic.Int64
	evictions atomic.Uint64
}

type LocalStoreStats struct {
	Buckets   int64
	Evictions uint64
}

func NewLocalStore(shardCount int) *LocalStore {
	return NewBoundedLocalStore(shardCount, defaultLocalMaxBuckets)
}

func NewBoundedLocalStore(shardCount, maxBuckets int) *LocalStore {
	if shardCount < 1 {
		shardCount = 1
	}
	if maxBuckets < 1 {
		maxBuckets = 1
	}
	if shardCount > maxBuckets {
		shardCount = maxBuckets
	}
	store := &LocalStore{shards: make([]localShard, shardCount)}
	for index := range store.shards {
		shard := &store.shards[index]
		shard.buckets = make(map[string]*localBucket)
		shard.positions = make(map[string]int)
		shard.maxBuckets = maxBuckets / shardCount
		if index < maxBuckets%shardCount {
			shard.maxBuckets++
		}
		shard.slots = make([]string, 0, shard.maxBuckets)
	}
	return store
}

func (store *LocalStore) Name() string { return "local" }

func (store *LocalStore) Take(_ context.Context, key string, policy Policy) (LimitDecision, error) {
	shard := store.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	now := time.Now()
	bucket := shard.buckets[key]
	if bucket == nil {
		bucket = &localBucket{capacity: policy.Capacity, tokens: float64(policy.Capacity), refillPerSecond: policy.RefillPerSecond, lastRefill: now, lastSeen: now}
		store.insertBucket(shard, key, bucket)
	}
	bucket.applyPolicy(policy, now)
	bucket.refill(now)
	allowed := bucket.tokens >= 1
	if allowed {
		bucket.tokens--
	}
	bucket.lastSeen = now
	return LimitDecision{Allowed: allowed, Bucket: bucket.state(store.Name())}, nil
}

func (store *LocalStore) Peek(_ context.Context, key string, policy Policy) (BucketState, error) {
	shard := store.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	bucket := shard.buckets[key]
	if bucket == nil {
		return BucketState{Capacity: policy.Capacity, Tokens: float64(policy.Capacity), RefillPerSecond: policy.RefillPerSecond, Storage: store.Name()}, nil
	}
	copy := *bucket
	copy.applyPolicy(policy, time.Now())
	copy.refill(time.Now())
	return copy.state(store.Name()), nil
}

func (store *LocalStore) Reset(_ context.Context, key string, policy Policy) (BucketState, error) {
	shard := store.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	now := time.Now()
	bucket := &localBucket{capacity: policy.Capacity, tokens: float64(policy.Capacity), refillPerSecond: policy.RefillPerSecond, lastRefill: now, lastSeen: now}
	if shard.buckets[key] == nil {
		store.insertBucket(shard, key, bucket)
	} else {
		shard.buckets[key] = bucket
	}
	return bucket.state(store.Name()), nil
}

func (store *LocalStore) Healthy(context.Context) error { return nil }
func (store *LocalStore) Close() error                  { return nil }

func (store *LocalStore) Cleanup(_ context.Context, olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	for index := range store.shards {
		shard := &store.shards[index]
		shard.mu.Lock()
		for key, bucket := range shard.buckets {
			if bucket.lastSeen.Before(cutoff) {
				store.deleteBucket(shard, key)
				store.size.Add(-1)
			}
		}
		shard.mu.Unlock()
	}
	return nil
}

func (store *LocalStore) Stats() LocalStoreStats {
	return LocalStoreStats{Buckets: store.size.Load(), Evictions: store.evictions.Load()}
}

func (store *LocalStore) insertBucket(shard *localShard, key string, bucket *localBucket) {
	if len(shard.buckets) < shard.maxBuckets {
		var slot int
		if last := len(shard.freeSlots) - 1; last >= 0 {
			slot = shard.freeSlots[last]
			shard.freeSlots = shard.freeSlots[:last]
		} else {
			slot = len(shard.slots)
			shard.slots = append(shard.slots, "")
		}
		shard.slots[slot] = key
		shard.positions[key] = slot
		shard.buckets[key] = bucket
		store.size.Add(1)
		return
	}

	slot := shard.nextEvict
	shard.nextEvict = (shard.nextEvict + 1) % shard.maxBuckets
	evictedKey := shard.slots[slot]
	delete(shard.buckets, evictedKey)
	delete(shard.positions, evictedKey)
	shard.slots[slot] = key
	shard.positions[key] = slot
	shard.buckets[key] = bucket
	store.evictions.Add(1)
}

func (store *LocalStore) deleteBucket(shard *localShard, key string) {
	slot, exists := shard.positions[key]
	if !exists {
		return
	}
	delete(shard.buckets, key)
	delete(shard.positions, key)
	shard.slots[slot] = ""
	shard.freeSlots = append(shard.freeSlots, slot)
}

func (store *LocalStore) shard(key string) *localShard {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(key))
	return &store.shards[hash.Sum64()%uint64(len(store.shards))]
}

func (bucket *localBucket) applyPolicy(policy Policy, now time.Time) {
	if bucket.capacity == policy.Capacity && bucket.refillPerSecond == policy.RefillPerSecond {
		return
	}
	bucket.refill(now)
	bucket.capacity = policy.Capacity
	bucket.refillPerSecond = policy.RefillPerSecond
	if bucket.tokens > float64(policy.Capacity) {
		bucket.tokens = float64(policy.Capacity)
	}
}

func (bucket *localBucket) refill(now time.Time) {
	elapsed := now.Sub(bucket.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	bucket.tokens = min(float64(bucket.capacity), bucket.tokens+elapsed*bucket.refillPerSecond)
	bucket.lastRefill = now
}

func (bucket *localBucket) state(storage string) BucketState {
	return BucketState{Capacity: bucket.capacity, Tokens: bucket.tokens, RefillPerSecond: bucket.refillPerSecond, Storage: storage}
}
