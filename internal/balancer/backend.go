package balancer

import (
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

type BackendSpec struct {
	ID       string
	URL      string
	Disabled bool
}

type backendList []*Backend

type PassivePolicy struct {
	FailureThreshold      int64
	Cooldown              time.Duration
	MaxConcurrentRequests int64
	SlowStart             time.Duration
	SlowStartMinimum      int
}

type Backend struct {
	stateMu            sync.Mutex
	id                 string
	URL                *url.URL
	healthy            atomic.Bool
	enabled            atomic.Bool
	requests           atomic.Uint64
	passiveFailures    atomic.Int64
	consecutiveSuccess atomic.Int64
	consecutiveFailure atomic.Int64
	ejectedUntil       atomic.Int64
	healthySince       atomic.Int64
	inflight           atomic.Int64
	draining           atomic.Bool
	pool               *BackendPool
}

func (b *Backend) ID() string { return b.id }

func (b *Backend) SetAlive(alive bool) {
	b.stateMu.Lock()
	changed := b.setAliveLocked(alive)
	b.stateMu.Unlock()
	b.refreshAvailability(changed)
}

func (b *Backend) setAliveLocked(alive bool) bool {
	changed := b.healthy.Swap(alive) != alive
	if alive {
		b.passiveFailures.Store(0)
		b.ejectedUntil.Store(0)
		if changed || b.healthySince.Load() == 0 {
			b.healthySince.Store(time.Now().UnixNano())
		}
	} else if changed {
		b.healthySince.Store(0)
	}
	return changed
}

func (b *Backend) RecordHealthResult(success bool, successThreshold, failureThreshold int) {
	b.stateMu.Lock()
	changed := b.recordHealthResultLocked(success, successThreshold, failureThreshold)
	b.stateMu.Unlock()
	b.refreshAvailability(changed)
}

func (b *Backend) recordHealthResultLocked(success bool, successThreshold, failureThreshold int) bool {
	if success {
		b.consecutiveFailure.Store(0)
		if until := b.ejectedUntil.Load(); until > time.Now().UnixNano() {
			b.consecutiveSuccess.Store(0)
			return false
		}
		if b.consecutiveSuccess.Add(1) >= int64(successThreshold) {
			return b.setAliveLocked(true)
		}
		return false
	}
	b.consecutiveSuccess.Store(0)
	if b.consecutiveFailure.Add(1) >= int64(failureThreshold) {
		return b.setAliveLocked(false)
	}
	return false
}

func (b *Backend) RecordPassiveFailure(policy PassivePolicy) {
	if policy.FailureThreshold < 1 {
		return
	}
	b.stateMu.Lock()
	if b.passiveFailures.Add(1) < policy.FailureThreshold {
		b.stateMu.Unlock()
		return
	}
	b.ejectedUntil.Store(time.Now().Add(policy.Cooldown).UnixNano())
	changed := b.setAliveLocked(false)
	b.stateMu.Unlock()
	b.refreshAvailability(changed)
}

func (b *Backend) RecordPassiveSuccess() {
	b.stateMu.Lock()
	b.passiveFailures.Store(0)
	b.stateMu.Unlock()
}

func (b *Backend) refreshAvailability(changed bool) {
	if changed && b.pool != nil {
		b.pool.refreshAvailable()
	}
}

func (b *Backend) IsAlive() bool {
	return b.healthy.Load() && b.enabled.Load() && !b.draining.Load() && !b.IsEjected()
}

func (b *Backend) IsHealthy() bool { return b.healthy.Load() }

func (b *Backend) IsEjected() bool {
	until := b.ejectedUntil.Load()
	return until > 0 && time.Now().UnixNano() < until
}

func (b *Backend) SetEnabled(enabled bool) {
	b.stateMu.Lock()
	if enabled {
		b.draining.Store(false)
	}
	changed := b.enabled.Swap(enabled) != enabled
	b.stateMu.Unlock()
	b.refreshAvailability(changed)
}

func (b *Backend) IsEnabled() bool { return b.enabled.Load() }
func (b *Backend) RecordRequest()  { b.requests.Add(1) }

func (b *Backend) SetDraining(draining bool) {
	b.stateMu.Lock()
	b.draining.Store(draining)
	if draining {
		b.enabled.Store(false)
	}
	b.stateMu.Unlock()
	if b.pool != nil {
		b.pool.refreshAvailable()
	}
}

func (b *Backend) IsDraining() bool { return b.draining.Load() }

func (b *Backend) TryAcquire() bool {
	policy := b.pool.PassivePolicy()
	limit := policy.MaxConcurrentRequests
	if limit < 1 {
		limit = 1
	}
	for {
		if !b.IsAlive() {
			return false
		}
		current := b.inflight.Load()
		if current >= limit {
			return false
		}
		if b.inflight.CompareAndSwap(current, current+1) {
			if b.IsAlive() {
				return true
			}
			b.inflight.Add(-1)
			return false
		}
	}
}

func (b *Backend) Release() {
	if b.inflight.Add(-1) < 0 {
		b.inflight.Store(0)
	}
}

func (b *Backend) Inflight() int64 { return b.inflight.Load() }

func (b *Backend) SlowStartPercent() int {
	policy := b.pool.PassivePolicy()
	if policy.SlowStart <= 0 {
		return 100
	}
	since := b.healthySince.Load()
	if since == 0 {
		return policy.SlowStartMinimum
	}
	elapsed := time.Since(time.Unix(0, since))
	if elapsed >= policy.SlowStart {
		return 100
	}
	minimum := policy.SlowStartMinimum
	if minimum < 1 {
		minimum = 1
	}
	percent := minimum + int(float64(100-minimum)*(float64(elapsed)/float64(policy.SlowStart)))
	if percent > 100 {
		return 100
	}
	return percent
}

type BackendSnapshot struct {
	ID               string `json:"id"`
	URL              string `json:"url"`
	Healthy          bool   `json:"healthy"`
	Enabled          bool   `json:"enabled"`
	Available        bool   `json:"available"`
	Ejected          bool   `json:"ejected"`
	Requests         uint64 `json:"requests"`
	PassiveFailures  int64  `json:"passive_failures"`
	Inflight         int64  `json:"inflight"`
	Draining         bool   `json:"draining"`
	SlowStartPercent int    `json:"slow_start_percent"`
	CircuitState     string `json:"circuit_state"`
}

func (b *Backend) Snapshot() BackendSnapshot {
	return BackendSnapshot{
		ID: b.ID(), URL: b.URL.String(), Healthy: b.IsHealthy(), Enabled: b.IsEnabled(),
		Available: b.IsAlive(), Ejected: b.IsEjected(), Requests: b.requests.Load(),
		PassiveFailures: b.passiveFailures.Load(), Inflight: b.Inflight(), Draining: b.IsDraining(),
		SlowStartPercent: b.SlowStartPercent(), CircuitState: b.circuitState(),
	}
}

func (b *Backend) circuitState() string {
	if b.IsDraining() {
		return "draining"
	}
	if b.IsEjected() || !b.IsHealthy() {
		return "open"
	}
	return "closed"
}

type BackendPool struct {
	mu        sync.Mutex
	all       atomic.Pointer[backendList]
	available atomic.Pointer[backendList]
	policy    atomic.Pointer[PassivePolicy]
}

type BackendReplacement struct {
	owner    *BackendPool
	backends backendList
}

func NewBackendPool(specs []BackendSpec, policy ...PassivePolicy) (*BackendPool, error) {
	pool := &BackendPool{}
	selectedPolicy := PassivePolicy{FailureThreshold: 2, Cooldown: 10 * time.Second, MaxConcurrentRequests: 512, SlowStartMinimum: 10}
	if len(policy) > 0 {
		selectedPolicy = policy[0]
	}
	pool.policy.Store(&selectedPolicy)
	backends, err := pool.buildBackends(specs)
	if err != nil {
		return nil, err
	}
	for _, backend := range backends {
		backend.pool = pool
	}
	pool.all.Store(&backends)
	pool.refreshAvailable()
	return pool, nil
}

func (p *BackendPool) buildBackends(specs []BackendSpec) (backendList, error) {
	backends := make(backendList, 0, len(specs))
	ids := make(map[string]struct{}, len(specs))
	urls := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		backendURL, err := url.Parse(spec.URL)
		if err != nil || spec.ID == "" || backendURL.Host == "" || (backendURL.Scheme != "http" && backendURL.Scheme != "https") {
			return nil, fmt.Errorf("invalid backend %q with URL %q", spec.ID, spec.URL)
		}
		if _, exists := ids[spec.ID]; exists {
			return nil, fmt.Errorf("duplicate backend ID %q", spec.ID)
		}
		if _, exists := urls[backendURL.String()]; exists {
			return nil, fmt.Errorf("duplicate backend URL %q", backendURL.String())
		}
		ids[spec.ID] = struct{}{}
		urls[backendURL.String()] = struct{}{}
		backend := &Backend{id: spec.ID, URL: backendURL}
		backend.enabled.Store(!spec.Disabled)
		backends = append(backends, backend)
	}
	return backends, nil
}

func (p *BackendPool) ReplaceBackends(specs []BackendSpec) error {
	replacement, err := p.PrepareReplacement(specs)
	if err != nil {
		return err
	}
	return p.CommitReplacement(replacement)
}

func (p *BackendPool) PrepareReplacement(specs []BackendSpec) (*BackendReplacement, error) {
	backends, err := p.buildBackends(specs)
	if err != nil {
		return nil, err
	}
	return &BackendReplacement{owner: p, backends: backends}, nil
}

func (p *BackendPool) CommitReplacement(replacement *BackendReplacement) error {
	if replacement == nil || replacement.owner != p {
		return fmt.Errorf("backend replacement belongs to another pool")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current := make(map[string]*Backend)
	for _, backend := range p.GetBackends() {
		current[backend.ID()+"\x00"+backend.URL.String()] = backend
	}
	for index, backend := range replacement.backends {
		if previous, exists := current[backend.ID()+"\x00"+backend.URL.String()]; exists {
			previous.applyReplacementState(backend.IsEnabled(), backend.IsHealthy())
			replacement.backends[index] = previous
		} else {
			backend.pool = p
		}
	}
	p.all.Store(&replacement.backends)
	p.refreshAvailableLocked()
	return nil
}

func (b *Backend) applyReplacementState(enabled, warmedHealthy bool) {
	b.stateMu.Lock()
	b.draining.Store(false)
	b.enabled.Store(enabled)
	if warmedHealthy && !b.healthy.Load() {
		b.setAliveLocked(true)
	}
	b.stateMu.Unlock()
}

func (replacement *BackendReplacement) ready() bool {
	if replacement == nil {
		return false
	}
	for _, backend := range replacement.backends {
		if backend.IsAlive() {
			return true
		}
	}
	return false
}

func (replacement *BackendReplacement) needsWarmup() bool {
	if replacement == nil {
		return false
	}
	current := make(map[string]*Backend)
	for _, backend := range replacement.owner.GetBackends() {
		current[backend.ID()+"\x00"+backend.URL.String()] = backend
	}
	hasEnabledBackend := false
	for _, candidate := range replacement.backends {
		if !candidate.IsEnabled() {
			continue
		}
		hasEnabledBackend = true
		previous := current[candidate.ID()+"\x00"+candidate.URL.String()]
		if previous != nil && previous.IsHealthy() && !previous.IsEjected() {
			return false
		}
	}
	return hasEnabledBackend
}

func (p *BackendPool) UpdatePassivePolicy(policy PassivePolicy) {
	p.policy.Store(&policy)
}

func (p *BackendPool) PassivePolicy() PassivePolicy {
	policy := p.policy.Load()
	if policy == nil {
		return PassivePolicy{}
	}
	return *policy
}

func (p *BackendPool) GetBackends() []*Backend {
	current := p.all.Load()
	if current == nil {
		return nil
	}
	return *current
}

func (p *BackendPool) AvailableBackends() []*Backend {
	current := p.available.Load()
	if current == nil {
		return nil
	}
	return *current
}

func (p *BackendPool) refreshAvailable() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshAvailableLocked()
}

func (p *BackendPool) refreshAvailableLocked() {
	all := p.all.Load()
	available := make(backendList, 0)
	if all != nil {
		available = make(backendList, 0, len(*all))
		for _, backend := range *all {
			if backend.IsAlive() {
				available = append(available, backend)
			}
		}
	}
	p.available.Store(&available)
}

func (p *BackendPool) MarkBackendStatus(backendURL *url.URL, alive bool) {
	for _, backend := range p.GetBackends() {
		if backend.URL.String() == backendURL.String() {
			backend.SetAlive(alive)
			return
		}
	}
}

func (p *BackendPool) SetBackendEnabled(id string, enabled bool) bool {
	for _, backend := range p.GetBackends() {
		if backend.ID() == id {
			backend.SetEnabled(enabled)
			return true
		}
	}
	return false
}

func (p *BackendPool) SetActiveCount(count int) bool {
	backends := p.GetBackends()
	if count < 1 || count > len(backends) {
		return false
	}
	for index, backend := range backends {
		backend.SetEnabled(index < count)
	}
	return true
}

func (p *BackendPool) DrainBackend(id string) bool {
	for _, backend := range p.GetBackends() {
		if backend.ID() == id {
			backend.SetDraining(true)
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

func (p *BackendPool) Ready() bool { return len(p.AvailableBackends()) > 0 }
