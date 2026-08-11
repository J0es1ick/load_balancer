package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

type overloadMetrics interface {
	ObserveProtectionEvent(kind, backendID string)
	SetInflightRequests(value int64)
}

type OverloadSnapshot struct {
	MaxConcurrentRequests int    `json:"max_concurrent_requests"`
	Inflight              int64  `json:"inflight"`
	QueueTimeout          string `json:"queue_timeout"`
}

type overloadController struct {
	semaphore    chan struct{}
	queueTimeout time.Duration
	inflight     atomic.Int64
	metrics      overloadMetrics
}

func newOverloadController(maxConcurrent int, queueTimeout time.Duration, metrics overloadMetrics) *overloadController {
	return &overloadController{semaphore: make(chan struct{}, maxConcurrent), queueTimeout: queueTimeout, metrics: metrics}
}

func (controller *overloadController) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !controller.acquire(request.Context()) {
			writer.Header().Set("Retry-After", "1")
			writer.Header().Set("X-Balancer-Overloaded", "true")
			if controller.metrics != nil {
				controller.metrics.ObserveProtectionEvent("global_concurrency_limit", "")
			}
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "load balancer is overloaded"})
			return
		}
		defer controller.release()
		next.ServeHTTP(writer, request)
	})
}

func (controller *overloadController) acquire(ctx context.Context) bool {
	if controller.queueTimeout == 0 {
		select {
		case controller.semaphore <- struct{}{}:
			controller.recordInflight(1)
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(controller.queueTimeout)
	defer timer.Stop()
	select {
	case controller.semaphore <- struct{}{}:
		controller.recordInflight(1)
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (controller *overloadController) release() {
	<-controller.semaphore
	controller.recordInflight(-1)
}

func (controller *overloadController) recordInflight(delta int64) {
	current := controller.inflight.Add(delta)
	if controller.metrics != nil {
		controller.metrics.SetInflightRequests(current)
	}
}

func (controller *overloadController) Snapshot() OverloadSnapshot {
	return OverloadSnapshot{MaxConcurrentRequests: cap(controller.semaphore), Inflight: controller.inflight.Load(), QueueTimeout: controller.queueTimeout.String()}
}
