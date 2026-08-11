package balancer

import (
	"sync"
	"time"
)

type RetryBudgetSnapshot struct {
	Capacity        int     `json:"capacity"`
	Tokens          float64 `json:"tokens"`
	RefillPerSecond float64 `json:"refill_per_second"`
}

type RetryBudget struct {
	mu              sync.Mutex
	capacity        int
	refillPerSecond float64
	tokens          float64
	updatedAt       time.Time
}

func newRetryBudget(capacity int, refillPerSecond float64) *RetryBudget {
	budget := &RetryBudget{}
	budget.Update(capacity, refillPerSecond)
	return budget
}

func (budget *RetryBudget) Update(capacity int, refillPerSecond float64) {
	if capacity < 1 {
		capacity = 1
	}
	if refillPerSecond <= 0 {
		refillPerSecond = 1
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	uninitialized := budget.updatedAt.IsZero()
	if !uninitialized {
		budget.refillLocked(time.Now())
	}
	budget.capacity = capacity
	budget.refillPerSecond = refillPerSecond
	if uninitialized || budget.tokens > float64(capacity) {
		budget.tokens = float64(capacity)
	}
	budget.updatedAt = time.Now()
}

func (budget *RetryBudget) Allow() bool {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	budget.refillLocked(time.Now())
	if budget.tokens < 1 {
		return false
	}
	budget.tokens--
	return true
}

func (budget *RetryBudget) Snapshot() RetryBudgetSnapshot {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	budget.refillLocked(time.Now())
	return RetryBudgetSnapshot{Capacity: budget.capacity, Tokens: budget.tokens, RefillPerSecond: budget.refillPerSecond}
}

func (budget *RetryBudget) refillLocked(now time.Time) {
	if budget.updatedAt.IsZero() {
		budget.updatedAt = now
		return
	}
	budget.tokens += now.Sub(budget.updatedAt).Seconds() * budget.refillPerSecond
	if budget.tokens > float64(budget.capacity) {
		budget.tokens = float64(budget.capacity)
	}
	budget.updatedAt = now
}
