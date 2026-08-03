package balancer_test

import (
	"net/url"
	"testing"

	"github.com/J0es1ick/test-assignment/internal/balancer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackendPool(t *testing.T) {
	t.Run("should mark backend status correctly", func(t *testing.T) {
		backendURL := "http://localhost:8081"
		pool, err := balancer.NewBackendPool([]string{backendURL})
		require.NoError(t, err)
		u, _ := url.Parse(backendURL)

		assert.False(t, pool.Backends[0].IsAlive())

		pool.MarkBackendStatus(u, false)
		assert.False(t, pool.Backends[0].IsAlive())

		pool.MarkBackendStatus(u, true)
		assert.True(t, pool.Backends[0].IsAlive())

		assert.True(t, pool.SetBackendEnabled("localhost", false))
		assert.False(t, pool.Backends[0].IsAlive())
		assert.True(t, pool.Backends[0].IsHealthy())
		assert.False(t, pool.Backends[0].IsEnabled())

		assert.True(t, pool.SetBackendEnabled("localhost", true))
		assert.True(t, pool.Backends[0].IsAlive())
	})

	t.Run("should handle unknown backend", func(t *testing.T) {
		pool, err := balancer.NewBackendPool([]string{"http://localhost:8081"})
		require.NoError(t, err)
		unknownURL, _ := url.Parse("http://unknown:8080")
		pool.MarkBackendStatus(unknownURL, false)
	})

	t.Run("should reject invalid and duplicate backends", func(t *testing.T) {
		_, err := balancer.NewBackendPool([]string{"backend-without-scheme"})
		assert.Error(t, err)

		_, err = balancer.NewBackendPool([]string{
			"http://backend:8080",
			"http://backend:8081",
		})
		assert.Error(t, err)
	})
}
