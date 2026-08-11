package observability_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsExposeRequestUpstreamProtectionAndProviderState(t *testing.T) {
	metrics := observability.NewMetrics()
	metrics.ObserveHTTPRequest("public", 200, 12*time.Millisecond)
	metrics.ObserveBackendAttempt("api-a", 200, 8*time.Millisecond, nil, false)
	metrics.ObserveProtectionEvent("overload", "")
	metrics.SetInflightRequests(3)
	metrics.SetProviders(
		func() []observability.BackendMetric {
			return []observability.BackendMetric{{ID: "api-a", Available: true}}
		},
		func() observability.LimiterMetric {
			return observability.LimiterMetric{Storage: "redis", Healthy: true, LocalBuckets: 7, LocalEvictions: 2}
		},
	)
	recorder := httptest.NewRecorder()
	metrics.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	require.Equal(t, 200, recorder.Code)
	body := recorder.Body.String()
	for _, expected := range []string{
		`load_balancer_http_requests_total{listener="public",code="200"} 1`,
		`load_balancer_upstream_attempts_total{backend="api-a",outcome="success",retry="false"} 1`,
		`load_balancer_protection_events_total{kind="overload",backend=""} 1`,
		`load_balancer_inflight_requests 3`,
		`load_balancer_backend_available{backend="api-a"} 1`,
		`load_balancer_rate_limit_storage_healthy{storage="redis"} 1`,
		`load_balancer_rate_limit_local_buckets 7`,
		`load_balancer_rate_limit_local_evictions_total 2`,
	} {
		assert.Contains(t, body, expected)
	}
}

func TestConcurrentObservationsAreCountedExactly(t *testing.T) {
	metrics := observability.NewMetrics()
	var waitGroup sync.WaitGroup
	for range 32 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for range 250 {
				metrics.ObserveHTTPRequest("public", http.StatusOK, time.Millisecond)
				metrics.ObserveBackendAttempt("api", http.StatusOK, time.Millisecond, nil, false)
			}
		}()
	}
	waitGroup.Wait()
	recorder := httptest.NewRecorder()
	metrics.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Contains(t, recorder.Body.String(), fmt.Sprintf(`load_balancer_http_requests_total{listener="public",code="200"} %d`, 32*250))
	assert.Contains(t, recorder.Body.String(), fmt.Sprintf(`load_balancer_upstream_attempts_total{backend="api",outcome="success",retry="false"} %d`, 32*250))
}

func TestSlowMetricsClientDoesNotBlockRequestObservation(t *testing.T) {
	metrics := observability.NewMetrics()
	metrics.ObserveHTTPRequest("public", 200, time.Millisecond)
	writer := &blockingWriter{started: make(chan struct{}), release: make(chan struct{})}
	scrapeDone := make(chan struct{})
	go func() {
		metrics.ServeHTTP(writer, httptest.NewRequest("GET", "/metrics", nil))
		close(scrapeDone)
	}()
	<-writer.started

	observationDone := make(chan struct{})
	go func() {
		metrics.ObserveHTTPRequest("public", 200, time.Millisecond)
		close(observationDone)
	}()
	select {
	case <-observationDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("request observation was blocked by scrape I/O")
	}
	close(writer.release)
	<-scrapeDone
}

type blockingWriter struct {
	header  sync.Once
	started chan struct{}
	release chan struct{}
}

func (writer *blockingWriter) Header() http.Header { return make(http.Header) }
func (writer *blockingWriter) WriteHeader(int)     {}
func (writer *blockingWriter) Write(value []byte) (int, error) {
	writer.header.Do(func() {
		close(writer.started)
		<-writer.release
	})
	return len(value), nil
}
