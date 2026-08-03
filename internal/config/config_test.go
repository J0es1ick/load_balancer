package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/J0es1ick/test-assignment/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validConfig = `
server:
  port: "8080"
  trusted_proxies:
    - "172.16.0.0/12"
database:
  host: "postgres"
  port: "5432"
  user: "postgres"
  name: "balancer"
  sslmode: "disable"
backends:
  - "http://backend1:80"
  - "http://backend2:80"
rate_limit:
  default_capacity: 100
  default_rate: "1s"
`

func TestLoadAppliesDefaultsAndValidates(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validConfig))
	require.NoError(t, err)

	assert.Equal(t, 5*time.Second, cfg.Server.ReadHeaderTimeout)
	assert.Equal(t, 5*time.Second, cfg.HealthCheck.Interval)
	assert.Equal(t, 2*time.Second, cfg.HealthCheck.Timeout)
	assert.Equal(t, 1<<20, cfg.Server.MaxHeaderBytes)
}

func TestLoadRejectsInvalidBackendAndUnknownFields(t *testing.T) {
	invalidBackend := `
server:
  port: "8080"
database:
  host: "postgres"
  port: "5432"
  user: "postgres"
  name: "balancer"
backends:
  - "backend-without-scheme"
`
	_, err := config.Load(writeConfig(t, invalidBackend))
	assert.Error(t, err)

	_, err = config.Load(writeConfig(t, validConfig+"\nunknown: true\n"))
	assert.Error(t, err)
}

func TestValidateReloadSeparatesDynamicAndImmutableSettings(t *testing.T) {
	current, err := config.Load(writeConfig(t, validConfig))
	require.NoError(t, err)
	next, err := config.Load(writeConfig(t, validConfig))
	require.NoError(t, err)

	next.Backends = []string{"http://backend3:80"}
	next.RateLimit.DefaultCapacity = 50
	next.HealthCheck.Interval = 10 * time.Second
	next.Server.TrustedProxies = []string{"10.0.0.0/8"}
	assert.NoError(t, config.ValidateReload(current, next))

	next.Server.Port = "9090"
	assert.Error(t, config.ValidateReload(current, next))
	next.Server.Port = current.Server.Port
	next.Database.Host = "other-postgres"
	assert.Error(t, config.ValidateReload(current, next))
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
