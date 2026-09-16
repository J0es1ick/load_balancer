package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/config"
)

func normalizeConfig(source *config.GatewayConfig, defaults Defaults) (*config.GatewayConfig, error) {
	if source == nil {
		return nil, fmt.Errorf("gateway configuration is required")
	}
	normalized, err := cloneConfig(source)
	if err != nil {
		return nil, err
	}
	normalized.ApplyDefaults()
	if normalized.HistoryLimit == 0 {
		normalized.HistoryLimit = defaults.HistoryLimit
	}
	if normalized.HistoryLimit == 0 {
		normalized.HistoryLimit = 10
	}
	if len(normalized.Listeners) == 1 {
		normalized.Listeners[0].Default = true
	}
	baseRetry := normalizeRetry(defaults.Retry)
	baseHealth := normalizeHealth(defaults.Health)
	baseUpstream := normalizeUpstream(defaults.Upstream)
	for index := range normalized.Clusters {
		cluster := &normalized.Clusters[index]
		cluster.Transport = mergeTransport(cluster.Transport, baseUpstream)
		clusterRetry := baseRetry
		if cluster.Retry != nil {
			clusterRetry = mergeRetry(clusterRetry, *cluster.Retry)
		}
		cluster.Retry = retryConfig(clusterRetry)
		mergeHealth(&cluster.Health, baseHealth)
		if cluster.Discovery.Type != "static" {
			if cluster.Discovery.RefreshInterval.Duration() == 0 {
				cluster.Discovery.RefreshInterval = config.Duration(30 * time.Second)
			}
			if cluster.Discovery.StaleAfter.Duration() == 0 {
				cluster.Discovery.StaleAfter = config.Duration(2 * time.Minute)
			}
		}
	}
	for index := range normalized.Routes {
		route := &normalized.Routes[index]
		if route.Action.Redirect != nil && route.Action.Redirect.StatusCode == 0 {
			route.Action.Redirect.StatusCode = 307
		}
		if route.Retry != nil {
			merged := mergeRetry(baseRetry, *route.Retry)
			if route.Timeouts.PerTry.Duration() > 0 {
				merged.PerTryTimeout = route.Timeouts.PerTry.Duration()
			}
			route.Retry = retryConfig(merged)
		}
	}
	if err := normalized.Validate(); err != nil {
		return nil, err
	}
	return normalized, nil
}

func normalizeRetry(policy balancer.RetryPolicy) balancer.RetryPolicy {
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}
	if policy.PerTryTimeout <= 0 {
		policy.PerTryTimeout = 10 * time.Second
	}
	if policy.BodyLimit < 0 {
		policy.BodyLimit = 0
	}
	if len(policy.Methods) == 0 {
		policy.Methods = []string{"GET", "HEAD", "OPTIONS"}
	}
	if policy.BudgetCapacity < 1 {
		policy.BudgetCapacity = 100
	}
	if policy.BudgetRefillPerSecond <= 0 {
		policy.BudgetRefillPerSecond = 10
	}
	return policy
}

func mergeRetry(base balancer.RetryPolicy, override config.GatewayRetryConfig) balancer.RetryPolicy {
	if override.MaxAttempts > 0 {
		base.MaxAttempts = override.MaxAttempts
	}
	if override.PerTryTimeout.Duration() > 0 {
		base.PerTryTimeout = override.PerTryTimeout.Duration()
	}
	if override.BodyLimit > 0 {
		base.BodyLimit = override.BodyLimit
	}
	if override.Methods != nil {
		base.Methods = append([]string(nil), override.Methods...)
	}
	if override.Statuses != nil {
		base.Statuses = append([]int(nil), override.Statuses...)
	}
	if override.BudgetCapacity > 0 {
		base.BudgetCapacity = override.BudgetCapacity
	}
	if override.BudgetRefillPerSecond > 0 {
		base.BudgetRefillPerSecond = override.BudgetRefillPerSecond
	}
	return normalizeRetry(base)
}

func retryConfig(policy balancer.RetryPolicy) *config.GatewayRetryConfig {
	return &config.GatewayRetryConfig{MaxAttempts: policy.MaxAttempts, PerTryTimeout: config.Duration(policy.PerTryTimeout), BodyLimit: policy.BodyLimit, Methods: append([]string(nil), policy.Methods...), Statuses: append([]int(nil), policy.Statuses...), BudgetCapacity: policy.BudgetCapacity, BudgetRefillPerSecond: policy.BudgetRefillPerSecond}
}

func retryPolicy(policy *config.GatewayRetryConfig) balancer.RetryPolicy {
	if policy == nil {
		return normalizeRetry(balancer.RetryPolicy{})
	}
	return balancer.RetryPolicy{MaxAttempts: policy.MaxAttempts, PerTryTimeout: policy.PerTryTimeout.Duration(), BodyLimit: policy.BodyLimit, Methods: append([]string(nil), policy.Methods...), Statuses: append([]int(nil), policy.Statuses...), BudgetCapacity: policy.BudgetCapacity, BudgetRefillPerSecond: policy.BudgetRefillPerSecond}
}

func normalizeHealth(settings balancer.HealthSettings) balancer.HealthSettings {
	if settings.Mode == "" {
		settings.Mode = "http"
	}
	if settings.Path == "" {
		settings.Path = "/health"
	}
	if settings.Interval <= 0 {
		settings.Interval = 5 * time.Second
	}
	if settings.Timeout <= 0 {
		settings.Timeout = 2 * time.Second
	}
	if settings.FailureThreshold < 1 {
		settings.FailureThreshold = 2
	}
	if settings.SuccessThreshold < 1 {
		settings.SuccessThreshold = 1
	}
	if settings.MaxConcurrency < 1 {
		settings.MaxConcurrency = 16
	}
	if settings.Cooldown <= 0 {
		settings.Cooldown = 10 * time.Second
	}
	if len(settings.ExpectedStatuses) == 0 {
		settings.ExpectedStatuses = []int{200, 204}
	}
	if settings.SlowStartMinimum < 1 {
		settings.SlowStartMinimum = 10
	}
	if settings.MaxConcurrentRequests < 1 {
		settings.MaxConcurrentRequests = 512
	}
	return settings
}

func mergeHealth(target *config.GatewayHealthCheckConfig, base balancer.HealthSettings) {
	if target.Mode == "" {
		target.Mode = base.Mode
	}
	if target.Path == "" {
		target.Path = base.Path
	}
	if target.Interval.Duration() == 0 {
		target.Interval = config.Duration(base.Interval)
	}
	if target.Timeout.Duration() == 0 {
		target.Timeout = config.Duration(base.Timeout)
	}
	if target.FailureThreshold == 0 {
		target.FailureThreshold = base.FailureThreshold
	}
	if target.SuccessThreshold == 0 {
		target.SuccessThreshold = base.SuccessThreshold
	}
	if target.MaxConcurrency == 0 {
		target.MaxConcurrency = base.MaxConcurrency
	}
	if target.Jitter.Duration() == 0 {
		target.Jitter = config.Duration(base.Jitter)
	}
	if target.Cooldown.Duration() == 0 {
		target.Cooldown = config.Duration(base.Cooldown)
	}
	if len(target.ExpectedStatuses) == 0 {
		target.ExpectedStatuses = append([]int(nil), base.ExpectedStatuses...)
	}
	if target.SlowStart.Duration() == 0 {
		target.SlowStart = config.Duration(base.SlowStart)
	}
	if target.SlowStartMinimum == 0 {
		target.SlowStartMinimum = base.SlowStartMinimum
	}
}

func healthSettings(value config.GatewayHealthCheckConfig, maxConcurrent int64) balancer.HealthSettings {
	return balancer.HealthSettings{Mode: value.Mode, Path: value.Path, Interval: value.Interval.Duration(), Timeout: value.Timeout.Duration(), FailureThreshold: value.FailureThreshold, SuccessThreshold: value.SuccessThreshold, MaxConcurrency: value.MaxConcurrency, Jitter: value.Jitter.Duration(), Cooldown: value.Cooldown.Duration(), ExpectedStatuses: append([]int(nil), value.ExpectedStatuses...), SlowStart: value.SlowStart.Duration(), SlowStartMinimum: value.SlowStartMinimum, MaxConcurrentRequests: maxConcurrent}
}

func normalizeUpstream(value config.UpstreamConfig) config.UpstreamConfig {
	if value.DialTimeout <= 0 {
		value.DialTimeout = 2 * time.Second
	}
	if value.TLSHandshakeTimeout <= 0 {
		value.TLSHandshakeTimeout = 3 * time.Second
	}
	if value.ResponseHeaderTimeout <= 0 {
		value.ResponseHeaderTimeout = 10 * time.Second
	}
	if value.ExpectContinueTimeout <= 0 {
		value.ExpectContinueTimeout = time.Second
	}
	if value.IdleConnTimeout <= 0 {
		value.IdleConnTimeout = 90 * time.Second
	}
	if value.MaxIdleConns < 1 {
		value.MaxIdleConns = 256
	}
	if value.MaxIdleConnsPerHost < 1 {
		value.MaxIdleConnsPerHost = 64
	}
	if value.MaxConcurrentRequests < 1 {
		value.MaxConcurrentRequests = 512
	}
	return value
}

func mergeTransport(target config.GatewayTransportConfig, base config.UpstreamConfig) config.GatewayTransportConfig {
	if target.Protocol == "" {
		target.Protocol = "auto"
	}
	if target.DialTimeout.Duration() == 0 {
		target.DialTimeout = config.Duration(base.DialTimeout)
	}
	if target.TLSHandshakeTimeout.Duration() == 0 {
		target.TLSHandshakeTimeout = config.Duration(base.TLSHandshakeTimeout)
	}
	if target.ResponseHeaderTimeout.Duration() == 0 {
		target.ResponseHeaderTimeout = config.Duration(base.ResponseHeaderTimeout)
	}
	if target.ExpectContinueTimeout.Duration() == 0 {
		target.ExpectContinueTimeout = config.Duration(base.ExpectContinueTimeout)
	}
	if target.IdleConnTimeout.Duration() == 0 {
		target.IdleConnTimeout = config.Duration(base.IdleConnTimeout)
	}
	if target.MaxIdleConns == 0 {
		target.MaxIdleConns = base.MaxIdleConns
	}
	if target.MaxIdleConnsPerHost == 0 {
		target.MaxIdleConnsPerHost = base.MaxIdleConnsPerHost
	}
	if target.MaxConnsPerHost == 0 {
		target.MaxConnsPerHost = base.MaxConnsPerHost
	}
	if target.MaxConcurrentRequests == 0 {
		target.MaxConcurrentRequests = base.MaxConcurrentRequests
	}
	if target.MaxResponseHeaderBytes == 0 {
		target.MaxResponseHeaderBytes = 1 << 20
	}
	return target
}

func cloneConfig(source *config.GatewayConfig) (*config.GatewayConfig, error) {
	data, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("marshal gateway config: %w", err)
	}
	var destination config.GatewayConfig
	if err := json.Unmarshal(data, &destination); err != nil {
		return nil, fmt.Errorf("clone gateway config: %w", err)
	}
	return &destination, nil
}

func configHash(value *config.GatewayConfig) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
