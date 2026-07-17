package server

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"

	"github.com/J0es1ick/test-assignment/internal/balancer"
	"github.com/J0es1ick/test-assignment/internal/ratelimit"
)

type Server struct {
	server   *http.Server
	balancer *balancer.LoadBalancer
	limiter  *ratelimit.TokenBucketLimiter
}

func NewServer(port string, balancer *balancer.LoadBalancer, limiter *ratelimit.TokenBucketLimiter) *Server {
	s := &Server{
		balancer: balancer,
		limiter:  limiter,
	}

	mux := http.NewServeMux()
	keyFunc := func(r *http.Request) string {
		return clientIP(r)
	}
	requestHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequest := r.Clone(r.Context())
		proxyRequest.URL.Path = "/"
		proxyRequest.URL.RawPath = ""
		balancer.ServeHTTP(w, proxyRequest)
	})
	rateLimitedRequest := ratelimit.RateLimitMiddleware(
		limiter,
		keyFunc,
	)(requestHandler)
	rateLimitedBalancer := ratelimit.RateLimitMiddleware(limiter, keyFunc)(balancer)

	mux.Handle("GET /api/dashboard/status", http.HandlerFunc(s.handleStatus))
	mux.Handle("GET /api/dashboard/request", rateLimitedRequest)
	mux.Handle("POST /api/dashboard/backends/{id}", http.HandlerFunc(s.handleBackendState))
	mux.Handle("POST /api/dashboard/limit", http.HandlerFunc(s.handleLimitReset))
	mux.Handle("/", rateLimitedBalancer)

	s.server = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	return s
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
	bucket, err := s.limiter.Snapshot(r.Context(), clientIP(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read rate limit state"})
		return
	}

	writeJSON(w, http.StatusOK, dashboardStatus{
		Mode:           "live",
		Strategy:       "round-robin",
		HealthInterval: "5s",
		HealthTimeout:  "2s",
		ClientIP:       clientIP(r),
		Backends:       s.balancer.Backends(),
		Bucket:         bucket,
	})
}

func (s *Server) handleBackendState(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Enabled bool `json:"enabled"`
	}

	decoder := json.NewDecoder(r.Body)
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

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Capacity < 1 || payload.Capacity > 10000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capacity must be between 1 and 10000"})
		return
	}

	bucket, err := s.limiter.Reset(r.Context(), clientIP(r), payload.Capacity)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to reset rate limit state"})
		return
	}

	writeJSON(w, http.StatusOK, bucket)
}

func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
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
	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down server")
	return s.server.Shutdown(ctx)
}
