package balancer

import (
	"context"
	"crypto/tls"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

type HealthSettings struct {
	Mode                  string
	Path                  string
	Interval              time.Duration
	Timeout               time.Duration
	FailureThreshold      int
	SuccessThreshold      int
	MaxConcurrency        int
	Jitter                time.Duration
	Cooldown              time.Duration
	ExpectedStatuses      []int
	SlowStart             time.Duration
	SlowStartMinimum      int
	MaxConcurrentRequests int64
}

type HealthChecker struct {
	pool     *BackendPool
	settings atomic.Pointer[HealthSettings]
	updated  chan struct{}
	checkMu  sync.Mutex
}

func NewHealthChecker(pool *BackendPool, settings HealthSettings) (*HealthChecker, error) {
	normalizeHealthSettings(&settings)
	if err := validateHealthSettings(settings); err != nil {
		return nil, err
	}
	hc := &HealthChecker{pool: pool, updated: make(chan struct{}, 1)}
	hc.settings.Store(cloneHealthSettings(settings))
	pool.UpdatePassivePolicy(policyFromHealth(settings))
	return hc, nil
}

func (hc *HealthChecker) Update(settings HealthSettings) error {
	normalizeHealthSettings(&settings)
	if err := validateHealthSettings(settings); err != nil {
		return err
	}
	hc.settings.Store(cloneHealthSettings(settings))
	hc.pool.UpdatePassivePolicy(policyFromHealth(settings))
	select {
	case hc.updated <- struct{}{}:
	default:
	}
	return nil
}

func policyFromHealth(settings HealthSettings) PassivePolicy {
	return PassivePolicy{FailureThreshold: int64(settings.FailureThreshold), Cooldown: settings.Cooldown, MaxConcurrentRequests: settings.MaxConcurrentRequests, SlowStart: settings.SlowStart, SlowStartMinimum: settings.SlowStartMinimum}
}

func (hc *HealthChecker) Settings() HealthSettings {
	settings := hc.settings.Load()
	if settings == nil {
		return HealthSettings{}
	}
	return *cloneHealthSettings(*settings)
}

func (hc *HealthChecker) Start(ctx context.Context) {
	timer := time.NewTimer(hc.nextDelay())
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			hc.Check(ctx)
			if ctx.Err() != nil {
				return
			}
			timer.Reset(hc.nextDelay())
		case <-hc.updated:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(hc.nextDelay())
		case <-ctx.Done():
			return
		}
	}
}

func (hc *HealthChecker) nextDelay() time.Duration {
	settings := hc.Settings()
	if settings.Jitter <= 0 {
		return settings.Interval
	}
	return settings.Interval + time.Duration(rand.Int64N(int64(settings.Jitter)+1))
}

func (hc *HealthChecker) Check(ctx context.Context) {
	hc.checkMu.Lock()
	defer hc.checkMu.Unlock()
	settings := hc.Settings()
	hc.check(ctx, hc.pool.GetBackends(), settings)
}

func (hc *HealthChecker) WarmReplacement(ctx context.Context, replacement *BackendReplacement, settings HealthSettings) error {
	normalizeHealthSettings(&settings)
	if err := validateHealthSettings(settings); err != nil {
		return err
	}
	if replacement == nil || replacement.owner != hc.pool {
		return fmt.Errorf("backend replacement belongs to another pool")
	}
	if !replacement.needsWarmup() {
		return nil
	}

	hc.checkMu.Lock()
	defer hc.checkMu.Unlock()
	for range settings.SuccessThreshold {
		hc.check(ctx, replacement.backends, settings)
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("warm backend replacement: %w", err)
		}
	}
	if !replacement.ready() {
		return fmt.Errorf("backend replacement is not ready: no enabled backend passed %d consecutive health checks", settings.SuccessThreshold)
	}
	return nil
}

func (hc *HealthChecker) check(ctx context.Context, backends []*Backend, settings HealthSettings) {
	if len(backends) == 0 {
		return
	}

	workers := min(settings.MaxConcurrency, len(backends))
	jobs := make(chan *Backend)
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for range workers {
		go func() {
			defer waitGroup.Done()
			for backend := range jobs {
				healthy := checkBackend(ctx, backend.URL, settings)
				backend.RecordHealthResult(healthy, settings.SuccessThreshold, settings.FailureThreshold)
			}
		}()
	}
	for _, backend := range backends {
		select {
		case jobs <- backend:
		case <-ctx.Done():
			close(jobs)
			waitGroup.Wait()
			return
		}
	}
	close(jobs)
	waitGroup.Wait()
}

func checkBackend(parent context.Context, backendURL *url.URL, settings HealthSettings) bool {
	ctx, cancel := context.WithTimeout(parent, settings.Timeout)
	defer cancel()
	if settings.Mode == "tcp" {
		dialer := net.Dialer{Timeout: settings.Timeout}
		connection, err := dialer.DialContext(ctx, "tcp", backendURL.Host)
		if err != nil {
			return false
		}
		_ = connection.Close()
		return true
	}

	probeURL := *backendURL
	if settings.Mode == "https" {
		probeURL.Scheme = "https"
	}
	probeURL.Path = joinPath(backendURL.Path, settings.Path)
	probeURL.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL.String(), nil)
	if err != nil {
		return false
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: settings.Timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: settings.Timeout,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives:   true,
	}}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	for _, expected := range settings.ExpectedStatuses {
		if response.StatusCode == expected {
			return true
		}
	}
	return false
}

func validateHealthSettings(settings HealthSettings) error {
	if settings.Mode != "tcp" && settings.Mode != "http" && settings.Mode != "https" {
		return fmt.Errorf("health mode must be tcp, http or https")
	}
	if settings.Interval <= 0 || settings.Timeout <= 0 || settings.FailureThreshold < 1 || settings.SuccessThreshold < 1 || settings.MaxConcurrency < 1 || settings.Jitter < 0 || settings.Cooldown < 0 || settings.SlowStart < 0 || settings.SlowStartMinimum < 1 || settings.SlowStartMinimum > 100 || settings.MaxConcurrentRequests < 1 {
		return fmt.Errorf("health-check settings are invalid")
	}
	if settings.Mode != "tcp" && (settings.Path == "" || settings.Path[0] != '/') {
		return fmt.Errorf("HTTP health path must start with /")
	}
	return nil
}

func normalizeHealthSettings(settings *HealthSettings) {
	if settings.SlowStartMinimum == 0 {
		settings.SlowStartMinimum = 10
	}
	if settings.MaxConcurrentRequests == 0 {
		settings.MaxConcurrentRequests = 512
	}
}

func cloneHealthSettings(settings HealthSettings) *HealthSettings {
	settings.ExpectedStatuses = append([]int(nil), settings.ExpectedStatuses...)
	return &settings
}

func joinPath(base, requestPath string) string {
	baseSlash := len(base) > 0 && base[len(base)-1] == '/'
	requestSlash := len(requestPath) > 0 && requestPath[0] == '/'
	switch {
	case baseSlash && requestSlash:
		return base + requestPath[1:]
	case !baseSlash && !requestSlash:
		return base + "/" + requestPath
	default:
		return base + requestPath
	}
}
