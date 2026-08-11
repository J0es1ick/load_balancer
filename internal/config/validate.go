package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

func (cfg *Config) Validate() error {
	if port, err := strconv.Atoi(cfg.Server.Port); err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("server.port must be a number between 1 and 65535")
	}
	if cfg.Server.ReadHeaderTimeout <= 0 || cfg.Server.ReadTimeout <= 0 || cfg.Server.WriteTimeout < 0 || cfg.Server.IdleTimeout <= 0 || cfg.Server.ShutdownTimeout <= 0 {
		return fmt.Errorf("server timeouts are invalid")
	}
	if cfg.Server.AccessLogSampleRate < 0 || cfg.Server.AccessLogSampleRate > 1 {
		return fmt.Errorf("server.access_log_sample_rate must be between 0 and 1")
	}
	if cfg.Server.MaxHeaderBytes < 1024 {
		return fmt.Errorf("server.max_header_bytes must be at least 1024")
	}
	if cfg.Server.Upstream.DialTimeout <= 0 || cfg.Server.Upstream.TLSHandshakeTimeout <= 0 || cfg.Server.Upstream.ResponseHeaderTimeout <= 0 || cfg.Server.Upstream.IdleConnTimeout <= 0 {
		return fmt.Errorf("server.upstream timeouts must be positive")
	}
	if cfg.Server.Upstream.MaxIdleConns < 1 || cfg.Server.Upstream.MaxIdleConnsPerHost < 1 || cfg.Server.Upstream.MaxConnsPerHost < 0 || cfg.Server.Upstream.MaxConcurrentRequests < 1 {
		return fmt.Errorf("server.upstream connection limits are invalid")
	}
	if cfg.Server.Retry.MaxAttempts < 1 || cfg.Server.Retry.MaxAttempts > 5 || cfg.Server.Retry.PerTryTimeout <= 0 || cfg.Server.Retry.BodyLimit < 0 {
		return fmt.Errorf("server.retry values are invalid")
	}
	for index, method := range cfg.Server.Retry.Methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" {
			return fmt.Errorf("server.retry.methods cannot contain an empty method")
		}
		cfg.Server.Retry.Methods[index] = method
	}
	for _, status := range cfg.Server.Retry.Statuses {
		if status < 400 || status > 599 {
			return fmt.Errorf("server.retry.statuses must contain HTTP error statuses")
		}
	}
	if cfg.Server.Retry.BudgetCapacity < 1 || cfg.Server.Retry.BudgetRefillPerSecond <= 0 {
		return fmt.Errorf("server.retry budget values must be positive")
	}
	if cfg.Server.Overload.MaxConcurrentRequests < 1 || cfg.Server.Overload.QueueTimeout < 0 {
		return fmt.Errorf("server.overload values are invalid")
	}
	for _, proxy := range cfg.Server.TrustedProxies {
		if _, err := parsePrefix(proxy); err != nil {
			return fmt.Errorf("invalid trusted proxy %q: %w", proxy, err)
		}
	}
	if cfg.Management.Enabled {
		if cfg.Management.Address == "" {
			return fmt.Errorf("management.address is required when management API is enabled")
		}
		if cfg.Management.AuthTokenEnv == "" && !cfg.Management.AllowInsecure {
			return fmt.Errorf("management.auth_token_env is required unless allow_insecure is enabled")
		}
		if cfg.Management.WriteTimeout <= 0 {
			return fmt.Errorf("management.write_timeout must be positive")
		}
	}
	if err := cfg.validateBackends(); err != nil {
		return err
	}
	if !slices.Contains([]string{"local", "redis", "postgres"}, cfg.RateLimit.Storage) {
		return fmt.Errorf("rate_limit.storage must be local, redis or postgres")
	}
	if !slices.Contains([]string{"fail-open", "fail-closed", "local-fallback"}, cfg.RateLimit.FailureMode) {
		return fmt.Errorf("rate_limit.failure_mode must be fail-open, fail-closed or local-fallback")
	}
	if cfg.RateLimit.Capacity < 1 || cfg.RateLimit.RefillPerSecond <= 0 || cfg.RateLimit.OperationTimeout <= 0 || cfg.RateLimit.LocalShards < 1 || cfg.RateLimit.LocalMaxBuckets < 1 || cfg.RateLimit.IPv4PrefixBits < 1 || cfg.RateLimit.IPv4PrefixBits > 32 || cfg.RateLimit.IPv6PrefixBits < 1 || cfg.RateLimit.IPv6PrefixBits > 128 || cfg.RateLimit.CleanupInterval <= 0 || cfg.RateLimit.Retention <= 0 {
		return fmt.Errorf("rate_limit values must be positive")
	}
	if cfg.RateLimit.Storage == "redis" && cfg.Redis.Address == "" {
		return fmt.Errorf("redis.address is required for redis rate limit storage")
	}
	if cfg.RateLimit.Storage == "postgres" && (cfg.Database.Host == "" || cfg.Database.Port == "" || cfg.Database.User == "" || cfg.Database.Name == "" || cfg.Database.PasswordEnv == "") {
		return fmt.Errorf("database host, port, user, name and password_env are required for postgres storage")
	}
	if cfg.Redis.PoolSize < 1 || cfg.Redis.DialTimeout <= 0 || cfg.Redis.ReadTimeout <= 0 || cfg.Redis.WriteTimeout <= 0 {
		return fmt.Errorf("redis connection settings are invalid")
	}
	if cfg.Database.MaxOpenConns < 1 || cfg.Database.ConnectTimeout <= 0 {
		return fmt.Errorf("database connection settings are invalid")
	}
	if !slices.Contains([]string{"tcp", "http", "https"}, cfg.HealthCheck.Mode) {
		return fmt.Errorf("health_check.mode must be tcp, http or https")
	}
	if !strings.HasPrefix(cfg.HealthCheck.Path, "/") {
		return fmt.Errorf("health_check.path must start with /")
	}
	if cfg.HealthCheck.Interval <= 0 || cfg.HealthCheck.Timeout <= 0 || cfg.HealthCheck.FailureThreshold < 1 || cfg.HealthCheck.SuccessThreshold < 1 || cfg.HealthCheck.MaxConcurrency < 1 || cfg.HealthCheck.Jitter < 0 || cfg.HealthCheck.Cooldown < 0 || cfg.HealthCheck.SlowStart < 0 || cfg.HealthCheck.SlowStartMinimum < 1 || cfg.HealthCheck.SlowStartMinimum > 100 {
		return fmt.Errorf("health_check values are invalid")
	}
	for _, status := range cfg.HealthCheck.ExpectedStatuses {
		if status < 100 || status > 599 {
			return fmt.Errorf("health_check.expected_statuses contains invalid status %d", status)
		}
	}
	return nil
}

func (cfg *Config) validateBackends() error {
	if len(cfg.Backends) == 0 {
		return fmt.Errorf("at least one backend is required")
	}
	backendIDs := make(map[string]struct{}, len(cfg.Backends))
	backendURLs := make(map[string]struct{}, len(cfg.Backends))
	for _, backend := range cfg.Backends {
		if strings.TrimSpace(backend.ID) == "" {
			return fmt.Errorf("backend.id is required")
		}
		parsed, err := url.Parse(backend.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("invalid backend URL %q", backend.URL)
		}
		if _, exists := backendIDs[backend.ID]; exists {
			return fmt.Errorf("duplicate backend ID %q", backend.ID)
		}
		if _, exists := backendURLs[parsed.String()]; exists {
			return fmt.Errorf("duplicate backend URL %q", parsed.String())
		}
		backendIDs[backend.ID] = struct{}{}
		backendURLs[parsed.String()] = struct{}{}
	}
	return nil
}

func ValidateReload(current, next *Config) error {
	if current.Server.Port != next.Server.Port || current.Server.ReadHeaderTimeout != next.Server.ReadHeaderTimeout || current.Server.ReadTimeout != next.Server.ReadTimeout || current.Server.WriteTimeout != next.Server.WriteTimeout || current.Server.IdleTimeout != next.Server.IdleTimeout || current.Server.AccessLogSampleRate != next.Server.AccessLogSampleRate || current.Server.MaxHeaderBytes != next.Server.MaxHeaderBytes || !reflect.DeepEqual(current.Server.Upstream, next.Server.Upstream) || !reflect.DeepEqual(current.Server.Overload, next.Server.Overload) || !reflect.DeepEqual(current.Management, next.Management) {
		return fmt.Errorf("listener, management and upstream transport changes require a restart")
	}
	if current.RateLimit.Storage != next.RateLimit.Storage || current.RateLimit.LocalShards != next.RateLimit.LocalShards || current.RateLimit.LocalMaxBuckets != next.RateLimit.LocalMaxBuckets || current.RateLimit.CleanupInterval != next.RateLimit.CleanupInterval || current.RateLimit.Retention != next.RateLimit.Retention || !reflect.DeepEqual(current.Database, next.Database) || !reflect.DeepEqual(current.Redis, next.Redis) {
		return fmt.Errorf("rate limit storage connection and lifecycle changes require a restart")
	}
	return nil
}

func parsePrefix(value string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked(), nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(address, address.BitLen()), nil
}
