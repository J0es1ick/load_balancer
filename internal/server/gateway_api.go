package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/gateway"
	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"golang.org/x/net/http/httpguts"
)

func (server *Server) ApplyGatewayConfig(ctx context.Context, value *config.GatewayConfig) error {
	server.configMu.Lock()
	defer server.configMu.Unlock()
	if server.gateway == nil {
		return fmt.Errorf("gateway is not enabled")
	}
	if value == nil {
		return fmt.Errorf("gateway configuration is required")
	}
	value.ApplyDefaults()
	if !reflect.DeepEqual(server.gateway.Config().Listeners, value.Listeners) {
		return fmt.Errorf("listener changes require restart")
	}
	return server.gateway.Apply(ctx, value)
}

func (server *Server) registerGatewayAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/status", server.handleGatewayStatus)
	mux.HandleFunc("GET /api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		if server.requireGateway(w) {
			writeJSON(w, http.StatusOK, server.gateway.Config())
		}
	})
	mux.HandleFunc("POST /api/v1/config/validate", server.handleGatewayValidate)
	mux.HandleFunc("PUT /api/v1/config", server.handleGatewayApply)
	mux.HandleFunc("POST /api/v1/config/rollback", server.handleGatewayRollback)
	mux.HandleFunc("PATCH /api/v1/clusters/{cluster}/endpoints/{id}", server.handleGatewayEndpoint)
	mux.HandleFunc("PUT /api/v1/discovery/{cluster}", server.handleDiscoveryUpdate)
	mux.HandleFunc("POST /api/v1/request", server.handleGatewayRequest)
	mux.HandleFunc("PATCH /api/v1/rate-limit", server.handleGatewayRateLimit)
	mux.HandleFunc("POST /api/v1/rate-limit/reset", server.handleGatewayRateLimitReset)
	mux.HandleFunc("GET /api/v1/audit", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, server.auditEvents()) })
	mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": observability.Version, "commit": observability.Commit, "build_date": observability.BuildDate})
	})
}

func (server *Server) handleGatewayRateLimit(writer http.ResponseWriter, request *http.Request) {
	if !server.requireGateway(writer) || !server.requireRuntimeMutations(writer) {
		return
	}
	var update struct {
		Enabled     *bool    `json:"enabled"`
		Capacity    *int     `json:"capacity"`
		Refill      *float64 `json:"refill_per_second"`
		FailureMode *string  `json:"failure_mode"`
	}
	if err := decodeJSON(writer, request, &update); err != nil {
		writeJSON(writer, 400, map[string]string{"error": err.Error()})
		return
	}
	if update.Enabled == nil && update.Capacity == nil && update.Refill == nil && update.FailureMode == nil {
		writeJSON(writer, 400, map[string]string{"error": "provide at least one rate-limit setting"})
		return
	}
	server.configMu.Lock()
	defer server.configMu.Unlock()
	settings := server.limiter.Settings()
	if update.Enabled != nil {
		settings.Enabled = *update.Enabled
	}
	if update.Capacity != nil {
		settings.Policy.Capacity = *update.Capacity
	}
	if update.Refill != nil {
		settings.Policy.RefillPerSecond = *update.Refill
	}
	if update.FailureMode != nil {
		settings.FailureMode = *update.FailureMode
	}
	if err := server.limiter.Reconfigure(settings); err != nil {
		writeJSON(writer, 400, map[string]string{"error": err.Error()})
		return
	}
	server.handleGatewayStatus(writer, request)
}

func (server *Server) handleGatewayRateLimitReset(writer http.ResponseWriter, request *http.Request) {
	if !server.requireGateway(writer) || !server.requireRuntimeMutations(writer) {
		return
	}
	var empty struct{}
	if err := decodeJSON(writer, request, &empty); err != nil {
		writeJSON(writer, 400, map[string]string{"error": err.Error()})
		return
	}
	server.configMu.Lock()
	defer server.configMu.Unlock()
	if _, err := server.limiter.Reset(request.Context(), server.clientIP(request), server.limiter.Settings().Policy.Capacity); err != nil {
		writeJSON(writer, 503, map[string]string{"error": "could not reset client bucket"})
		return
	}
	server.handleGatewayStatus(writer, request)
}

func (server *Server) requireGateway(writer http.ResponseWriter) bool {
	if server.gateway != nil {
		return true
	}
	writeJSON(writer, http.StatusNotImplemented, map[string]string{"error": "versioned gateway configuration is not enabled"})
	return false
}

func (server *Server) handleGatewayStatus(writer http.ResponseWriter, request *http.Request) {
	if !server.requireGateway(writer) {
		return
	}
	limiter := server.limiter.Settings()
	local := server.limiter.LocalStats()
	storageHealthy := server.limiter.Healthy(request.Context()) == nil
	ready := !server.shuttingDown.Load() && server.gateway.Ready() && (!limiter.Enabled || limiter.FailureMode != "fail-closed" || storageHealthy)
	var remaining *float64
	if bucket, err := server.limiter.Snapshot(request.Context(), server.clientIP(request)); err == nil {
		remaining = &bucket.Tokens
	}
	identity := principal(request.Context())
	writeJSON(writer, http.StatusOK, map[string]any{
		"mode": "live", "instance_id": server.instanceID, "runtime_mutations_enabled": server.runtimeMutations && (identity.Role == "admin" || identity.Role == "operator"), "principal": identity,
		"gateway": server.gateway.Status(), "ready": ready, "storage": map[string]any{"type": server.limiter.StorageName(), "healthy": storageHealthy, "degraded": limiter.Enabled && !storageHealthy && limiter.FailureMode != "fail-closed"},
		"rate_limit": rateLimitStatus{Enabled: limiter.Enabled, Capacity: limiter.Policy.Capacity, RefillPerSecond: limiter.Policy.RefillPerSecond, Remaining: remaining, FailureMode: limiter.FailureMode, OperationTimeout: limiter.OperationTimeout.String(), IPv4PrefixBits: limiter.IPv4PrefixBits, IPv6PrefixBits: limiter.IPv6PrefixBits, LocalBuckets: local.Buckets, LocalEvictions: local.Evictions},
		"protection": map[string]any{"overload": server.overload.Snapshot()},
	})
}

type configUpdate struct {
	Config           config.GatewayConfig `json:"config"`
	ExpectedRevision uint64               `json:"expected_revision"`
}

func (server *Server) handleGatewayValidate(writer http.ResponseWriter, request *http.Request) {
	if !server.requireGateway(writer) {
		return
	}
	var update configUpdate
	if err := decodeJSON(writer, request, &update); err != nil {
		writeJSON(writer, 400, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	if err := server.gateway.Validate(ctx, &update.Config); err != nil {
		writeJSON(writer, 400, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	writeJSON(writer, 200, map[string]bool{"valid": true})
}

func (server *Server) expectRevision(writer http.ResponseWriter, expected uint64) bool {
	if expected == 0 || server.gateway.Status().Revision != expected {
		writeJSON(writer, http.StatusConflict, map[string]string{"error": "configuration revision changed; refresh before applying"})
		return false
	}
	return true
}

func (server *Server) handleGatewayApply(writer http.ResponseWriter, request *http.Request) {
	if !server.requireGateway(writer) || !server.requireRuntimeMutations(writer) {
		return
	}
	var update configUpdate
	if err := decodeJSON(writer, request, &update); err != nil {
		writeJSON(writer, 400, map[string]string{"error": err.Error()})
		return
	}
	server.configMu.Lock()
	defer server.configMu.Unlock()
	if !server.expectRevision(writer, update.ExpectedRevision) {
		return
	}
	update.Config.ApplyDefaults()
	if !reflect.DeepEqual(server.gateway.Config().Listeners, update.Config.Listeners) {
		writeJSON(writer, 400, map[string]string{"error": "listener changes require a rolling restart"})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	if err := server.gateway.ApplyIfRevision(ctx, &update.Config, update.ExpectedRevision); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, gateway.ErrRevisionConflict) {
			status = http.StatusConflict
		}
		writeJSON(writer, status, map[string]string{"error": err.Error()})
		return
	}
	server.handleGatewayStatus(writer, request)
}

func (server *Server) handleGatewayRollback(writer http.ResponseWriter, request *http.Request) {
	if !server.requireGateway(writer) || !server.requireRuntimeMutations(writer) {
		return
	}
	var update struct {
		ExpectedRevision uint64 `json:"expected_revision"`
		Revision         uint64 `json:"revision"`
	}
	if err := decodeJSON(writer, request, &update); err != nil {
		writeJSON(writer, 400, map[string]string{"error": err.Error()})
		return
	}
	server.configMu.Lock()
	defer server.configMu.Unlock()
	if !server.expectRevision(writer, update.ExpectedRevision) {
		return
	}
	if update.Revision == 0 {
		update.Revision = server.gateway.Status().PreviousRevision
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	if err := server.gateway.RollbackIfRevision(ctx, update.Revision, update.ExpectedRevision); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, gateway.ErrRevisionConflict) {
			status = http.StatusConflict
		}
		writeJSON(writer, status, map[string]string{"error": err.Error()})
		return
	}
	server.handleGatewayStatus(writer, request)
}

func (server *Server) handleGatewayEndpoint(writer http.ResponseWriter, request *http.Request) {
	if !server.requireGateway(writer) || !server.requireRuntimeMutations(writer) {
		return
	}
	var update struct {
		Enabled  *bool `json:"enabled"`
		Draining *bool `json:"draining"`
	}
	if err := decodeJSON(writer, request, &update); err != nil || (update.Enabled == nil && update.Draining == nil) || (update.Enabled != nil && update.Draining != nil) || (update.Draining != nil && !*update.Draining) {
		writeJSON(writer, 400, map[string]string{"error": "provide exactly enabled:true/false or draining:true; enabled:true explicitly resumes an endpoint"})
		return
	}
	var err error
	if update.Draining != nil && *update.Draining {
		err = server.gateway.DrainEndpoint(request.PathValue("cluster"), request.PathValue("id"))
	} else if update.Enabled != nil {
		err = server.gateway.SetEndpoint(request.PathValue("cluster"), request.PathValue("id"), *update.Enabled)
	} else {
		err = server.gateway.SetEndpoint(request.PathValue("cluster"), request.PathValue("id"), true)
	}
	if err != nil {
		writeJSON(writer, 404, map[string]string{"error": err.Error()})
		return
	}
	server.handleGatewayStatus(writer, request)
}

type DiscoveryUpdate struct {
	Endpoints       []config.GatewayEndpointConfig `json:"endpoints"`
	Settings        config.GatewayDiscoveryConfig  `json:"settings"`
	ObservedAt      time.Time                      `json:"observed_at"`
	ResourceVersion string                         `json:"resource_version,omitempty"`
	Error           string                         `json:"error,omitempty"`
}

func (server *Server) handleDiscoveryUpdate(writer http.ResponseWriter, request *http.Request) {
	if !server.requireGateway(writer) {
		return
	}
	var update DiscoveryUpdate
	if err := decodeJSON(writer, request, &update); err != nil {
		writeJSON(writer, 400, map[string]string{"error": err.Error()})
		return
	}
	server.configMu.Lock()
	defer server.configMu.Unlock()
	clusterID := request.PathValue("cluster")
	found := false
	for _, cluster := range server.gateway.Config().Clusters {
		if cluster.ID == clusterID {
			found = true
			a, _ := json.Marshal(cluster.Discovery)
			b, _ := json.Marshal(update.Settings)
			if !bytes.Equal(a, b) || cluster.Discovery.Type == "static" {
				writeJSON(writer, 409, map[string]string{"error": "discovery source changed"})
				return
			}
		}
	}
	if !found {
		writeJSON(writer, 404, map[string]string{"error": "cluster not found"})
		return
	}
	if update.ObservedAt.IsZero() || update.ObservedAt.After(time.Now().Add(time.Minute)) {
		writeJSON(writer, 400, map[string]string{"error": "invalid discovery observation timestamp"})
		return
	}
	for _, cluster := range server.gateway.Status().Clusters {
		if cluster.ID == clusterID && update.ObservedAt.Before(cluster.Discovery.LastUpdate) {
			writeJSON(writer, 409, map[string]string{"error": "stale discovery observation"})
			return
		}
	}
	if update.Error != "" {
		if err := server.gateway.MarkDiscoveryErrorForSource(clusterID, fmt.Errorf("%s", update.Error), true, update.Settings); err != nil {
			writeJSON(writer, 409, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(writer, 200, map[string]bool{"accepted": true})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	err := server.gateway.UpdateEndpointsForSource(ctx, clusterID, update.Endpoints, gateway.DiscoveryStatus{Type: update.Settings.Type, LastUpdate: update.ObservedAt}, update.Settings)
	if err != nil {
		writeJSON(writer, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(writer, 200, map[string]bool{"accepted": true})
}

func (server *Server) handleGatewayRequest(writer http.ResponseWriter, request *http.Request) {
	if !server.requireGateway(writer) || !server.requireRuntimeMutations(writer) {
		return
	}
	var input struct {
		Method   string            `json:"method"`
		Host     string            `json:"host"`
		Path     string            `json:"path"`
		Headers  map[string]string `json:"headers"`
		Body     string            `json:"body"`
		Listener string            `json:"listener,omitempty"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeJSON(writer, 400, map[string]string{"error": err.Error()})
		return
	}
	input.Method = strings.ToUpper(input.Method)
	if input.Method == "" {
		input.Method = "GET"
	}
	switch input.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		writeJSON(writer, 400, map[string]string{"error": "unsupported playground method"})
		return
	}
	parsed, err := url.ParseRequestURI(input.Path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(input.Path, "/") || strings.ContainsAny(input.Host, "/\\\r\n\t @") || len(input.Body) > 256<<10 {
		writeJSON(writer, 400, map[string]string{"error": "provide a relative path, valid host and body up to 256 KiB"})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	ctx = gateway.WithDiagnostics(ctx)
	ctx = balancer.WithVerifiedClientIP(ctx, server.clientIP(request))
	ctx = balancer.WithVerifiedForwardedProto(ctx, "http")
	synthetic, err := http.NewRequestWithContext(ctx, input.Method, "http://playground"+input.Path, strings.NewReader(input.Body))
	if err != nil {
		writeJSON(writer, 400, map[string]string{"error": "invalid test request"})
		return
	}
	synthetic.Host = input.Host
	if synthetic.Host == "" {
		synthetic.Host = "localhost"
	}
	synthetic.URL.Scheme = ""
	synthetic.URL.Host = ""
	synthetic.RequestURI = input.Path
	synthetic.RemoteAddr = request.RemoteAddr
	for name, value := range input.Headers {
		if safePlaygroundHeader(name, value) {
			synthetic.Header.Set(name, value)
		}
	}
	recorder := &boundedResponse{header: make(http.Header), limit: 64 << 10, cancel: cancel}
	started := time.Now()
	handler := server.gateway.Handler(input.Listener)
	if err := executePlayground(server.proxyPipeline(handler), recorder, synthetic); err != nil && !recorder.truncated {
		if recorder.status == 0 {
			recorder.status = http.StatusBadGateway
		}
	}
	if recorder.status == 0 {
		recorder.status = 200
	}
	headers := map[string]string{}
	for key, values := range recorder.header {
		if !strings.EqualFold(key, "Set-Cookie") {
			headers[key] = strings.Join(values, ", ")
		}
	}
	attempts, _ := strconv.Atoi(recorder.header.Get("X-Balancer-Attempts"))
	writeJSON(writer, 200, map[string]any{"status": recorder.status, "headers": headers, "body": recorder.body.String(), "duration_ms": float64(time.Since(started).Microseconds()) / 1000, "truncated": recorder.truncated, "route": recorder.header.Get("X-Balancer-Route"), "cluster": recorder.header.Get("X-Balancer-Cluster"), "backend": recorder.header.Get("X-Balancer-Backend"), "attempts": attempts})
}

func executePlayground(handler http.Handler, writer http.ResponseWriter, request *http.Request) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			if failure == http.ErrAbortHandler {
				err = http.ErrAbortHandler
			} else {
				panic(failure)
			}
		}
	}()
	handler.ServeHTTP(writer, request)
	return nil
}

func safePlaygroundHeader(name, value string) bool {
	if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
		return false
	}
	key := strings.ToLower(name)
	if strings.HasPrefix(key, "x-balancer-") || strings.HasPrefix(key, "x-forwarded-") {
		return false
	}
	switch key {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "forwarded", "x-real-ip", "connection", "upgrade", "host", "content-length", "transfer-encoding", "trailer", "te", "expect":
		return false
	}
	return true
}

type boundedResponse struct {
	header    http.Header
	status    int
	body      bytes.Buffer
	limit     int
	truncated bool
	cancel    context.CancelFunc
}

func (response *boundedResponse) Header() http.Header { return response.header }
func (response *boundedResponse) WriteHeader(status int) {
	if status >= 200 && response.status == 0 {
		response.status = status
	}
}
func (response *boundedResponse) Flush() {}
func (response *boundedResponse) Write(value []byte) (int, error) {
	if response.status == 0 {
		response.status = 200
	}
	remaining := response.limit - response.body.Len()
	if len(value) > remaining {
		_, _ = response.body.Write(value[:remaining])
		response.truncated = true
		response.cancel()
		return remaining, io.ErrShortWrite
	}
	return response.body.Write(value)
}
