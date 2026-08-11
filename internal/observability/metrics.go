package observability

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var latencyBuckets = []float64{0.001, 0.003, 0.01, 0.03, 0.1, 0.3, 1, 3, 10}

type histogramSeries struct {
	mu     sync.Mutex
	counts []uint64
	count  uint64
	sum    float64
}

type histogramSnapshot struct {
	counts []uint64
	count  uint64
	sum    float64
}

type Metrics struct {
	httpSeries       sync.Map
	upstreamAttempts sync.Map
	upstreamDuration sync.Map
	protectionEvents sync.Map
	inflightRequests atomic.Int64
	providersMu      sync.RWMutex
	backendProvider  func() []BackendMetric
	limiterProvider  func() LimiterMetric
}

type BackendMetric struct {
	ID        string
	Available bool
}
type LimiterMetric struct {
	Storage        string
	Healthy        bool
	Degraded       bool
	LocalBuckets   int64
	LocalEvictions uint64
}

func NewMetrics() *Metrics {
	return &Metrics{}
}

func (metrics *Metrics) ObserveProtectionEvent(kind, backendID string) {
	counterFor(&metrics.protectionEvents, kind+"\x00"+backendID).Add(1)
}

func (metrics *Metrics) SetInflightRequests(value int64) {
	metrics.inflightRequests.Store(value)
}

func (metrics *Metrics) SetProviders(backends func() []BackendMetric, limiter func() LimiterMetric) {
	metrics.providersMu.Lock()
	metrics.backendProvider = backends
	metrics.limiterProvider = limiter
	metrics.providersMu.Unlock()
}

func (metrics *Metrics) ObserveHTTPRequest(listener string, status int, duration time.Duration) {
	key := listener + "\x00" + strconv.Itoa(status)
	observeHistogram(histogramFor(&metrics.httpSeries, key), duration.Seconds())
}

func (metrics *Metrics) ObserveBackendAttempt(backendID string, status int, duration time.Duration, err error, retry bool) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	} else if status >= 500 {
		outcome = "5xx"
	}
	key := backendID + "\x00" + outcome + "\x00" + strconv.FormatBool(retry)
	counterFor(&metrics.upstreamAttempts, key).Add(1)
	observeHistogram(histogramFor(&metrics.upstreamDuration, backendID), duration.Seconds())
}

func (metrics *Metrics) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	httpDuration := snapshotHistograms(&metrics.httpSeries)
	httpRequests := make(map[string]uint64, len(httpDuration))
	for key, entry := range httpDuration {
		httpRequests[key] = entry.count
	}
	upstreamAttempts := snapshotCounters(&metrics.upstreamAttempts)
	upstreamDuration := snapshotHistograms(&metrics.upstreamDuration)
	protectionEvents := snapshotCounters(&metrics.protectionEvents)
	inflightRequests := metrics.inflightRequests.Load()
	metrics.providersMu.RLock()
	backendProvider := metrics.backendProvider
	limiterProvider := metrics.limiterProvider
	metrics.providersMu.RUnlock()
	writeLine(writer, "# HELP load_balancer_http_requests_total Requests handled by listener and status code.")
	writeLine(writer, "# TYPE load_balancer_http_requests_total counter")
	for _, key := range sortedKeys(httpRequests) {
		parts := strings.Split(key, "\x00")
		writeLine(writer, fmt.Sprintf("load_balancer_http_requests_total{listener=%q,code=%q} %d", parts[0], parts[1], httpRequests[key]))
	}
	writeHistograms(writer, "load_balancer_http_request_duration_seconds", "Request latency by listener and status.", httpDuration, func(key string) string {
		parts := strings.Split(key, "\x00")
		return fmt.Sprintf("listener=%q,code=%q", parts[0], parts[1])
	})
	writeLine(writer, "# HELP load_balancer_upstream_attempts_total Requests attempted against an upstream backend.")
	writeLine(writer, "# TYPE load_balancer_upstream_attempts_total counter")
	for _, key := range sortedKeys(upstreamAttempts) {
		parts := strings.Split(key, "\x00")
		writeLine(writer, fmt.Sprintf("load_balancer_upstream_attempts_total{backend=%q,outcome=%q,retry=%q} %d", parts[0], parts[1], parts[2], upstreamAttempts[key]))
	}
	writeHistograms(writer, "load_balancer_upstream_duration_seconds", "Upstream attempt latency by backend.", upstreamDuration, func(key string) string { return fmt.Sprintf("backend=%q", key) })
	writeLine(writer, "# HELP load_balancer_protection_events_total Requests affected by overload, retry-budget or backend concurrency protection.")
	writeLine(writer, "# TYPE load_balancer_protection_events_total counter")
	for _, key := range sortedKeys(protectionEvents) {
		parts := strings.Split(key, "\x00")
		writeLine(writer, fmt.Sprintf("load_balancer_protection_events_total{kind=%q,backend=%q} %d", parts[0], parts[1], protectionEvents[key]))
	}
	writeLine(writer, "# HELP load_balancer_inflight_requests Current requests admitted to the public data plane.")
	writeLine(writer, "# TYPE load_balancer_inflight_requests gauge")
	writeLine(writer, fmt.Sprintf("load_balancer_inflight_requests %d", inflightRequests))
	if backendProvider != nil {
		writeLine(writer, "# HELP load_balancer_backend_available Whether a backend is eligible for routing.")
		writeLine(writer, "# TYPE load_balancer_backend_available gauge")
		for _, backend := range backendProvider() {
			writeLine(writer, fmt.Sprintf("load_balancer_backend_available{backend=%q} %d", backend.ID, boolNumber(backend.Available)))
		}
	}
	if limiterProvider != nil {
		limiter := limiterProvider()
		writeLine(writer, "# HELP load_balancer_rate_limit_storage_healthy Whether the configured rate-limit storage is reachable.")
		writeLine(writer, "# TYPE load_balancer_rate_limit_storage_healthy gauge")
		writeLine(writer, fmt.Sprintf("load_balancer_rate_limit_storage_healthy{storage=%q} %d", limiter.Storage, boolNumber(limiter.Healthy)))
		writeLine(writer, "# HELP load_balancer_rate_limit_degraded Whether traffic is using a degraded rate-limit policy.")
		writeLine(writer, "# TYPE load_balancer_rate_limit_degraded gauge")
		writeLine(writer, fmt.Sprintf("load_balancer_rate_limit_degraded{storage=%q} %d", limiter.Storage, boolNumber(limiter.Degraded)))
		writeLine(writer, "# HELP load_balancer_rate_limit_local_buckets Buckets currently held by the bounded local store or fallback.")
		writeLine(writer, "# TYPE load_balancer_rate_limit_local_buckets gauge")
		writeLine(writer, fmt.Sprintf("load_balancer_rate_limit_local_buckets %d", limiter.LocalBuckets))
		writeLine(writer, "# HELP load_balancer_rate_limit_local_evictions_total Buckets evicted by the bounded local store or fallback.")
		writeLine(writer, "# TYPE load_balancer_rate_limit_local_evictions_total counter")
		writeLine(writer, fmt.Sprintf("load_balancer_rate_limit_local_evictions_total %d", limiter.LocalEvictions))
	}
}

func counterFor(values *sync.Map, key string) *atomic.Uint64 {
	if actual, exists := values.Load(key); exists {
		return actual.(*atomic.Uint64)
	}
	counter := new(atomic.Uint64)
	actual, _ := values.LoadOrStore(key, counter)
	return actual.(*atomic.Uint64)
}

func histogramFor(values *sync.Map, key string) *histogramSeries {
	if actual, exists := values.Load(key); exists {
		return actual.(*histogramSeries)
	}
	histogram := &histogramSeries{counts: make([]uint64, len(latencyBuckets))}
	actual, _ := values.LoadOrStore(key, histogram)
	return actual.(*histogramSeries)
}

func observeHistogram(entry *histogramSeries, value float64) {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.count++
	entry.sum += value
	for index, upper := range latencyBuckets {
		if value <= upper {
			entry.counts[index]++
		}
	}
}

func snapshotCounters(values *sync.Map) map[string]uint64 {
	result := make(map[string]uint64)
	values.Range(func(key, value any) bool {
		result[key.(string)] = value.(*atomic.Uint64).Load()
		return true
	})
	return result
}

func snapshotHistograms(values *sync.Map) map[string]*histogramSnapshot {
	result := make(map[string]*histogramSnapshot)
	values.Range(func(key, value any) bool {
		entry := value.(*histogramSeries)
		entry.mu.Lock()
		snapshot := &histogramSnapshot{counts: append([]uint64(nil), entry.counts...), count: entry.count, sum: entry.sum}
		entry.mu.Unlock()
		result[key.(string)] = snapshot
		return true
	})
	return result
}

func writeHistograms(writer io.Writer, name, help string, values map[string]*histogramSnapshot, labels func(string) string) {
	writeLine(writer, "# HELP "+name+" "+help)
	writeLine(writer, "# TYPE "+name+" histogram")
	for _, key := range sortedKeys(values) {
		entry := values[key]
		label := labels(key)
		for index, upper := range latencyBuckets {
			writeLine(writer, fmt.Sprintf("%s_bucket{%s,le=%q} %d", name, label, strconv.FormatFloat(upper, 'f', -1, 64), entry.counts[index]))
		}
		writeLine(writer, fmt.Sprintf("%s_bucket{%s,le=\"+Inf\"} %d", name, label, entry.count))
		writeLine(writer, fmt.Sprintf("%s_sum{%s} %g", name, label, entry.sum))
		writeLine(writer, fmt.Sprintf("%s_count{%s} %d", name, label, entry.count))
	}
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeLine(writer io.Writer, line string) { _, _ = io.WriteString(writer, line+"\n") }
func boolNumber(value bool) int {
	if value {
		return 1
	}
	return 0
}
