package gateway

import (
	"net/http"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/config"
)

type Defaults struct {
	Upstream      config.UpstreamConfig
	Retry         balancer.RetryPolicy
	Health        balancer.HealthSettings
	HistoryLimit  int
	WarmupTimeout time.Duration
	WrapTransport func(http.RoundTripper) http.RoundTripper
}

type DiscoveryStatus struct {
	Type       string    `json:"type"`
	LastUpdate time.Time `json:"last_update,omitempty"`
	Stale      bool      `json:"stale"`
	Error      string    `json:"error,omitempty"`
}

type TLSStatus struct {
	Enabled    bool   `json:"enabled"`
	ServerName string `json:"server_name,omitempty"`
}

type RetryStatus struct {
	MaxAttempts   int                          `json:"max_attempts"`
	PerTryTimeout string                       `json:"per_try_timeout"`
	Methods       []string                     `json:"methods"`
	Statuses      []int                        `json:"statuses"`
	Budget        balancer.RetryBudgetSnapshot `json:"budget"`
}

type ListenerStatus struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	Protocol string `json:"protocol"`
	TLS      bool   `json:"tls"`
}

type RouteStatus struct {
	ID       string `json:"id"`
	Listener string `json:"listener,omitempty"`
	Priority int    `json:"priority"`
}

type ClusterStatus struct {
	ID        string                     `json:"id"`
	Strategy  string                     `json:"strategy"`
	Endpoints []balancer.BackendSnapshot `json:"endpoints"`
	Discovery DiscoveryStatus            `json:"discovery"`
	TLS       TLSStatus                  `json:"tls"`
	Retry     RetryStatus                `json:"retry"`
}

type Stats struct {
	Requests      uint64 `json:"requests"`
	RouteNotFound uint64 `json:"route_not_found"`
	Redirects     uint64 `json:"redirects"`
	RateLimited   uint64 `json:"rate_limited"`
	BodyRejected  uint64 `json:"body_rejected"`
	ApplySuccess  uint64 `json:"apply_success"`
	ApplyFailures uint64 `json:"apply_failures"`
}

type Status struct {
	Revision         uint64           `json:"revision"`
	Hash             string           `json:"hash"`
	PreviousRevision uint64           `json:"previous_revision,omitempty"`
	AppliedAt        time.Time        `json:"applied_at"`
	LastError        string           `json:"last_error,omitempty"`
	Listeners        []ListenerStatus `json:"listeners"`
	Routes           []RouteStatus    `json:"routes"`
	Clusters         []ClusterStatus  `json:"clusters"`
	Stats            Stats            `json:"stats"`
}
