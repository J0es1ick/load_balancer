package server_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/stretchr/testify/require"
)

func TestFrontendDemoConfigMatchesStrictGoSchema(t *testing.T) {
	data, err := os.ReadFile("../../frontend/src/config/demo.gateway.json")
	require.NoError(t, err)
	var cfg config.GatewayConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&cfg))
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())
}
