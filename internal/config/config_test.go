package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validConfig = `
server:
  port: "8080"
  trusted_proxies: ["172.16.0.0/12"]
management:
  enabled: true
  address: ":9090"
  auth_token_env: "BALANCER_ADMIN_TOKEN"
database:
  host: "postgres"
  port: "5432"
  user: "postgres"
  password_env: "POSTGRES_PASSWORD"
  name: "balancer"
  sslmode: "disable"
backends:
  - id: "backend-1"
    url: "http://backend:8081"
  - id: "backend-2"
    url: "http://backend:8082"
    disabled: true
rate_limit:
  enabled: true
  storage: "local"
  capacity: 100
  refill_per_second: 10
health_check:
  mode: "http"
  path: "/health"
`

func TestLoadAppliesDefaultsAndAcceptsSameHostOnDifferentPorts(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validConfig))
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, cfg.Server.ReadHeaderTimeout)
	assert.Equal(t, 5*time.Second, cfg.HealthCheck.Interval)
	assert.Equal(t, 2*time.Second, cfg.HealthCheck.Timeout)
	assert.Equal(t, 1<<20, cfg.Server.MaxHeaderBytes)
	assert.Equal(t, 2, cfg.Server.Retry.MaxAttempts)
	assert.Equal(t, 64, cfg.RateLimit.LocalShards)
	assert.True(t, cfg.Backends[1].Disabled)
}

func TestLoadRejectsDuplicateIDsUnknownFieldsAndUnsafeManagement(t *testing.T) {
	duplicate := validConfig + `
`
	duplicate = replace(duplicate, `id: "backend-2"`, `id: "backend-1"`)
	_, err := config.Load(writeConfig(t, duplicate))
	assert.ErrorContains(t, err, "duplicate backend ID")

	_, err = config.Load(writeConfig(t, validConfig+"\nunknown: true\n"))
	assert.Error(t, err)

	unsafe := replace(validConfig, `auth_token_env: "BALANCER_ADMIN_TOKEN"`, `auth_token_env: ""`)
	_, err = config.Load(writeConfig(t, unsafe))
	assert.ErrorContains(t, err, "auth_token_env")
}

func TestValidateReloadSeparatesDynamicAndImmutableSettings(t *testing.T) {
	current, err := config.Load(writeConfig(t, validConfig))
	require.NoError(t, err)
	next, err := config.Load(writeConfig(t, validConfig))
	require.NoError(t, err)
	next.Backends = []config.BackendConfig{{ID: "backend-3", URL: "http://backend3:80"}}
	next.RateLimit.Capacity = 50
	next.HealthCheck.Interval = 10 * time.Second
	next.Server.TrustedProxies = []string{"10.0.0.0/8"}
	assert.NoError(t, config.ValidateReload(current, next))
	next.Server.Port = "9091"
	assert.Error(t, config.ValidateReload(current, next))
	next.Server.Port = current.Server.Port
	next.RateLimit.Storage = "redis"
	assert.Error(t, config.ValidateReload(current, next))
	next.RateLimit.Storage = current.RateLimit.Storage
	next.RateLimit.Retention = 48 * time.Hour
	assert.Error(t, config.ValidateReload(current, next))
}

func TestSecretFromEnvSupportsMountedSecretFile(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "admin-token")
	require.NoError(t, os.WriteFile(secretPath, []byte("file-secret\n"), 0o600))
	t.Setenv("TEST_ADMIN_TOKEN", "")
	t.Setenv("TEST_ADMIN_TOKEN_FILE", secretPath)
	value, err := config.SecretFromEnv("TEST_ADMIN_TOKEN")
	require.NoError(t, err)
	assert.Equal(t, "file-secret", value)
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func replace(value, old, replacement string) string {
	for index := 0; index+len(old) <= len(value); index++ {
		if value[index:index+len(old)] == old {
			return value[:index] + replacement + value[index+len(old):]
		}
	}
	return value
}
