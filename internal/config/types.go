package config

import "time"

type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Management  ManagementConfig  `yaml:"management"`
	Database    DatabaseConfig    `yaml:"database"`
	Redis       RedisConfig       `yaml:"redis"`
	Backends    []BackendConfig   `yaml:"backends"`
	RateLimit   RateLimitConfig   `yaml:"rate_limit"`
	HealthCheck HealthCheckConfig `yaml:"health_check"`
}

type ServerConfig struct {
	Port                string         `yaml:"port"`
	TrustedProxies      []string       `yaml:"trusted_proxies"`
	AccessLogSampleRate float64        `yaml:"access_log_sample_rate"`
	ReadHeaderTimeout   time.Duration  `yaml:"read_header_timeout"`
	ReadTimeout         time.Duration  `yaml:"read_timeout"`
	WriteTimeout        time.Duration  `yaml:"write_timeout"`
	IdleTimeout         time.Duration  `yaml:"idle_timeout"`
	ShutdownTimeout     time.Duration  `yaml:"shutdown_timeout"`
	MaxHeaderBytes      int            `yaml:"max_header_bytes"`
	Upstream            UpstreamConfig `yaml:"upstream"`
	Retry               RetryConfig    `yaml:"retry"`
	Overload            OverloadConfig `yaml:"overload"`
}

type UpstreamConfig struct {
	DialTimeout           time.Duration `yaml:"dial_timeout"`
	TLSHandshakeTimeout   time.Duration `yaml:"tls_handshake_timeout"`
	ResponseHeaderTimeout time.Duration `yaml:"response_header_timeout"`
	ExpectContinueTimeout time.Duration `yaml:"expect_continue_timeout"`
	IdleConnTimeout       time.Duration `yaml:"idle_conn_timeout"`
	MaxIdleConns          int           `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost   int           `yaml:"max_idle_conns_per_host"`
	MaxConnsPerHost       int           `yaml:"max_conns_per_host"`
	MaxConcurrentRequests int           `yaml:"max_concurrent_requests"`
}

type RetryConfig struct {
	MaxAttempts           int           `yaml:"max_attempts"`
	PerTryTimeout         time.Duration `yaml:"per_try_timeout"`
	BodyLimit             int64         `yaml:"body_limit"`
	Methods               []string      `yaml:"methods"`
	Statuses              []int         `yaml:"statuses"`
	BudgetCapacity        int           `yaml:"budget_capacity"`
	BudgetRefillPerSecond float64       `yaml:"budget_refill_per_second"`
}

type OverloadConfig struct {
	MaxConcurrentRequests int           `yaml:"max_concurrent_requests"`
	QueueTimeout          time.Duration `yaml:"queue_timeout"`
}

type ManagementConfig struct {
	Enabled          bool          `yaml:"enabled"`
	Address          string        `yaml:"address"`
	AuthTokenEnv     string        `yaml:"auth_token_env"`
	AllowInsecure    bool          `yaml:"allow_insecure"`
	EnablePprof      bool          `yaml:"enable_pprof"`
	RuntimeMutations bool          `yaml:"runtime_mutations"`
	WriteTimeout     time.Duration `yaml:"write_timeout"`
}

type DatabaseConfig struct {
	Host           string        `yaml:"host"`
	Port           string        `yaml:"port"`
	User           string        `yaml:"user"`
	PasswordEnv    string        `yaml:"password_env"`
	Name           string        `yaml:"name"`
	SSLMode        string        `yaml:"sslmode"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	MaxOpenConns   int           `yaml:"max_open_connections"`
}

type RedisConfig struct {
	Address      string        `yaml:"address"`
	PasswordEnv  string        `yaml:"password_env"`
	Database     int           `yaml:"database"`
	PoolSize     int           `yaml:"pool_size"`
	DialTimeout  time.Duration `yaml:"dial_timeout"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

type BackendConfig struct {
	ID  string `yaml:"id"`
	URL string `yaml:"url"`
}

type RateLimitConfig struct {
	Enabled          bool          `yaml:"enabled"`
	Storage          string        `yaml:"storage"`
	FailureMode      string        `yaml:"failure_mode"`
	Capacity         int           `yaml:"capacity"`
	RefillPerSecond  float64       `yaml:"refill_per_second"`
	OperationTimeout time.Duration `yaml:"operation_timeout"`
	LocalShards      int           `yaml:"local_shards"`
	LocalMaxBuckets  int           `yaml:"local_max_buckets"`
	IPv4PrefixBits   int           `yaml:"ipv4_prefix_bits"`
	IPv6PrefixBits   int           `yaml:"ipv6_prefix_bits"`
	CleanupInterval  time.Duration `yaml:"cleanup_interval"`
	Retention        time.Duration `yaml:"retention"`
}

type HealthCheckConfig struct {
	Mode             string        `yaml:"mode"`
	Path             string        `yaml:"path"`
	Interval         time.Duration `yaml:"interval"`
	Timeout          time.Duration `yaml:"timeout"`
	FailureThreshold int           `yaml:"failure_threshold"`
	SuccessThreshold int           `yaml:"success_threshold"`
	MaxConcurrency   int           `yaml:"max_concurrency"`
	Jitter           time.Duration `yaml:"jitter"`
	Cooldown         time.Duration `yaml:"cooldown"`
	ExpectedStatuses []int         `yaml:"expected_statuses"`
	SlowStart        time.Duration `yaml:"slow_start"`
	SlowStartMinimum int           `yaml:"slow_start_minimum_percent"`
}
