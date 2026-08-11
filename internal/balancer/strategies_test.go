package balancer_test

import (
	"fmt"
	"testing"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoundRobinStrategy(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "one", URL: "http://one"}, {ID: "two", URL: "http://two"}, {ID: "three", URL: "http://three"}})
	require.NoError(t, err)
	for _, backend := range pool.GetBackends() {
		backend.SetAlive(true)
	}
	strategy := balancer.NewRoundRobinStrategy()
	assert.Equal(t, "one", strategy.GetNextPeer(pool).ID())
	assert.Equal(t, "two", strategy.GetNextPeer(pool).ID())
	assert.Equal(t, "three", strategy.GetNextPeer(pool).ID())
	assert.Equal(t, "one", strategy.GetNextPeer(pool).ID())
	assert.NotEqual(t, "two", strategy.GetNextPeerExcluding(pool, map[string]struct{}{"two": {}}).ID())
}

func BenchmarkRoundRobinStrategy(b *testing.B) {
	specs := make([]balancer.BackendSpec, 100)
	for index := range specs {
		specs[index] = balancer.BackendSpec{ID: fmt.Sprintf("backend-%d", index), URL: fmt.Sprintf("http://backend-%d", index)}
	}
	pool, err := balancer.NewBackendPool(specs)
	if err != nil {
		b.Fatal(err)
	}
	for _, backend := range pool.GetBackends() {
		backend.SetAlive(true)
	}
	strategy := balancer.NewRoundRobinStrategy()
	b.ReportAllocs()
	b.RunParallel(func(parallel *testing.PB) {
		for parallel.Next() {
			_ = strategy.GetNextPeer(pool)
		}
	})
}
