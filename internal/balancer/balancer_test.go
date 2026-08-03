package balancer_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/J0es1ick/test-assignment/internal/balancer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadBalancer(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	defer backend.Close()

	pool, err := balancer.NewBackendPool([]string{backend.URL})
	require.NoError(t, err)
	pool.Backends[0].SetAlive(true)
	lb := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy())

	t.Run("should proxy requests to backends", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()

		lb.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "OK", rec.Body.String())
		assert.NotEmpty(t, rec.Header().Get("X-Balancer-Backend"))
	})

	t.Run("should return 503 when no backends available", func(t *testing.T) {
		emptyPool, err := balancer.NewBackendPool([]string{})
		require.NoError(t, err)
		lb := balancer.NewLoadBalancer(emptyPool, balancer.NewRoundRobinStrategy())

		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()

		lb.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}
