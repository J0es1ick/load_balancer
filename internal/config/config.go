package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"time"

	"gopkg.in/yaml.v2"
)

const (
	defaultServerPort        = "8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 5 * time.Second
	defaultMaxHeaderBytes    = 1 << 20
	defaultHealthInterval    = 5 * time.Second
	defaultHealthTimeout     = 2 * time.Second
	defaultConnectTimeout    = 5 * time.Second
	defaultRateCapacity      = 100
	defaultRateInterval      = time.Second
)

type ServerConfig struct {
	Port              string        `yaml:"port"`
	TrustedProxies    []string      `yaml:"trusted_proxies"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	ShutdownTimeout   time.Duration `yaml:"shutdown_timeout"`
	MaxHeaderBytes    int           `yaml:"max_header_bytes"`
}

type DatabaseConfig struct {
	Host           string        `yaml:"host"`
	Port           string        `yaml:"port"`
	User           string        `yaml:"user"`
	Password       string        `yaml:"password"`
	Name           string        `yaml:"name"`
	SSLMode        string        `yaml:"sslmode"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
}

type RateLimitConfig struct {
	DefaultCapacity int           `yaml:"default_capacity"`
	DefaultRate     time.Duration `yaml:"default_rate"`
}

type HealthCheckConfig struct {
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
}

type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Database    DatabaseConfig    `yaml:"database"`
	Backends    []string          `yaml:"backends"`
	RateLimit   RateLimitConfig   `yaml:"rate_limit"`
	HealthCheck HealthCheckConfig `yaml:"health_check"`
}

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
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = defaultWriteTimeout
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
	if cfg.Database.ConnectTimeout == 0 {
		cfg.Database.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.RateLimit.DefaultCapacity == 0 {
		cfg.RateLimit.DefaultCapacity = defaultRateCapacity
	}
	if cfg.RateLimit.DefaultRate == 0 {
		cfg.RateLimit.DefaultRate = defaultRateInterval
	}
	if cfg.HealthCheck.Interval == 0 {
		cfg.HealthCheck.Interval = defaultHealthInterval
	}
	if cfg.HealthCheck.Timeout == 0 {
		cfg.HealthCheck.Timeout = defaultHealthTimeout
	}
}

func (cfg *Config) Validate() error {
	port, err := strconv.Atoi(cfg.Server.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("server.port must be a number between 1 and 65535")
	}

	if cfg.Server.ReadHeaderTimeout <= 0 ||
		cfg.Server.ReadTimeout <= 0 ||
		cfg.Server.WriteTimeout <= 0 ||
		cfg.Server.IdleTimeout <= 0 ||
		cfg.Server.ShutdownTimeout <= 0 {
		return fmt.Errorf("server timeouts must be positive")
	}
	if cfg.Server.MaxHeaderBytes < 1024 {
		return fmt.Errorf("server.max_header_bytes must be at least 1024")
	}

	for _, proxy := range cfg.Server.TrustedProxies {
		if _, err := parsePrefix(proxy); err != nil {
			return fmt.Errorf("invalid trusted proxy %q: %w", proxy, err)
		}
	}

	if cfg.Database.Host == "" || cfg.Database.Port == "" || cfg.Database.User == "" || cfg.Database.Name == "" {
		return fmt.Errorf("database host, port, user and name are required")
	}
	if cfg.Database.ConnectTimeout <= 0 {
		return fmt.Errorf("database.connect_timeout must be positive")
	}

	if len(cfg.Backends) == 0 {
		return fmt.Errorf("at least one backend is required")
	}
	backendIDs := make(map[string]struct{}, len(cfg.Backends))
	for _, rawURL := range cfg.Backends {
		backendURL, err := url.Parse(rawURL)
		if err != nil || backendURL.Host == "" || (backendURL.Scheme != "http" && backendURL.Scheme != "https") {
			return fmt.Errorf("invalid backend URL %q", rawURL)
		}
		id := backendURL.Hostname()
		if _, exists := backendIDs[id]; exists {
			return fmt.Errorf("backend IDs must be unique; duplicate hostname %q", id)
		}
		backendIDs[id] = struct{}{}
	}

	if cfg.RateLimit.DefaultCapacity < 1 {
		return fmt.Errorf("rate_limit.default_capacity must be positive")
	}
	if cfg.RateLimit.DefaultRate <= 0 {
		return fmt.Errorf("rate_limit.default_rate must be positive")
	}
	if cfg.HealthCheck.Interval <= 0 || cfg.HealthCheck.Timeout <= 0 {
		return fmt.Errorf("health_check interval and timeout must be positive")
	}

	return nil
}

func ValidateReload(current, next *Config) error {
	if current.Server.Port != next.Server.Port ||
		current.Server.ReadHeaderTimeout != next.Server.ReadHeaderTimeout ||
		current.Server.ReadTimeout != next.Server.ReadTimeout ||
		current.Server.WriteTimeout != next.Server.WriteTimeout ||
		current.Server.IdleTimeout != next.Server.IdleTimeout ||
		current.Server.MaxHeaderBytes != next.Server.MaxHeaderBytes {
		return fmt.Errorf("server port and HTTP timeout changes require a restart")
	}
	if !reflect.DeepEqual(current.Database, next.Database) {
		return fmt.Errorf("database configuration changes require a restart")
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
