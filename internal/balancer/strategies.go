package balancer

import "sync/atomic"

type Strategy interface {
	GetNextPeer(*BackendPool) *Backend
	GetNextPeerExcluding(*BackendPool, map[string]struct{}) *Backend
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
