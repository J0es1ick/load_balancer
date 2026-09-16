package balancer

import (
	"hash/fnv"
	"math"
	"net/http"
	"sync/atomic"
)

type Strategy interface {
	GetNextPeer(*BackendPool) *Backend
	GetNextPeerExcluding(*BackendPool, map[string]struct{}) *Backend
}

type RequestStrategy interface {
	Strategy
	GetNextPeerForRequest(*BackendPool, map[string]struct{}, *http.Request) *Backend
}

type RoundRobinStrategy struct{ counter atomic.Uint64 }

func NewRoundRobinStrategy() *RoundRobinStrategy { return &RoundRobinStrategy{} }

func (s *RoundRobinStrategy) GetNextPeer(pool *BackendPool) *Backend {
	return s.GetNextPeerExcluding(pool, nil)
}

func (s *RoundRobinStrategy) GetNextPeerExcluding(pool *BackendPool, excluded map[string]struct{}) *Backend {
	backends := pool.AvailableBackends()
	if len(backends) == 0 {
		return nil
	}
	start := s.counter.Add(1) - 1
	var fallback *Backend
	for offset := uint64(0); offset < uint64(len(backends)); offset++ {
		candidate := backends[(start+offset)%uint64(len(backends))]
		if _, exists := excluded[candidate.ID()]; exists {
			continue
		}
		if fallback == nil {
			fallback = candidate
		}
		percentage := candidate.SlowStartPercent()
		if percentage >= 100 || int((start+offset)%100) < percentage {
			return candidate
		}
	}
	return fallback
}

type WeightedRoundRobinStrategy struct{ counter atomic.Uint64 }

func NewWeightedRoundRobinStrategy() *WeightedRoundRobinStrategy {
	return &WeightedRoundRobinStrategy{}
}

func (strategy *WeightedRoundRobinStrategy) GetNextPeer(pool *BackendPool) *Backend {
	return strategy.GetNextPeerExcluding(pool, nil)
}

func (strategy *WeightedRoundRobinStrategy) GetNextPeerExcluding(pool *BackendPool, excluded map[string]struct{}) *Backend {
	backends := pool.AvailableBackends()
	total := 0
	for _, backend := range backends {
		if _, skip := excluded[backend.ID()]; !skip {
			total += effectiveWeight(backend)
		}
	}
	if total == 0 {
		return nil
	}
	position := int((strategy.counter.Add(1) - 1) % uint64(total))
	for _, backend := range backends {
		if _, skip := excluded[backend.ID()]; skip {
			continue
		}
		weight := effectiveWeight(backend)
		if position < weight {
			return backend
		}
		position -= weight
	}
	return nil
}

type LeastRequestStrategy struct{ counter atomic.Uint64 }

func NewLeastRequestStrategy() *LeastRequestStrategy { return &LeastRequestStrategy{} }

func (strategy *LeastRequestStrategy) GetNextPeer(pool *BackendPool) *Backend {
	return strategy.GetNextPeerExcluding(pool, nil)
}

func (strategy *LeastRequestStrategy) GetNextPeerExcluding(pool *BackendPool, excluded map[string]struct{}) *Backend {
	backends := pool.AvailableBackends()
	if len(backends) == 0 {
		return nil
	}
	start := int((strategy.counter.Add(1) - 1) % uint64(len(backends)))
	var selected *Backend
	for offset := range len(backends) {
		candidate := backends[(start+offset)%len(backends)]
		if _, skip := excluded[candidate.ID()]; skip {
			continue
		}
		if selected == nil || lessLoaded(candidate, selected) {
			selected = candidate
		}
	}
	return selected
}

func lessLoaded(left, right *Backend) bool {
	return (left.Inflight()+1)*int64(effectiveWeight(right)) < (right.Inflight()+1)*int64(effectiveWeight(left))
}

type RendezvousStrategy struct {
	key func(*http.Request) string
}

func NewRendezvousStrategy(key func(*http.Request) string) *RendezvousStrategy {
	return &RendezvousStrategy{key: key}
}

func (strategy *RendezvousStrategy) GetNextPeer(pool *BackendPool) *Backend {
	return strategy.GetNextPeerExcluding(pool, nil)
}

func (strategy *RendezvousStrategy) GetNextPeerExcluding(pool *BackendPool, excluded map[string]struct{}) *Backend {
	return strategy.selectBackend(pool, excluded, "")
}

func (strategy *RendezvousStrategy) GetNextPeerForRequest(pool *BackendPool, excluded map[string]struct{}, request *http.Request) *Backend {
	key := ""
	if strategy.key != nil {
		key = strategy.key(request)
	}
	return strategy.selectBackend(pool, excluded, key)
}

func (strategy *RendezvousStrategy) selectBackend(pool *BackendPool, excluded map[string]struct{}, key string) *Backend {
	var selected *Backend
	best := math.Inf(1)
	for _, backend := range pool.AvailableBackends() {
		if _, skip := excluded[backend.ID()]; skip {
			continue
		}
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(key))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(backend.ID()))
		uniform := (float64(hash.Sum64()) + 1) / (float64(^uint64(0)) + 2)
		score := -math.Log(uniform) / float64(effectiveWeight(backend))
		if score < best {
			best = score
			selected = backend
		}
	}
	return selected
}

func effectiveWeight(backend *Backend) int {
	weight := backend.Weight() * backend.SlowStartPercent() / 100
	if weight < 1 {
		return 1
	}
	return weight
}
