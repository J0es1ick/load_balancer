package balancer

import "net/http"

type LoadBalancer struct {
	pool     *BackendPool
	strategy Strategy
}

func NewLoadBalancer(pool *BackendPool, strategy Strategy) *LoadBalancer {
	return &LoadBalancer{
		pool:     pool,
		strategy: strategy,
	}
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	backend := lb.strategy.GetNextPeer(lb.pool)
	if backend != nil {
		backend.RecordRequest()
		w.Header().Set("X-Balancer-Backend", backend.URL.Host)
		backend.ReverseProxy.ServeHTTP(w, r)
		return
	}

	http.Error(w, "Service not available", http.StatusServiceUnavailable)
}

func (lb *LoadBalancer) Backends() []BackendSnapshot {
	return lb.pool.Snapshot()
}

func (lb *LoadBalancer) SetBackendEnabled(id string, enabled bool) bool {
	return lb.pool.SetBackendEnabled(id, enabled)
}
