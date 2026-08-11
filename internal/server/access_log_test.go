package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAccessLogPolicy(t *testing.T) {
	assert.True(t, shouldLogAccess("public", http.MethodGet, http.StatusBadGateway, 0), "errors must always be logged")
	assert.True(t, shouldLogAccess("management", http.MethodPost, http.StatusNoContent, 0), "management mutations must always be logged")
	assert.False(t, shouldLogAccess("public", http.MethodGet, http.StatusOK, 0), "sampling can disable successful request logs")
	assert.True(t, shouldLogAccess("public", http.MethodGet, http.StatusOK, 1), "sample rate one must log every request")
}
