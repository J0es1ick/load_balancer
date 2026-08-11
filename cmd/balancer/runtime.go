package main

import (
	"fmt"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/server"
)

func mergeRuntimeUpdate(cfg *config.Config, update server.RuntimeUpdate) error {
	if update.RateLimit != nil {
		if update.RateLimit.Capacity > 0 {
			cfg.RateLimit.Capacity = update.RateLimit.Capacity
		}
		if update.RateLimit.RefillPerSecond > 0 {
			cfg.RateLimit.RefillPerSecond = update.RateLimit.RefillPerSecond
		}
		if update.RateLimit.FailureMode != "" {
			cfg.RateLimit.FailureMode = update.RateLimit.FailureMode
		}
	}
	if update.Health != nil {
		if err := mergeHealthUpdate(cfg, update.Health); err != nil {
			return err
		}
	}
	if update.Retry != nil {
		if err := mergeRetryUpdate(cfg, update.Retry); err != nil {
			return err
		}
	}
	return nil
}

func mergeHealthUpdate(cfg *config.Config, value *server.HealthUpdate) error {
	if value.Mode != "" {
		cfg.HealthCheck.Mode = value.Mode
	}
	if value.Path != "" {
		cfg.HealthCheck.Path = value.Path
	}
	durations := []struct {
		name   string
		raw    string
		target *time.Duration
	}{
		{"interval", value.Interval, &cfg.HealthCheck.Interval},
		{"timeout", value.Timeout, &cfg.HealthCheck.Timeout},
		{"jitter", value.Jitter, &cfg.HealthCheck.Jitter},
		{"cooldown", value.Cooldown, &cfg.HealthCheck.Cooldown},
		{"slow start", value.SlowStart, &cfg.HealthCheck.SlowStart},
	}
	for _, duration := range durations {
		if duration.raw == "" {
			continue
		}
		parsed, err := time.ParseDuration(duration.raw)
		if err != nil {
			return fmt.Errorf("invalid health %s: %w", duration.name, err)
		}
		*duration.target = parsed
	}
	if value.FailureThreshold > 0 {
		cfg.HealthCheck.FailureThreshold = value.FailureThreshold
	}
	if value.SuccessThreshold > 0 {
		cfg.HealthCheck.SuccessThreshold = value.SuccessThreshold
	}
	if value.MaxConcurrency > 0 {
		cfg.HealthCheck.MaxConcurrency = value.MaxConcurrency
	}
	if value.SlowStartMinimum > 0 {
		cfg.HealthCheck.SlowStartMinimum = value.SlowStartMinimum
	}
	if len(value.ExpectedStatuses) > 0 {
		cfg.HealthCheck.ExpectedStatuses = append([]int(nil), value.ExpectedStatuses...)
	}
	return nil
}

func mergeRetryUpdate(cfg *config.Config, value *server.RetryUpdate) error {
	if value.MaxAttempts > 0 {
		cfg.Server.Retry.MaxAttempts = value.MaxAttempts
	}
	if value.PerTryTimeout != "" {
		duration, err := time.ParseDuration(value.PerTryTimeout)
		if err != nil {
			return fmt.Errorf("invalid retry timeout: %w", err)
		}
		cfg.Server.Retry.PerTryTimeout = duration
	}
	if len(value.Methods) > 0 {
		cfg.Server.Retry.Methods = append([]string(nil), value.Methods...)
	}
	if len(value.Statuses) > 0 {
		cfg.Server.Retry.Statuses = append([]int(nil), value.Statuses...)
	}
	if value.BudgetCapacity > 0 {
		cfg.Server.Retry.BudgetCapacity = value.BudgetCapacity
	}
	if value.BudgetRefillPerSecond > 0 {
		cfg.Server.Retry.BudgetRefillPerSecond = value.BudgetRefillPerSecond
	}
	return nil
}

func cloneConfig(cfg *config.Config) *config.Config {
	copy := *cfg
	copy.Server.TrustedProxies = append([]string(nil), cfg.Server.TrustedProxies...)
	copy.Server.Retry.Methods = append([]string(nil), cfg.Server.Retry.Methods...)
	copy.Server.Retry.Statuses = append([]int(nil), cfg.Server.Retry.Statuses...)
	copy.Backends = append([]config.BackendConfig(nil), cfg.Backends...)
	copy.HealthCheck.ExpectedStatuses = append([]int(nil), cfg.HealthCheck.ExpectedStatuses...)
	return &copy
}
