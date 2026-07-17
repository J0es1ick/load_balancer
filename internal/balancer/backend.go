package balancer

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
)

type Backend struct {
	URL          *url.URL
	Alive        bool
	Enabled      bool
	mux          sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
	requests     atomic.Uint64
}

func (b *Backend) SetAlive(alive bool) {
	b.mux.Lock()
	b.Alive = alive
	b.mux.Unlock()
}

func (b *Backend) IsAlive() bool {
	b.mux.RLock()
	defer b.mux.RUnlock()
	return b.Alive && b.Enabled
}

func (b *Backend) IsHealthy() bool {
	b.mux.RLock()
	defer b.mux.RUnlock()
	return b.Alive
}

func (b *Backend) SetEnabled(enabled bool) {
	b.mux.Lock()
	b.Enabled = enabled
	b.mux.Unlock()
}

func (b *Backend) IsEnabled() bool {
	b.mux.RLock()
	defer b.mux.RUnlock()
	return b.Enabled
}

func (b *Backend) ID() string {
	return b.URL.Hostname()
}

func (b *Backend) RecordRequest() {
	b.requests.Add(1)
}

type BackendSnapshot struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Healthy   bool   `json:"healthy"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Requests  uint64 `json:"requests"`
}

func (b *Backend) Snapshot() BackendSnapshot {
	b.mux.RLock()
	healthy := b.Alive
	enabled := b.Enabled
	b.mux.RUnlock()

	return BackendSnapshot{
		ID:        b.ID(),
		URL:       b.URL.Host,
		Healthy:   healthy,
		Enabled:   enabled,
		Available: healthy && enabled,
		Requests:  b.requests.Load(),
	}
}

type BackendPool struct {
	Backends []*Backend
	mu       sync.RWMutex
}

func NewBackendPool(backendURLs []string) *BackendPool {
	var backends []*Backend
	for _, u := range backendURLs {
		backendURL, err := url.Parse(u)
		if err != nil {
			log.Fatal(err)
		}

		proxy := httputil.NewSingleHostReverseProxy(backendURL)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
			log.Printf("[%s] %s\n", backendURL.Host, e.Error())
			w.WriteHeader(http.StatusBadGateway)
		}

		backends = append(backends, &Backend{
			URL:          backendURL,
			Alive:        true,
			Enabled:      true,
			ReverseProxy: proxy,
		})
	}
	return &BackendPool{Backends: backends}
}

func (p *BackendPool) GetBackends() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	backends := make([]*Backend, len(p.Backends))
	copy(backends, p.Backends)
	return backends
}

func (p *BackendPool) MarkBackendStatus(backendURL *url.URL, alive bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, backend := range p.Backends {
		if backend.URL.String() == backendURL.String() {
			backend.SetAlive(alive)
			return
		}
	}
	log.Printf("Backend %s not found", backendURL)
}

func (p *BackendPool) SetBackendEnabled(id string, enabled bool) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, backend := range p.Backends {
		if backend.ID() == id {
			backend.SetEnabled(enabled)
			return true
		}
	}

	return false
}

func (p *BackendPool) Snapshot() []BackendSnapshot {
	backends := p.GetBackends()
	snapshot := make([]BackendSnapshot, 0, len(backends))
	for _, backend := range backends {
		snapshot = append(snapshot, backend.Snapshot())
	}
	return snapshot
}
