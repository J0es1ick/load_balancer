package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

const (
	defaultServerPort                         = "8080"
	defaultManagementAddress                  = ":9090"
	defaultReadHeaderTimeout                  = 5 * time.Second
	defaultReadTimeout                        = 15 * time.Second
	defaultManagementWriteTimeout             = 30 * time.Second
	defaultIdleTimeout                        = 60 * time.Second
	defaultShutdownTimeout                    = 10 * time.Second
	defaultMaxHeaderBytes                     = 1 << 20
	defaultDialTimeout                        = 2 * time.Second
	defaultTLSHandshakeTimeout                = 3 * time.Second
	defaultResponseHeaderTimeout              = 10 * time.Second
	defaultExpectContinueTimeout              = time.Second
	defaultUpstreamIdleTimeout                = 90 * time.Second
	defaultMaxIdleConnections                 = 256
	defaultMaxIdleConnectionsHost             = 64
	defaultMaxConcurrentBackendRequests       = 512
	defaultMaxConcurrentRequests              = 2048
	defaultOverloadQueueTimeout               = 50 * time.Millisecond
	defaultHealthInterval                     = 5 * time.Second
	defaultHealthTimeout                      = 2 * time.Second
	defaultHealthPath                         = "/health"
	defaultHealthConcurrency                  = 16
	defaultHealthFailureThreshold             = 2
	defaultHealthSuccessThreshold             = 1
	defaultHealthCooldown                     = 10 * time.Second
	defaultHealthJitter                       = 500 * time.Millisecond
	defaultRateCapacity                       = 100
	defaultRefillRate                         = 100.0
	defaultRateOperationTimeout               = 50 * time.Millisecond
	defaultRateCleanupInterval                = 6 * time.Hour
	defaultRateRetention                      = 24 * time.Hour
	defaultLocalShards                        = 64
	defaultLocalMaxBuckets                    = 100_000
	defaultIPv4PrefixBits                     = 32
	defaultIPv6PrefixBits                     = 64
	defaultRedisAddress                       = "redis:6379"
	defaultRedisPoolSize                      = 64
	defaultDatabaseConnectTimeout             = 5 * time.Second
	defaultDatabaseMaxConnections             = 25
	defaultRetryAttempts                      = 2
	defaultRetryPerTryTimeout                 = 5 * time.Second
	defaultRetryBodyLimit               int64 = 1 << 20
	defaultRetryBudgetCapacity                = 100
	defaultRetryBudgetRefill                  = 10.0
	defaultSlowStartDuration                  = 30 * time.Second
	defaultSlowStartMinPercent                = 10
)

func InitConfig() (*Config, error) {
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		return nil, fmt.Errorf("CONFIG_PATH is not set")
	}
	return Load(path)
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (cfg *Config) applyDefaults() {
	if cfg.Server.Port == "" {
		cfg.Server.Port = defaultServerPort
	}
	if cfg.Server.ReadHeaderTimeout == 0 {
		cfg.Server.ReadHeaderTimeout = defaultReadHeaderTimeout
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = defaultReadTimeout
	}
	if cfg.Server.IdleTimeout == 0 {
		cfg.Server.IdleTimeout = defaultIdleTimeout
	}
	if cfg.Server.ShutdownTimeout == 0 {
		cfg.Server.ShutdownTimeout = defaultShutdownTimeout
	}
	if cfg.Server.MaxHeaderBytes == 0 {
		cfg.Server.MaxHeaderBytes = defaultMaxHeaderBytes
	}
	if cfg.Server.Upstream.DialTimeout == 0 {
		cfg.Server.Upstream.DialTimeout = defaultDialTimeout
	}
	if cfg.Server.Upstream.TLSHandshakeTimeout == 0 {
		cfg.Server.Upstream.TLSHandshakeTimeout = defaultTLSHandshakeTimeout
	}
	if cfg.Server.Upstream.ResponseHeaderTimeout == 0 {
		cfg.Server.Upstream.ResponseHeaderTimeout = defaultResponseHeaderTimeout
	}
	if cfg.Server.Upstream.ExpectContinueTimeout == 0 {
		cfg.Server.Upstream.ExpectContinueTimeout = defaultExpectContinueTimeout
	}
	if cfg.Server.Upstream.IdleConnTimeout == 0 {
		cfg.Server.Upstream.IdleConnTimeout = defaultUpstreamIdleTimeout
	}
	if cfg.Server.Upstream.MaxIdleConns == 0 {
		cfg.Server.Upstream.MaxIdleConns = defaultMaxIdleConnections
	}
	if cfg.Server.Upstream.MaxIdleConnsPerHost == 0 {
		cfg.Server.Upstream.MaxIdleConnsPerHost = defaultMaxIdleConnectionsHost
	}
	if cfg.Server.Upstream.MaxConcurrentRequests == 0 {
		cfg.Server.Upstream.MaxConcurrentRequests = defaultMaxConcurrentBackendRequests
	}
	if cfg.Server.Retry.MaxAttempts == 0 {
		cfg.Server.Retry.MaxAttempts = defaultRetryAttempts
	}
	if cfg.Server.Retry.PerTryTimeout == 0 {
		cfg.Server.Retry.PerTryTimeout = defaultRetryPerTryTimeout
	}
	if cfg.Server.Retry.BodyLimit == 0 {
		cfg.Server.Retry.BodyLimit = defaultRetryBodyLimit
	}
	if len(cfg.Server.Retry.Methods) == 0 {
		cfg.Server.Retry.Methods = []string{"GET", "HEAD", "OPTIONS"}
	}
	if len(cfg.Server.Retry.Statuses) == 0 {
		cfg.Server.Retry.Statuses = []int{502, 503, 504}
	}
	if cfg.Server.Retry.BudgetCapacity == 0 {
		cfg.Server.Retry.BudgetCapacity = defaultRetryBudgetCapacity
	}
	if cfg.Server.Retry.BudgetRefillPerSecond == 0 {
		cfg.Server.Retry.BudgetRefillPerSecond = defaultRetryBudgetRefill
	}
	if cfg.Server.Overload.MaxConcurrentRequests == 0 {
		cfg.Server.Overload.MaxConcurrentRequests = defaultMaxConcurrentRequests
	}
	if cfg.Server.Overload.QueueTimeout == 0 {
		cfg.Server.Overload.QueueTimeout = defaultOverloadQueueTimeout
	}
	if cfg.Management.Enabled && cfg.Management.Address == "" {
		cfg.Management.Address = defaultManagementAddress
	}
	if cfg.Management.Enabled && cfg.Management.WriteTimeout == 0 {
		cfg.Management.WriteTimeout = defaultManagementWriteTimeout
	}
	if cfg.Database.ConnectTimeout == 0 {
		cfg.Database.ConnectTimeout = defaultDatabaseConnectTimeout
	}
	if cfg.Database.MaxOpenConns == 0 {
		cfg.Database.MaxOpenConns = defaultDatabaseMaxConnections
	}
	if cfg.Redis.Address == "" {
		cfg.Redis.Address = defaultRedisAddress
	}
	if cfg.Redis.PoolSize == 0 {
		cfg.Redis.PoolSize = defaultRedisPoolSize
	}
	if cfg.Redis.DialTimeout == 0 {
		cfg.Redis.DialTimeout = defaultDialTimeout
	}
	if cfg.Redis.ReadTimeout == 0 {
		cfg.Redis.ReadTimeout = defaultRateOperationTimeout
	}
	if cfg.Redis.WriteTimeout == 0 {
		cfg.Redis.WriteTimeout = defaultRateOperationTimeout
	}
	if cfg.RateLimit.Storage == "" {
		cfg.RateLimit.Storage = "local"
	}
	if cfg.RateLimit.FailureMode == "" {
		cfg.RateLimit.FailureMode = "fail-open"
	}
	if cfg.RateLimit.Capacity == 0 {
		cfg.RateLimit.Capacity = defaultRateCapacity
	}
	if cfg.RateLimit.RefillPerSecond == 0 {
		cfg.RateLimit.RefillPerSecond = defaultRefillRate
	}
	if cfg.RateLimit.OperationTimeout == 0 {
		cfg.RateLimit.OperationTimeout = defaultRateOperationTimeout
	}
	if cfg.RateLimit.LocalShards == 0 {
		cfg.RateLimit.LocalShards = defaultLocalShards
	}
	if cfg.RateLimit.LocalMaxBuckets == 0 {
		cfg.RateLimit.LocalMaxBuckets = defaultLocalMaxBuckets
	}
	if cfg.RateLimit.IPv4PrefixBits == 0 {
		cfg.RateLimit.IPv4PrefixBits = defaultIPv4PrefixBits
	}
	if cfg.RateLimit.IPv6PrefixBits == 0 {
		cfg.RateLimit.IPv6PrefixBits = defaultIPv6PrefixBits
	}
	if cfg.RateLimit.CleanupInterval == 0 {
		cfg.RateLimit.CleanupInterval = defaultRateCleanupInterval
	}
	if cfg.RateLimit.Retention == 0 {
		cfg.RateLimit.Retention = defaultRateRetention
	}
	if cfg.HealthCheck.Mode == "" {
		cfg.HealthCheck.Mode = "http"
	}
	if cfg.HealthCheck.Path == "" {
		cfg.HealthCheck.Path = defaultHealthPath
	}
	if cfg.HealthCheck.Interval == 0 {
		cfg.HealthCheck.Interval = defaultHealthInterval
	}
	if cfg.HealthCheck.Timeout == 0 {
		cfg.HealthCheck.Timeout = defaultHealthTimeout
	}
	if cfg.HealthCheck.FailureThreshold == 0 {
		cfg.HealthCheck.FailureThreshold = defaultHealthFailureThreshold
	}
	if cfg.HealthCheck.SuccessThreshold == 0 {
		cfg.HealthCheck.SuccessThreshold = defaultHealthSuccessThreshold
	}
	if cfg.HealthCheck.MaxConcurrency == 0 {
		cfg.HealthCheck.MaxConcurrency = defaultHealthConcurrency
	}
	if cfg.HealthCheck.Jitter == 0 {
		cfg.HealthCheck.Jitter = defaultHealthJitter
	}
	if cfg.HealthCheck.Cooldown == 0 {
		cfg.HealthCheck.Cooldown = defaultHealthCooldown
	}
	if len(cfg.HealthCheck.ExpectedStatuses) == 0 {
		cfg.HealthCheck.ExpectedStatuses = []int{200, 204}
	}
	if cfg.HealthCheck.SlowStart == 0 {
		cfg.HealthCheck.SlowStart = defaultSlowStartDuration
	}
	if cfg.HealthCheck.SlowStartMinimum == 0 {
		cfg.HealthCheck.SlowStartMinimum = defaultSlowStartMinPercent
	}
}

func SecretFromEnv(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if value, exists := os.LookupEnv(name); exists && value != "" {
		return value, nil
	}
	fileName := os.Getenv(name + "_FILE")
	if fileName == "" {
		return "", fmt.Errorf("required environment variable %s or %s_FILE is not set", name, name)
	}
	data, err := os.ReadFile(fileName)
	if err != nil {
		return "", fmt.Errorf("read secret %s_FILE: %w", name, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("secret file from %s_FILE is empty", name)
	}
	return value, nil
}
