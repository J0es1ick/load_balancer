package balancer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var errAttemptTimeout = errors.New("upstream attempt response-header timeout")

type retryTransport struct {
	pool     *BackendPool
	strategy Strategy
	base     http.RoundTripper
	observer ProxyObserver
	policy   atomic.Pointer[RetryPolicy]
	budget   *RetryBudget
}

func (transport *retryTransport) UpdatePolicy(policy RetryPolicy) {
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}
	policy.Methods = append([]string(nil), policy.Methods...)
	policy.Statuses = append([]int(nil), policy.Statuses...)
	if policy.BudgetCapacity < 1 {
		policy.BudgetCapacity = 1
	}
	if policy.BudgetRefillPerSecond <= 0 {
		policy.BudgetRefillPerSecond = 1
	}
	if transport.budget == nil {
		transport.budget = newRetryBudget(policy.BudgetCapacity, policy.BudgetRefillPerSecond)
	} else {
		transport.budget.Update(policy.BudgetCapacity, policy.BudgetRefillPerSecond)
	}
	transport.policy.Store(&policy)
}

func (transport *retryTransport) Policy() RetryPolicy {
	policy := transport.policy.Load()
	if policy == nil {
		return RetryPolicy{MaxAttempts: 1}
	}
	return *policy
}

func (transport *retryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	policy := transport.Policy()
	maxAttempts := 1
	if policy.allowsMethod(request.Method) && (request.Body == nil || request.Body == http.NoBody || request.GetBody != nil) {
		maxAttempts = policy.MaxAttempts
	}
	excluded := make(map[string]struct{}, len(transport.pool.GetBackends()))
	backend := transport.nextAvailableBackend(excluded)
	if backend == nil {
		return nil, ErrNoBackend
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		excluded[backend.ID()] = struct{}{}
		outgoing, err := cloneForAttempt(request, backend, attempt)
		if err != nil {
			backend.Release()
			return nil, err
		}
		attemptContext, cancelAttempt := context.WithCancelCause(outgoing.Context())
		outgoing = outgoing.WithContext(attemptContext)
		timeoutCompleted := make(chan struct{})
		headerTimer := time.AfterFunc(policy.PerTryTimeout, func() {
			cancelAttempt(errAttemptTimeout)
			close(timeoutCompleted)
		})
		started := time.Now()
		response, roundTripError := transport.base.RoundTrip(outgoing)
		if !headerTimer.Stop() {
			<-timeoutCompleted
		}
		duration := time.Since(started)
		attemptTimedOut := errors.Is(context.Cause(attemptContext), errAttemptTimeout)
		if attemptTimedOut {
			roundTripError = errAttemptTimeout
		}
		backend.RecordRequest()
		if response != nil {
			response.Body = &attemptBody{ReadCloser: response.Body, cancel: func() { cancelAttempt(context.Canceled) }, release: backend.Release}
		} else {
			cancelAttempt(context.Canceled)
			backend.Release()
		}

		status := 0
		if response != nil {
			status = response.StatusCode
		}
		retryStatus := response != nil && policy.retriesStatus(response.StatusCode)
		failed := roundTripError != nil || retryStatus
		clientAborted := request.Context().Err() != nil && !attemptTimedOut
		if failed && !clientAborted {
			backend.RecordPassiveFailure(transport.pool.PassivePolicy())
		} else if !failed {
			backend.RecordPassiveSuccess()
		}
		if transport.observer != nil {
			transport.observer.ObserveBackendAttempt(backend.ID(), status, duration, roundTripError, attempt > 1)
		}

		if !failed || attempt == maxAttempts || clientAborted {
			if response != nil {
				setAttemptHeaders(response, backend.ID(), attempt, false)
				return response, roundTripError
			}
			return nil, roundTripError
		}

		nextBackend := transport.nextAvailableBackend(excluded)
		if nextBackend == nil {
			if response != nil {
				setAttemptHeaders(response, backend.ID(), attempt, false)
				return response, roundTripError
			}
			return nil, roundTripError
		}
		if !transport.budget.Allow() {
			nextBackend.Release()
			transport.observeProtection("retry_budget_exhausted", backend.ID())
			if response != nil {
				setAttemptHeaders(response, backend.ID(), attempt, true)
				return response, roundTripError
			}
			return nil, roundTripError
		}
		// Closing instead of draining bounds retry latency even when an upstream
		// sends retryable headers and then stalls its response body.
		if response != nil {
			_ = response.Body.Close()
		}
		backend = nextBackend
	}
	return nil, ErrNoBackend
}

func setAttemptHeaders(response *http.Response, backendID string, attempt int, budgetRejected bool) {
	response.Header.Set("X-Balancer-Backend", backendID)
	response.Header.Set("X-Balancer-Attempts", strconv.Itoa(attempt))
	if budgetRejected {
		response.Header.Set("X-Balancer-Retry-Budget", "exhausted")
	}
}

func (transport *retryTransport) nextAvailableBackend(excluded map[string]struct{}) *Backend {
	for {
		backend := transport.strategy.GetNextPeerExcluding(transport.pool, excluded)
		if backend == nil {
			return nil
		}
		if backend.TryAcquire() {
			return backend
		}
		excluded[backend.ID()] = struct{}{}
		transport.observeProtection("backend_concurrency_limit", backend.ID())
	}
}

func (transport *retryTransport) observeProtection(kind, backendID string) {
	if observer, ok := transport.observer.(ProtectionObserver); ok {
		observer.ObserveProtectionEvent(kind, backendID)
	}
}

func cloneForAttempt(request *http.Request, backend *Backend, attempt int) (*http.Request, error) {
	outgoing := request.Clone(request.Context())
	if attempt > 1 && request.GetBody != nil {
		body, err := request.GetBody()
		if err != nil {
			return nil, err
		}
		outgoing.Body = body
	}
	outgoing.URL.Scheme = backend.URL.Scheme
	outgoing.URL.Host = backend.URL.Host
	outgoing.URL.Path, outgoing.URL.RawPath = joinURLPath(backend.URL, request.URL)
	if backend.URL.RawQuery == "" || request.URL.RawQuery == "" {
		outgoing.URL.RawQuery = backend.URL.RawQuery + request.URL.RawQuery
	} else {
		outgoing.URL.RawQuery = backend.URL.RawQuery + "&" + request.URL.RawQuery
	}
	outgoing.RequestURI = ""
	outgoing.Header.Set("X-Balancer-Backend-Attempt", backend.ID())
	return outgoing, nil
}

func (policy RetryPolicy) allowsMethod(method string) bool {
	for _, allowed := range policy.Methods {
		if strings.EqualFold(allowed, method) {
			return true
		}
	}
	return false
}

func (policy RetryPolicy) retriesStatus(status int) bool {
	for _, retryStatus := range policy.Statuses {
		if retryStatus == status {
			return true
		}
	}
	return false
}

type attemptBody struct {
	io.ReadCloser
	cancel  func()
	release func()
	once    atomic.Bool
}

func (body *attemptBody) Write(value []byte) (int, error) {
	writer, ok := body.ReadCloser.(io.Writer)
	if !ok {
		return 0, fmt.Errorf("upstream response body does not support protocol upgrade")
	}
	return writer.Write(value)
}

func (body *attemptBody) Close() error {
	err := body.ReadCloser.Close()
	if body.once.CompareAndSwap(false, true) {
		body.cancel()
		body.release()
	}
	return err
}
