package balancer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/J0es1ick/test-assignment/internal/balancer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthChecker(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	pool, err := balancer.NewBackendPool([]string{backend.URL})
	require.NoError(t, err)
	checker := balancer.NewHealthChecker(pool, 100*time.Millisecond, 50*time.Millisecond)

	t.Run("should detect live backends", func(t *testing.T) {
		checker.Check(context.Background())
		assert.True(t, pool.Backends[0].IsAlive())
	})

	t.Run("should detect dead backends", func(t *testing.T) {
		backend.Close()

		checker.Check(context.Background())
		assert.False(t, pool.Backends[0].IsAlive())
	})

	t.Run("should update settings", func(t *testing.T) {
		require.NoError(t, checker.Update(200*time.Millisecond, 75*time.Millisecond))
		interval, timeout := checker.Settings()
		assert.Equal(t, 200*time.Millisecond, interval)
		assert.Equal(t, 75*time.Millisecond, timeout)
	})
}
