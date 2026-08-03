package balancer_test

import (
	"net/url"
	"testing"

	"github.com/J0es1ick/test-assignment/internal/balancer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoundRobinStrategy(t *testing.T) {
	backends := []string{
		"http://backend1:8080",
		"http://backend2:8080",
		"http://backend3:8080",
	}

	pool, err := balancer.NewBackendPool(backends)
	require.NoError(t, err)
	for _, backend := range pool.Backends {
		backend.SetAlive(true)
	}
	strategy := balancer.NewRoundRobinStrategy()

	t.Run("should rotate backends in order", func(t *testing.T) {
		first := strategy.GetNextPeer(pool)
		second := strategy.GetNextPeer(pool)
		third := strategy.GetNextPeer(pool)
		fourth := strategy.GetNextPeer(pool)

		assert.Equal(t, backends[0], first.URL.String())
		assert.Equal(t, backends[1], second.URL.String())
		assert.Equal(t, backends[2], third.URL.String())
		assert.Equal(t, backends[0], fourth.URL.String())
	})

	t.Run("should skip dead backends", func(t *testing.T) {
		u, _ := url.Parse(backends[1])
		pool.MarkBackendStatus(u, false)

		peers := []string{
			strategy.GetNextPeer(pool).URL.String(),
			strategy.GetNextPeer(pool).URL.String(),
			strategy.GetNextPeer(pool).URL.String(),
		}

		assert.Equal(t, []string{backends[0], backends[2], backends[0]}, peers)
	})

	t.Run("should return nil when no backends alive", func(t *testing.T) {
		for _, b := range pool.Backends {
			pool.MarkBackendStatus(b.URL, false)
		}

		assert.Nil(t, strategy.GetNextPeer(pool))
	})
}
