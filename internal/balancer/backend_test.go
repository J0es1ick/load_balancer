package balancer_test

import (
	"net/url"
	"testing"

	"github.com/J0es1ick/test-assignment/internal/balancer"
	"github.com/stretchr/testify/assert"
)

func TestBackendPool(t *testing.T) {
	t.Run("should mark backend status correctly", func(t *testing.T) {
		backendURL := "http://localhost:8081"
		pool := balancer.NewBackendPool([]string{backendURL})
		u, _ := url.Parse(backendURL)

		assert.True(t, pool.Backends[0].IsAlive())

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
		pool := balancer.NewBackendPool([]string{"http://localhost:8081"})
		unknownURL, _ := url.Parse("http://unknown:8080")
		pool.MarkBackendStatus(unknownURL, false)
	})
}
