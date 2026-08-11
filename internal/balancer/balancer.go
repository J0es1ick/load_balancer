package balancer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"time"
)

var ErrNoBackend = errors.New("no healthy backend available")

type verifiedClientIPContextKey struct{}
type verifiedForwardedProtoContextKey struct{}

func WithVerifiedClientIP(ctx context.Context, clientIP string) context.Context {
	return context.WithValue(ctx, verifiedClientIPContextKey{}, clientIP)
}

func WithVerifiedForwardedProto(ctx context.Context, protocol string) context.Context {
	return context.WithValue(ctx, verifiedForwardedProtoContextKey{}, protocol)
}

func verifiedClientIP(ctx context.Context) string {
	value, _ := ctx.Value(verifiedClientIPContextKey{}).(string)
	return value
}

func verifiedForwardedProto(ctx context.Context) string {
	value, _ := ctx.Value(verifiedForwardedProtoContextKey{}).(string)
	return value
}

type RetryPolicy struct {
	MaxAttempts           int
	PerTryTimeout         time.Duration
	BodyLimit             int64
	Methods               []string
	Statuses              []int
	BudgetCapacity        int
	BudgetRefillPerSecond float64
}

type ProxyObserver interface {
	ObserveBackendAttempt(backendID string, status int, duration time.Duration, err error, retry bool)
}

type ProtectionObserver interface {
	ObserveProtectionEvent(kind, backendID string)
}

type LoadBalancerOptions struct {
	Transport http.RoundTripper
	Retry     RetryPolicy
	Observer  ProxyObserver
}

type LoadBalancer struct {
	pool      *BackendPool
	strategy  Strategy
	proxy     *httputil.ReverseProxy
	transport *retryTransport
}

func NewLoadBalancer(pool *BackendPool, strategy Strategy, options ...LoadBalancerOptions) *LoadBalancer {
	selected := LoadBalancerOptions{
		Transport: http.DefaultTransport,
		Retry:     RetryPolicy{MaxAttempts: 1, PerTryTimeout: 10 * time.Second, BodyLimit: 1 << 20, Methods: []string{"GET", "HEAD", "OPTIONS"}, BudgetCapacity: 100, BudgetRefillPerSecond: 10},
	}
	if len(options) > 0 {
		selected = options[0]
	}
	if selected.Transport == nil {
		selected.Transport = http.DefaultTransport
	}
	transport := &retryTransport{pool: pool, strategy: strategy, base: selected.Transport, observer: selected.Observer, budget: newRetryBudget(selected.Retry.BudgetCapacity, selected.Retry.BudgetRefillPerSecond)}
	transport.UpdatePolicy(selected.Retry)
	lb := &LoadBalancer{pool: pool, strategy: strategy, transport: transport}
	lb.proxy = &httputil.ReverseProxy{
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
				proxyRequest.Out.Header.Del(header)
			}
			if clientIP := verifiedClientIP(proxyRequest.In.Context()); clientIP != "" {
				proxyRequest.Out.Header.Set("X-Forwarded-For", clientIP)
				proxyRequest.Out.Header.Set("X-Real-IP", clientIP)
			}
			if proxyRequest.In.Host != "" {
				proxyRequest.Out.Header.Set("X-Forwarded-Host", proxyRequest.In.Host)
			}
			protocol := verifiedForwardedProto(proxyRequest.In.Context())
			if protocol == "" {
				protocol = "http"
				if proxyRequest.In.TLS != nil {
					protocol = "https"
				}
			}
			proxyRequest.Out.Header.Set("X-Forwarded-Proto", protocol)
			proxyRequest.Out.Header.Set("X-Balancer-Proxy", "go-load-balancer")
		},
		Transport: transport,
		ErrorHandler: func(writer http.ResponseWriter, request *http.Request, err error) {
			if errors.Is(err, ErrNoBackend) {
				http.Error(writer, "Service not available", http.StatusServiceUnavailable)
				return
			}
			http.Error(writer, "Bad gateway", http.StatusBadGateway)
		},
	}
	return lb
}

func (lb *LoadBalancer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	lb.prepareReplayableBody(request)
	lb.proxy.ServeHTTP(writer, request)
}

func (lb *LoadBalancer) prepareReplayableBody(request *http.Request) {
	policy := lb.transport.Policy()
	if policy.MaxAttempts < 2 || !policy.allowsMethod(request.Method) || request.Body == nil || request.Body == http.NoBody || request.GetBody != nil {
		return
	}
	if request.ContentLength < 0 || request.ContentLength > policy.BodyLimit {
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, policy.BodyLimit+1))
	if err != nil || int64(len(body)) > policy.BodyLimit {
		return
	}
	_ = request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
}

func (lb *LoadBalancer) UpdateRetryPolicy(policy RetryPolicy) { lb.transport.UpdatePolicy(policy) }
func (lb *LoadBalancer) RetryPolicy() RetryPolicy             { return lb.transport.Policy() }
func (lb *LoadBalancer) RetryBudget() RetryBudgetSnapshot     { return lb.transport.budget.Snapshot() }
func (lb *LoadBalancer) Backends() []BackendSnapshot          { return lb.pool.Snapshot() }
func (lb *LoadBalancer) Ready() bool                          { return lb.pool.Ready() }
func (lb *LoadBalancer) BackendPolicy() PassivePolicy         { return lb.pool.PassivePolicy() }
func (lb *LoadBalancer) SetBackendEnabled(id string, enabled bool) bool {
	return lb.pool.SetBackendEnabled(id, enabled)
}
func (lb *LoadBalancer) DrainBackend(id string) bool { return lb.pool.DrainBackend(id) }
