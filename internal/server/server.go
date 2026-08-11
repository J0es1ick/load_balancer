package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"sync/atomic"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
)

type RuntimeUpdate struct {
	RateLimit *RateLimitUpdate `json:"rate_limit,omitempty"`
	Health    *HealthUpdate    `json:"health_check,omitempty"`
	Retry     *RetryUpdate     `json:"retry,omitempty"`
}

type RateLimitUpdate struct {
	Capacity        int     `json:"capacity"`
	RefillPerSecond float64 `json:"refill_per_second"`
	FailureMode     string  `json:"failure_mode"`
}
type HealthUpdate struct {
	Mode             string `json:"mode"`
	Path             string `json:"path"`
	Interval         string `json:"interval"`
	Timeout          string `json:"timeout"`
	FailureThreshold int    `json:"failure_threshold"`
	SuccessThreshold int    `json:"success_threshold"`
	MaxConcurrency   int    `json:"max_concurrency"`
	Jitter           string `json:"jitter"`
	Cooldown         string `json:"cooldown"`
	ExpectedStatuses []int  `json:"expected_statuses"`
	SlowStart        string `json:"slow_start"`
	SlowStartMinimum int    `json:"slow_start_minimum_percent"`
}
type RetryUpdate struct {
	MaxAttempts           int      `json:"max_attempts"`
	PerTryTimeout         string   `json:"per_try_timeout"`
	Methods               []string `json:"methods"`
	Statuses              []int    `json:"statuses"`
	BudgetCapacity        int      `json:"budget_capacity"`
	BudgetRefillPerSecond float64  `json:"budget_refill_per_second"`
}

type Options struct {
	Port                   string
	ManagementEnabled      bool
	ManagementAddress      string
	ManagementAuthToken    string
	ManagementInsecure     bool
	RuntimeMutations       bool
	InstanceID             string
	EnablePprof            bool
	TrustedProxies         []string
	AccessLogSampleRate    float64
	ReadHeaderTimeout      time.Duration
	ReadTimeout            time.Duration
	WriteTimeout           time.Duration
	ManagementWriteTimeout time.Duration
	IdleTimeout            time.Duration
	MaxHeaderBytes         int
	OverloadMaxConcurrent  int
	OverloadQueueTimeout   time.Duration
	Health                 balancer.HealthSettings
	Metrics                *observability.Metrics
	ApplyRuntime           func(context.Context, RuntimeUpdate) error
}

type Server struct {
	publicServer        *http.Server
	managementServer    *http.Server
	balancer            *balancer.LoadBalancer
	limiter             *ratelimit.TokenBucketLimiter
	metrics             *observability.Metrics
	resolver            atomic.Pointer[clientIPResolver]
	health              atomic.Pointer[balancer.HealthSettings]
	applyRuntime        func(context.Context, RuntimeUpdate) error
	runtimeMutations    bool
	instanceID          string
	overload            *overloadController
	accessLogSampleRate float64
}

func NewServer(options Options, loadBalancer *balancer.LoadBalancer, limiter *ratelimit.TokenBucketLimiter) (*Server, error) {
	if options.Port == "" {
		return nil, fmt.Errorf("server port is required")
	}
	if options.ReadHeaderTimeout <= 0 || options.ReadTimeout <= 0 || options.WriteTimeout < 0 || options.IdleTimeout <= 0 {
		return nil, fmt.Errorf("HTTP server timeouts are invalid")
	}
	if options.ManagementEnabled && options.ManagementWriteTimeout <= 0 {
		return nil, fmt.Errorf("management write timeout must be positive")
	}
	if options.MaxHeaderBytes < 1024 {
		return nil, fmt.Errorf("max header bytes must be at least 1024")
	}
	if options.OverloadMaxConcurrent < 1 || options.OverloadQueueTimeout < 0 {
		return nil, fmt.Errorf("overload protection values are invalid")
	}
	if options.AccessLogSampleRate < 0 || options.AccessLogSampleRate > 1 {
		return nil, fmt.Errorf("access log sample rate must be between 0 and 1")
	}
	if options.ManagementEnabled && options.ManagementAddress == "" {
		return nil, fmt.Errorf("management address is required")
	}
	if options.ManagementEnabled && options.ManagementAuthToken == "" && !options.ManagementInsecure {
		return nil, fmt.Errorf("management auth token is required")
	}
	resolver, err := newClientIPResolver(options.TrustedProxies)
	if err != nil {
		return nil, err
	}
	if options.Metrics == nil {
		options.Metrics = observability.NewMetrics()
	}
	instanceID := options.InstanceID
	if instanceID == "" {
		instanceID = "local"
	}
	server := &Server{balancer: loadBalancer, limiter: limiter, metrics: options.Metrics, applyRuntime: options.ApplyRuntime, runtimeMutations: options.RuntimeMutations, instanceID: instanceID, accessLogSampleRate: options.AccessLogSampleRate}
	server.overload = newOverloadController(options.OverloadMaxConcurrent, options.OverloadQueueTimeout, options.Metrics)
	server.resolver.Store(resolver)
	server.health.Store(cloneHealth(options.Health))

	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /healthz", server.handleLiveness)
	publicMux.HandleFunc("GET /readyz", server.handleReadiness)
	protectedProxy := server.overload.middleware(server.withVerifiedClientIP(ratelimit.RateLimitMiddleware(limiter, server.clientIP)(loadBalancer)))
	publicMux.Handle("/", protectedProxy)
	server.publicServer = newHTTPServer(":"+options.Port, server.instrument("public", publicMux), options, options.WriteTimeout)

	if options.ManagementEnabled {
		managementMux := http.NewServeMux()
		managementMux.HandleFunc("GET /healthz", server.handleLiveness)
		managementMux.HandleFunc("GET /readyz", server.handleReadiness)
		managementMux.Handle("GET /metrics", options.Metrics)
		managementMux.HandleFunc("GET /api/dashboard/status", server.handleStatus)
		managementMux.Handle("GET /api/dashboard/request", server.overload.middleware(server.withVerifiedClientIP(ratelimit.RateLimitMiddleware(limiter, server.clientIP)(http.HandlerFunc(server.handleDashboardRequest)))))
		managementMux.HandleFunc("POST /api/dashboard/backends", server.handleBackendCount)
		managementMux.HandleFunc("POST /api/dashboard/backends/{id}", server.handleBackendState)
		managementMux.HandleFunc("POST /api/dashboard/backends/{id}/drain", server.handleBackendDrain)
		managementMux.HandleFunc("POST /api/dashboard/limit", server.handleLimitReset)
		managementMux.HandleFunc("PATCH /api/dashboard/config", server.handleRuntimeUpdate)
		if options.EnablePprof {
			registerPprof(managementMux)
		}
		managementHandler := server.managementAuth(options.ManagementAuthToken, options.ManagementInsecure, server.managementMutationGuard(managementMux))
		server.managementServer = newHTTPServer(options.ManagementAddress, server.instrument("management", managementHandler), options, options.ManagementWriteTimeout)
	}
	return server, nil
}

func registerPprof(mux *http.ServeMux) {
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("POST /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
}
