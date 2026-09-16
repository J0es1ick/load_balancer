package discovery

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
)

type Result struct {
	Endpoints       []config.GatewayEndpointConfig `json:"endpoints"`
	ResourceVersion string                         `json:"resource_version,omitempty"`
	ObservedAt      time.Time                      `json:"observed_at"`
	Err             error                          `json:"-"`
}

func Run(ctx context.Context, settings config.GatewayDiscoveryConfig, emit func(Result)) {
	if settings.Type == "kubernetes" {
		runKubernetes(ctx, settings, emit)
		return
	}
	interval := settings.RefreshInterval.Duration()
	if interval <= 0 {
		interval = 10 * time.Second
	}
	for ctx.Err() == nil {
		probe, cancel := context.WithTimeout(ctx, min(interval, 5*time.Second))
		addresses, err := net.DefaultResolver.LookupNetIP(probe, "ip", settings.Hostname)
		cancel()
		result := Result{ObservedAt: time.Now().UTC(), Err: err, Endpoints: []config.GatewayEndpointConfig{}}
		if err == nil {
			scheme := settings.Scheme
			if scheme == "" {
				scheme = "http"
			}
			port := settings.Port
			if port == 0 {
				port = 80
				if scheme == "https" {
					port = 443
				}
			}
			seen := make(map[string]bool)
			for _, address := range addresses {
				host := net.JoinHostPort(address.String(), strconv.Itoa(port))
				if seen[host] {
					continue
				}
				seen[host] = true
				result.Endpoints = append(result.Endpoints, config.GatewayEndpointConfig{ID: host, URL: scheme + "://" + host, Weight: 1})
			}
			if len(result.Endpoints) == 0 {
				result.Err = fmt.Errorf("DNS returned no addresses")
			}
			sort.Slice(result.Endpoints, func(i, j int) bool { return result.Endpoints[i].ID < result.Endpoints[j].ID })
		}
		emit(result)
		if !wait(ctx, interval) {
			return
		}
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
