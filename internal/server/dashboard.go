package server

import (
	"log/slog"
	"net/http"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
)

type dashboardStatus struct {
	Mode             string                     `json:"mode"`
	Strategy         string                     `json:"strategy"`
	ClientIP         string                     `json:"client_ip"`
	Backends         []balancer.BackendSnapshot `json:"backends"`
	Bucket           ratelimit.BucketState      `json:"bucket"`
	RateLimit        rateLimitStatus            `json:"rate_limit"`
	Storage          string                     `json:"storage"`
	Health           healthStatus               `json:"health_check"`
	Retry            retryStatus                `json:"retry"`
	Protection       protectionStatus           `json:"protection"`
	Ready            bool                       `json:"ready"`
	RuntimeMutations bool                       `json:"runtime_mutations_enabled"`
	InstanceID       string                     `json:"instance_id"`
}

type rateLimitStatus struct {
	Enabled          bool    `json:"enabled"`
	Capacity         int     `json:"capacity"`
	RefillPerSecond  float64 `json:"refill_per_second"`
	FailureMode      string  `json:"failure_mode"`
	OperationTimeout string  `json:"operation_timeout"`
	IPv4PrefixBits   int     `json:"ipv4_prefix_bits"`
	IPv6PrefixBits   int     `json:"ipv6_prefix_bits"`
	LocalBuckets     int64   `json:"local_buckets"`
	LocalEvictions   uint64  `json:"local_evictions"`
}

type healthStatus struct {
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

type retryStatus struct {
	MaxAttempts   int                          `json:"max_attempts"`
	PerTryTimeout string                       `json:"per_try_timeout"`
	Methods       []string                     `json:"methods"`
	Statuses      []int                        `json:"statuses"`
	Budget        balancer.RetryBudgetSnapshot `json:"budget"`
}

type protectionStatus struct {
	Overload             OverloadSnapshot `json:"overload"`
	BackendMaxConcurrent int64            `json:"backend_max_concurrent_requests"`
}

func (server *Server) handleStatus(writer http.ResponseWriter, request *http.Request) {
	clientIP := server.clientIP(request)
	bucket, err := server.limiter.Snapshot(request.Context(), clientIP)
	if err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "failed to read rate limit state"})
		return
	}
	health := server.health.Load()
	limiter := server.limiter.Settings()
	local := server.limiter.LocalStats()
	retry := server.balancer.RetryPolicy()
	writeJSON(writer, http.StatusOK, dashboardStatus{
		Mode: "live", Strategy: "round-robin", ClientIP: clientIP, Backends: server.balancer.Backends(), Bucket: bucket,
		RateLimit:  rateLimitStatus{Enabled: limiter.Enabled, Capacity: limiter.Policy.Capacity, RefillPerSecond: limiter.Policy.RefillPerSecond, FailureMode: limiter.FailureMode, OperationTimeout: limiter.OperationTimeout.String(), IPv4PrefixBits: limiter.IPv4PrefixBits, IPv6PrefixBits: limiter.IPv6PrefixBits, LocalBuckets: local.Buckets, LocalEvictions: local.Evictions},
		Storage:    server.limiter.StorageName(),
		Health:     healthStatus{Mode: health.Mode, Path: health.Path, Interval: health.Interval.String(), Timeout: health.Timeout.String(), FailureThreshold: health.FailureThreshold, SuccessThreshold: health.SuccessThreshold, MaxConcurrency: health.MaxConcurrency, Jitter: health.Jitter.String(), Cooldown: health.Cooldown.String(), ExpectedStatuses: append([]int(nil), health.ExpectedStatuses...), SlowStart: health.SlowStart.String(), SlowStartMinimum: health.SlowStartMinimum},
		Retry:      retryStatus{MaxAttempts: retry.MaxAttempts, PerTryTimeout: retry.PerTryTimeout.String(), Methods: append([]string(nil), retry.Methods...), Statuses: append([]int(nil), retry.Statuses...), Budget: server.balancer.RetryBudget()},
		Protection: protectionStatus{Overload: server.overload.Snapshot(), BackendMaxConcurrent: server.balancer.BackendPolicy().MaxConcurrentRequests},
		Ready:      server.balancer.Ready(), RuntimeMutations: server.runtimeMutations, InstanceID: server.instanceID,
	})
}

func (server *Server) handleDashboardRequest(writer http.ResponseWriter, request *http.Request) {
	proxyRequest := request.Clone(request.Context())
	proxyRequest.URL.Path, proxyRequest.URL.RawPath = "/", ""
	server.balancer.ServeHTTP(writer, proxyRequest)
}

func (server *Server) handleBackendState(writer http.ResponseWriter, request *http.Request) {
	if !server.requireRuntimeMutations(writer) {
		return
	}
	var payload struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(writer, request, &payload); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !server.balancer.SetBackendEnabled(request.PathValue("id"), payload.Enabled) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "backend not found"})
		return
	}
	slog.InfoContext(request.Context(), "backend routing state changed", "backend", request.PathValue("id"), "enabled", payload.Enabled, "client_ip", server.clientIP(request), "request_id", RequestID(request.Context()))
	writeJSON(writer, http.StatusOK, map[string]any{"id": request.PathValue("id"), "enabled": payload.Enabled})
}

func (server *Server) handleBackendDrain(writer http.ResponseWriter, request *http.Request) {
	if !server.requireRuntimeMutations(writer) {
		return
	}
	id := request.PathValue("id")
	if !server.balancer.DrainBackend(id) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "backend not found"})
		return
	}
	slog.InfoContext(request.Context(), "backend draining started", "backend", id, "client_ip", server.clientIP(request), "request_id", RequestID(request.Context()))
	writeJSON(writer, http.StatusAccepted, map[string]any{"id": id, "draining": true})
}

func (server *Server) handleLimitReset(writer http.ResponseWriter, request *http.Request) {
	if !server.requireRuntimeMutations(writer) {
		return
	}
	var payload struct {
		Capacity int `json:"capacity"`
	}
	if err := decodeJSON(writer, request, &payload); err != nil || payload.Capacity < 1 || payload.Capacity > 1_000_000 {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "capacity must be between 1 and 1000000"})
		return
	}
	bucket, err := server.limiter.Reset(request.Context(), server.clientIP(request), payload.Capacity)
	if err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "failed to reset rate limit state"})
		return
	}
	slog.InfoContext(request.Context(), "rate limit bucket reset", "capacity", payload.Capacity, "client_ip", server.clientIP(request), "request_id", RequestID(request.Context()))
	writeJSON(writer, http.StatusOK, bucket)
}

func (server *Server) handleRuntimeUpdate(writer http.ResponseWriter, request *http.Request) {
	if !server.requireRuntimeMutations(writer) {
		return
	}
	if server.applyRuntime == nil {
		writeJSON(writer, http.StatusNotImplemented, map[string]string{"error": "runtime updates are disabled"})
		return
	}
	var update RuntimeUpdate
	if err := decodeJSON(writer, request, &update); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := server.applyRuntime(request.Context(), update); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	slog.InfoContext(request.Context(), "runtime configuration updated", "client_ip", server.clientIP(request), "request_id", RequestID(request.Context()))
	writeJSON(writer, http.StatusOK, map[string]string{"status": "applied"})
}

func (server *Server) requireRuntimeMutations(writer http.ResponseWriter) bool {
	if server.runtimeMutations {
		return true
	}
	writeJSON(writer, http.StatusForbidden, map[string]string{"error": "runtime mutations are disabled; update the declarative configuration and roll out all replicas"})
	return false
}

func (server *Server) handleLiveness(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "alive"})
}

func (server *Server) handleReadiness(writer http.ResponseWriter, request *http.Request) {
	backendReady := server.balancer.Ready()
	limiterError := server.limiter.Healthy(request.Context())
	settings := server.limiter.Settings()
	ready := backendReady && (settings.FailureMode != "fail-closed" || limiterError == nil)
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(writer, status, map[string]any{"ready": ready, "backends": backendReady, "rate_limit_storage": limiterError == nil, "failure_mode": settings.FailureMode})
}
