package balancer

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"
)

type HealthChecker struct {
	pool     *BackendPool
	mu       sync.RWMutex
	interval time.Duration
	timeout  time.Duration
	updated  chan struct{}
}

func NewHealthChecker(pool *BackendPool, interval time.Duration, timeout ...time.Duration) *HealthChecker {
	checkTimeout := 2 * time.Second
	if len(timeout) > 0 {
		checkTimeout = timeout[0]
	}
	return &HealthChecker{
		pool:     pool,
		interval: interval,
		timeout:  checkTimeout,
		updated:  make(chan struct{}, 1),
	}
}

func (hc *HealthChecker) Update(interval, timeout time.Duration) error {
	if interval <= 0 || timeout <= 0 {
		return fmt.Errorf("health-check interval and timeout must be positive")
	}

	hc.mu.Lock()
	hc.interval = interval
	hc.timeout = timeout
	hc.mu.Unlock()

	select {
	case hc.updated <- struct{}{}:
	default:
	}
	return nil
}

func (hc *HealthChecker) Settings() (time.Duration, time.Duration) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.interval, hc.timeout
}

func (hc *HealthChecker) Start(ctx context.Context) {
	interval, _ := hc.Settings()
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			hc.Check(ctx)
			interval, _ = hc.Settings()
			timer.Reset(interval)
		case <-hc.updated:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			interval, _ = hc.Settings()
			timer.Reset(interval)
		case <-ctx.Done():
			return
		}
	}
}

func (hc *HealthChecker) Check(ctx context.Context) {
	_, timeout := hc.Settings()
	var waitGroup sync.WaitGroup

	for _, backend := range hc.pool.GetBackends() {
		waitGroup.Add(1)
		go func(backend *Backend) {
			defer waitGroup.Done()
			backend.SetAlive(isBackendAlive(ctx, backend.URL, timeout))
		}(backend)
	}

	waitGroup.Wait()
}

func isBackendAlive(ctx context.Context, backendURL *url.URL, timeout time.Duration) bool {
	dialer := net.Dialer{Timeout: timeout}
	connection, err := dialer.DialContext(ctx, "tcp", backendURL.Host)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}
