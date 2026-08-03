package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/J0es1ick/test-assignment/internal/balancer"
	"github.com/J0es1ick/test-assignment/internal/ratelimit"
)

type Options struct {
	Port              string
	TrustedProxies    []string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	HealthInterval    time.Duration
	HealthTimeout     time.Duration
}

type Server struct {
	server   *http.Server
	balancer *balancer.LoadBalancer
	limiter  *ratelimit.TokenBucketLimiter
	resolver atomic.Pointer[clientIPResolver]
	status   atomic.Pointer[runtimeStatus]
}

type runtimeStatus struct {
	healthInterval time.Duration
	healthTimeout  time.Duration
}

func NewServer(options Options, loadBalancer *balancer.LoadBalancer, limiter *ratelimit.TokenBucketLimiter) (*Server, error) {
	if options.Port == "" {
		return nil, fmt.Errorf("server port is required")
	}
	if options.ReadHeaderTimeout <= 0 ||
		options.ReadTimeout <= 0 ||
		options.WriteTimeout <= 0 ||
		options.IdleTimeout <= 0 {
		return nil, fmt.Errorf("HTTP server timeouts must be positive")
	}
	if options.MaxHeaderBytes < 1024 {
		return nil, fmt.Errorf("max header bytes must be at least 1024")
	}

	resolver, err := newClientIPResolver(options.TrustedProxies)
	if err != nil {
		return nil, err
	}
	if options.HealthInterval <= 0 || options.HealthTimeout <= 0 {
		return nil, fmt.Errorf("health-check interval and timeout must be positive")
	}

	s := &Server{
		balancer: loadBalancer,
		limiter:  limiter,
	}
	s.resolver.Store(resolver)
	s.status.Store(&runtimeStatus{
		healthInterval: options.HealthInterval,
		healthTimeout:  options.HealthTimeout,
	})

	mux := http.NewServeMux()
	keyFunc := func(r *http.Request) string {
		return s.clientIP(r)
	}
	requestHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequest := r.Clone(r.Context())
		proxyRequest.URL.Path = "/"
		proxyRequest.URL.RawPath = ""
		loadBalancer.ServeHTTP(w, proxyRequest)
	})
	rateLimitedRequest := ratelimit.RateLimitMiddleware(limiter, keyFunc)(requestHandler)
	rateLimitedBalancer := ratelimit.RateLimitMiddleware(limiter, keyFunc)(loadBalancer)

	mux.Handle("GET /api/dashboard/status", http.HandlerFunc(s.handleStatus))
	mux.Handle("GET /api/dashboard/request", rateLimitedRequest)
	mux.Handle("POST /api/dashboard/backends/{id}", http.HandlerFunc(s.handleBackendState))
	mux.Handle("POST /api/dashboard/limit", http.HandlerFunc(s.handleLimitReset))
	mux.Handle("/", rateLimitedBalancer)

	s.server = &http.Server{
		Addr:              ":" + options.Port,
		Handler:           mux,
		ReadHeaderTimeout: options.ReadHeaderTimeout,
		ReadTimeout:       options.ReadTimeout,
		WriteTimeout:      options.WriteTimeout,
		IdleTimeout:       options.IdleTimeout,
		MaxHeaderBytes:    options.MaxHeaderBytes,
	}

	return s, nil
}

func (s *Server) UpdateRuntime(trustedProxies []string, healthInterval, healthTimeout time.Duration) error {
	resolver, err := newClientIPResolver(trustedProxies)
	if err != nil {
		return err
	}
	if healthInterval <= 0 || healthTimeout <= 0 {
		return fmt.Errorf("health-check interval and timeout must be positive")
	}

	s.resolver.Store(resolver)
	s.status.Store(&runtimeStatus{
		healthInterval: healthInterval,
		healthTimeout:  healthTimeout,
	})
	return nil
}

type dashboardStatus struct {
	Mode           string                     `json:"mode"`
	Strategy       string                     `json:"strategy"`
	HealthInterval string                     `json:"health_interval"`
	HealthTimeout  string                     `json:"health_timeout"`
	ClientIP       string                     `json:"client_ip"`
	Backends       []balancer.BackendSnapshot `json:"backends"`
	Bucket         ratelimit.BucketState      `json:"bucket"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	clientIP := s.clientIP(r)
	bucket, err := s.limiter.Snapshot(r.Context(), clientIP)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read rate limit state"})
		return
	}
	status := s.status.Load()

	writeJSON(w, http.StatusOK, dashboardStatus{
		Mode:           "live",
		Strategy:       "round-robin",
		HealthInterval: status.healthInterval.String(),
		HealthTimeout:  status.healthTimeout.String(),
		ClientIP:       clientIP,
		Backends:       s.balancer.Backends(),
		Bucket:         bucket,
	})
}

func (s *Server) handleBackendState(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Enabled bool `json:"enabled"`
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if !s.balancer.SetBackendEnabled(r.PathValue("id"), payload.Enabled) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "backend not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      r.PathValue("id"),
		"enabled": payload.Enabled,
	})
}

func (s *Server) handleLimitReset(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Capacity int `json:"capacity"`
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Capacity < 1 || payload.Capacity > 10000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capacity must be between 1 and 10000"})
		return
	}

	bucket, err := s.limiter.Reset(r.Context(), s.clientIP(r), payload.Capacity)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to reset rate limit state"})
		return
	}

	writeJSON(w, http.StatusOK, bucket)
}

type clientIPResolver struct {
	trusted []netip.Prefix
}

func newClientIPResolver(values []string) (*clientIPResolver, error) {
	resolver := &clientIPResolver{trusted: make([]netip.Prefix, 0, len(values))}
	for _, value := range values {
		prefix, err := parsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: %w", value, err)
		}
		resolver.trusted = append(resolver.trusted, prefix)
	}
	return resolver, nil
}

func (s *Server) clientIP(r *http.Request) string {
	remoteIP, ok := parseRemoteIP(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}

	resolver := s.resolver.Load()
	if resolver == nil || !resolver.isTrusted(remoteIP) {
		return remoteIP.String()
	}

	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		candidate := remoteIP
		for index := len(parts) - 1; index >= 0; index-- {
			address, err := netip.ParseAddr(strings.TrimSpace(parts[index]))
			if err != nil {
				return remoteIP.String()
			}
			address = address.Unmap()
			candidate = address
			if !resolver.isTrusted(address) {
				return address.String()
			}
		}
		return candidate.String()
	}

	if realIP, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
		return realIP.Unmap().String()
	}
	return remoteIP.String()
}

func (resolver *clientIPResolver) isTrusted(address netip.Addr) bool {
	address = address.Unmap()
	for _, prefix := range resolver.trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func parseRemoteIP(remoteAddress string) (netip.Addr, bool) {
	if addressPort, err := netip.ParseAddrPort(remoteAddress); err == nil {
		return addressPort.Addr().Unmap(), true
	}
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func parsePrefix(value string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked(), nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(address, address.BitLen()), nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("failed to write JSON response: %v", err)
	}
}

func (s *Server) Handler() http.Handler {
	return s.server.Handler
}

func (s *Server) Start() error {
	log.Printf("Starting server on %s", s.server.Addr)
	err := s.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down server")
	return s.server.Shutdown(ctx)
}
