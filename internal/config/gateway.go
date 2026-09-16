package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const GatewayAPIVersion = "proxy/v1"

type Duration time.Duration

func (duration Duration) Duration() time.Duration { return time.Duration(duration) }
func (duration Duration) String() string          { return time.Duration(duration).String() }

func (duration Duration) MarshalJSON() ([]byte, error) { return json.Marshal(duration.String()) }
func (duration Duration) MarshalYAML() (any, error)    { return duration.String(), nil }
func (duration Duration) MarshalText() ([]byte, error) { return []byte(duration.String()), nil }

func (duration *Duration) UnmarshalText(data []byte) error { return duration.parse(string(data)) }

func (duration *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	return duration.parse(value)
}

func (duration *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var value string
	if err := unmarshal(&value); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	return duration.parse(value)
}

func (duration *Duration) parse(value string) error {
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	*duration = Duration(parsed)
	return nil
}

type GatewayConfig struct {
	APIVersion   string                  `yaml:"apiVersion" json:"apiVersion"`
	HistoryLimit int                     `yaml:"history_limit" json:"history_limit,omitempty"`
	Listeners    []GatewayListenerConfig `yaml:"listeners" json:"listeners"`
	Routes       []GatewayRouteConfig    `yaml:"routes" json:"routes"`
	Clusters     []GatewayClusterConfig  `yaml:"clusters" json:"clusters"`
}

type GatewayListenerConfig struct {
	ID        string          `yaml:"id" json:"id"`
	Address   string          `yaml:"address" json:"address"`
	Protocol  string          `yaml:"protocol" json:"protocol"`
	Hostnames []string        `yaml:"hostnames" json:"hostnames,omitempty"`
	Default   bool            `yaml:"default" json:"default,omitempty"`
	TLS       ServerTLSConfig `yaml:"tls" json:"tls,omitempty"`
}

type GatewayRouteConfig struct {
	ID                  string                  `yaml:"id" json:"id"`
	Listener            string                  `yaml:"listener" json:"listener,omitempty"`
	Priority            int                     `yaml:"priority" json:"priority,omitempty"`
	Match               GatewayRouteMatch       `yaml:"match" json:"match"`
	Action              GatewayRouteAction      `yaml:"action" json:"action"`
	Retry               *GatewayRetryConfig     `yaml:"retry" json:"retry,omitempty"`
	Timeouts            GatewayTimeoutConfig    `yaml:"timeouts" json:"timeouts,omitempty"`
	MaxRequestBodyBytes int64                   `yaml:"max_request_body_bytes" json:"max_request_body_bytes,omitempty"`
	RateLimit           *GatewayRateLimitConfig `yaml:"rate_limit" json:"rate_limit,omitempty"`
	RequestHeaders      GatewayHeaderPolicy     `yaml:"request_headers" json:"request_headers,omitempty"`
	ResponseHeaders     GatewayHeaderPolicy     `yaml:"response_headers" json:"response_headers,omitempty"`
}

type GatewayRouteMatch struct {
	Hosts      []string                   `yaml:"hosts" json:"hosts,omitempty"`
	PathExact  string                     `yaml:"path_exact" json:"path_exact,omitempty"`
	PathPrefix string                     `yaml:"path_prefix" json:"path_prefix,omitempty"`
	Methods    []string                   `yaml:"methods" json:"methods,omitempty"`
	Headers    []GatewayHeaderMatchConfig `yaml:"headers" json:"headers,omitempty"`
}

type GatewayHeaderMatchConfig struct {
	Name    string `yaml:"name" json:"name"`
	Exact   string `yaml:"exact" json:"exact,omitempty"`
	Present *bool  `yaml:"present" json:"present,omitempty"`
}

type GatewayRouteAction struct {
	Cluster          string                         `yaml:"cluster" json:"cluster,omitempty"`
	WeightedClusters []GatewayWeightedClusterConfig `yaml:"weighted_clusters" json:"weighted_clusters,omitempty"`
	Redirect         *GatewayRedirectConfig         `yaml:"redirect" json:"redirect,omitempty"`
	RewritePrefix    string                         `yaml:"rewrite_prefix" json:"rewrite_prefix,omitempty"`
	PreserveHost     bool                           `yaml:"preserve_host" json:"preserve_host,omitempty"`
	HostRewrite      string                         `yaml:"host_rewrite" json:"host_rewrite,omitempty"`
}

type GatewayWeightedClusterConfig struct {
	Cluster string `yaml:"cluster" json:"cluster"`
	Weight  int    `yaml:"weight" json:"weight"`
}

type GatewayRedirectConfig struct {
	Scheme        string `yaml:"scheme" json:"scheme,omitempty"`
	Host          string `yaml:"host" json:"host,omitempty"`
	Path          string `yaml:"path" json:"path,omitempty"`
	StatusCode    int    `yaml:"status_code" json:"status_code,omitempty"`
	PreserveQuery bool   `yaml:"preserve_query" json:"preserve_query,omitempty"`
}

type GatewayTimeoutConfig struct {
	Request Duration `yaml:"request" json:"request,omitempty"`
	PerTry  Duration `yaml:"per_try" json:"per_try,omitempty"`
}

type GatewayRateLimitConfig struct {
	Enabled         bool    `yaml:"enabled" json:"enabled"`
	Capacity        int     `yaml:"capacity" json:"capacity"`
	RefillPerSecond float64 `yaml:"refill_per_second" json:"refill_per_second"`
}

type GatewayHeaderPolicy struct {
	Set    map[string]string   `yaml:"set" json:"set,omitempty"`
	Add    map[string][]string `yaml:"add" json:"add,omitempty"`
	Remove []string            `yaml:"remove" json:"remove,omitempty"`
}

type GatewayClusterConfig struct {
	ID        string                   `yaml:"id" json:"id"`
	Strategy  string                   `yaml:"strategy" json:"strategy"`
	HashKey   string                   `yaml:"hash_key" json:"hash_key,omitempty"`
	Endpoints []GatewayEndpointConfig  `yaml:"endpoints" json:"endpoints"`
	Health    GatewayHealthCheckConfig `yaml:"health" json:"health,omitempty"`
	Retry     *GatewayRetryConfig      `yaml:"retry" json:"retry,omitempty"`
	Transport GatewayTransportConfig   `yaml:"transport" json:"transport,omitempty"`
	TLS       ClientTLSConfig          `yaml:"tls" json:"tls,omitempty"`
	Discovery GatewayDiscoveryConfig   `yaml:"discovery" json:"discovery,omitempty"`
}

type GatewayEndpointConfig struct {
	ID       string `yaml:"id" json:"id"`
	URL      string `yaml:"url" json:"url"`
	Weight   int    `yaml:"weight" json:"weight,omitempty"`
	Disabled bool   `yaml:"disabled" json:"disabled,omitempty"`
}

type GatewayHealthCheckConfig struct {
	Enabled          bool     `yaml:"enabled" json:"enabled"`
	Mode             string   `yaml:"mode" json:"mode,omitempty"`
	Path             string   `yaml:"path" json:"path,omitempty"`
	Interval         Duration `yaml:"interval" json:"interval,omitempty"`
	Timeout          Duration `yaml:"timeout" json:"timeout,omitempty"`
	FailureThreshold int      `yaml:"failure_threshold" json:"failure_threshold,omitempty"`
	SuccessThreshold int      `yaml:"success_threshold" json:"success_threshold,omitempty"`
	MaxConcurrency   int      `yaml:"max_concurrency" json:"max_concurrency,omitempty"`
	Jitter           Duration `yaml:"jitter" json:"jitter,omitempty"`
	Cooldown         Duration `yaml:"cooldown" json:"cooldown,omitempty"`
	ExpectedStatuses []int    `yaml:"expected_statuses" json:"expected_statuses,omitempty"`
	SlowStart        Duration `yaml:"slow_start" json:"slow_start,omitempty"`
	SlowStartMinimum int      `yaml:"slow_start_minimum_percent" json:"slow_start_minimum_percent,omitempty"`
}

type GatewayRetryConfig struct {
	MaxAttempts           int      `yaml:"max_attempts" json:"max_attempts,omitempty"`
	PerTryTimeout         Duration `yaml:"per_try_timeout" json:"per_try_timeout,omitempty"`
	BodyLimit             int64    `yaml:"body_limit" json:"body_limit,omitempty"`
	Methods               []string `yaml:"methods" json:"methods,omitempty"`
	Statuses              []int    `yaml:"statuses" json:"statuses,omitempty"`
	BudgetCapacity        int      `yaml:"budget_capacity" json:"budget_capacity,omitempty"`
	BudgetRefillPerSecond float64  `yaml:"budget_refill_per_second" json:"budget_refill_per_second,omitempty"`
}

type GatewayTransportConfig struct {
	Protocol               string   `yaml:"protocol" json:"protocol,omitempty"`
	DialTimeout            Duration `yaml:"dial_timeout" json:"dial_timeout,omitempty"`
	TLSHandshakeTimeout    Duration `yaml:"tls_handshake_timeout" json:"tls_handshake_timeout,omitempty"`
	ResponseHeaderTimeout  Duration `yaml:"response_header_timeout" json:"response_header_timeout,omitempty"`
	ExpectContinueTimeout  Duration `yaml:"expect_continue_timeout" json:"expect_continue_timeout,omitempty"`
	IdleConnTimeout        Duration `yaml:"idle_conn_timeout" json:"idle_conn_timeout,omitempty"`
	MaxIdleConns           int      `yaml:"max_idle_conns" json:"max_idle_conns,omitempty"`
	MaxIdleConnsPerHost    int      `yaml:"max_idle_conns_per_host" json:"max_idle_conns_per_host,omitempty"`
	MaxConnsPerHost        int      `yaml:"max_conns_per_host" json:"max_conns_per_host,omitempty"`
	MaxConcurrentRequests  int      `yaml:"max_concurrent_requests" json:"max_concurrent_requests,omitempty"`
	MaxResponseHeaderBytes int64    `yaml:"max_response_header_bytes" json:"max_response_header_bytes,omitempty"`
}

type GatewayDiscoveryConfig struct {
	Type            string   `yaml:"type" json:"type,omitempty"`
	Hostname        string   `yaml:"hostname" json:"hostname,omitempty"`
	Port            int      `yaml:"port" json:"port,omitempty"`
	Scheme          string   `yaml:"scheme" json:"scheme,omitempty"`
	Namespace       string   `yaml:"namespace" json:"namespace,omitempty"`
	Service         string   `yaml:"service" json:"service,omitempty"`
	APIServer       string   `yaml:"api_server" json:"api_server,omitempty"`
	TokenFile       string   `yaml:"token_file" json:"token_file,omitempty"`
	CAFile          string   `yaml:"ca_file" json:"ca_file,omitempty"`
	RefreshInterval Duration `yaml:"refresh_interval" json:"refresh_interval,omitempty"`
	StaleAfter      Duration `yaml:"stale_after" json:"stale_after,omitempty"`
}

func (gateway *GatewayConfig) ApplyDefaults() {
	if gateway == nil {
		return
	}
	if gateway.APIVersion == "" {
		gateway.APIVersion = GatewayAPIVersion
	}
	if gateway.HistoryLimit == 0 {
		gateway.HistoryLimit = 10
	}
	for index := range gateway.Listeners {
		if gateway.Listeners[index].Protocol == "" {
			gateway.Listeners[index].Protocol = "http1"
		}
	}
	if len(gateway.Listeners) == 1 {
		gateway.Listeners[0].Default = true
	}
	for routeIndex := range gateway.Routes {
		for methodIndex := range gateway.Routes[routeIndex].Match.Methods {
			gateway.Routes[routeIndex].Match.Methods[methodIndex] = strings.ToUpper(gateway.Routes[routeIndex].Match.Methods[methodIndex])
		}
		if redirect := gateway.Routes[routeIndex].Action.Redirect; redirect != nil && redirect.StatusCode == 0 {
			redirect.StatusCode = http.StatusTemporaryRedirect
		}
	}
	for index := range gateway.Clusters {
		cluster := &gateway.Clusters[index]
		if cluster.TLS.CAFile != "" || cluster.TLS.ServerName != "" || cluster.TLS.ClientCertFile != "" || cluster.TLS.ClientKeyFile != "" {
			cluster.TLS.Enabled = true
		}
		if cluster.Strategy == "" {
			cluster.Strategy = "round_robin"
		}
		if cluster.Discovery.Type == "" {
			cluster.Discovery.Type = "static"
		}
		if cluster.Discovery.Type != "static" && cluster.Discovery.RefreshInterval.Duration() == 0 {
			cluster.Discovery.RefreshInterval = Duration(30 * time.Second)
		}
		if cluster.Discovery.Type != "static" && cluster.Discovery.StaleAfter.Duration() == 0 {
			cluster.Discovery.StaleAfter = Duration(2 * time.Minute)
		}
		if cluster.Transport.Protocol == "" {
			cluster.Transport.Protocol = "auto"
		}
		for endpoint := range cluster.Endpoints {
			if cluster.Endpoints[endpoint].Weight == 0 {
				cluster.Endpoints[endpoint].Weight = 1
			}
		}
	}
}

func (gateway *GatewayConfig) ApplyInheritedDefaults(upstream UpstreamConfig, retry RetryConfig, health HealthCheckConfig) {
	if gateway == nil {
		return
	}
	gateway.ApplyDefaults()
	for index := range gateway.Clusters {
		cluster := &gateway.Clusters[index]
		transport := &cluster.Transport
		if transport.DialTimeout.Duration() == 0 {
			transport.DialTimeout = Duration(upstream.DialTimeout)
		}
		if transport.TLSHandshakeTimeout.Duration() == 0 {
			transport.TLSHandshakeTimeout = Duration(upstream.TLSHandshakeTimeout)
		}
		if transport.ResponseHeaderTimeout.Duration() == 0 {
			transport.ResponseHeaderTimeout = Duration(upstream.ResponseHeaderTimeout)
		}
		if transport.ExpectContinueTimeout.Duration() == 0 {
			transport.ExpectContinueTimeout = Duration(upstream.ExpectContinueTimeout)
		}
		if transport.IdleConnTimeout.Duration() == 0 {
			transport.IdleConnTimeout = Duration(upstream.IdleConnTimeout)
		}
		if transport.MaxIdleConns == 0 {
			transport.MaxIdleConns = upstream.MaxIdleConns
		}
		if transport.MaxIdleConnsPerHost == 0 {
			transport.MaxIdleConnsPerHost = upstream.MaxIdleConnsPerHost
		}
		if transport.MaxConnsPerHost == 0 {
			transport.MaxConnsPerHost = upstream.MaxConnsPerHost
		}
		if transport.MaxConcurrentRequests == 0 {
			transport.MaxConcurrentRequests = upstream.MaxConcurrentRequests
		}
		if transport.MaxResponseHeaderBytes == 0 {
			transport.MaxResponseHeaderBytes = 1 << 20
		}
		if cluster.Retry == nil {
			cluster.Retry = &GatewayRetryConfig{}
		}
		mergeGatewayRetryDefaults(cluster.Retry, retry)
		if cluster.Health.Enabled {
			mergeGatewayHealthDefaults(&cluster.Health, health)
		}
	}
	for index := range gateway.Routes {
		if gateway.Routes[index].Retry != nil {
			mergeGatewayRetryDefaults(gateway.Routes[index].Retry, retry)
		}
	}
}

func mergeGatewayRetryDefaults(target *GatewayRetryConfig, source RetryConfig) {
	if target.MaxAttempts == 0 {
		target.MaxAttempts = source.MaxAttempts
	}
	if target.PerTryTimeout.Duration() == 0 {
		target.PerTryTimeout = Duration(source.PerTryTimeout)
	}
	if target.BodyLimit == 0 {
		target.BodyLimit = source.BodyLimit
	}
	if len(target.Methods) == 0 {
		target.Methods = append([]string(nil), source.Methods...)
	}
	if len(target.Statuses) == 0 {
		target.Statuses = append([]int(nil), source.Statuses...)
	}
	if target.BudgetCapacity == 0 {
		target.BudgetCapacity = source.BudgetCapacity
	}
	if target.BudgetRefillPerSecond == 0 {
		target.BudgetRefillPerSecond = source.BudgetRefillPerSecond
	}
}

func mergeGatewayHealthDefaults(target *GatewayHealthCheckConfig, source HealthCheckConfig) {
	if target.Mode == "" {
		target.Mode = source.Mode
	}
	if target.Path == "" {
		target.Path = source.Path
	}
	if target.Interval.Duration() == 0 {
		target.Interval = Duration(source.Interval)
	}
	if target.Timeout.Duration() == 0 {
		target.Timeout = Duration(source.Timeout)
	}
	if target.FailureThreshold == 0 {
		target.FailureThreshold = source.FailureThreshold
	}
	if target.SuccessThreshold == 0 {
		target.SuccessThreshold = source.SuccessThreshold
	}
	if target.MaxConcurrency == 0 {
		target.MaxConcurrency = source.MaxConcurrency
	}
	if target.Jitter.Duration() == 0 {
		target.Jitter = Duration(source.Jitter)
	}
	if target.Cooldown.Duration() == 0 {
		target.Cooldown = Duration(source.Cooldown)
	}
	if len(target.ExpectedStatuses) == 0 {
		target.ExpectedStatuses = append([]int(nil), source.ExpectedStatuses...)
	}
	if target.SlowStart.Duration() == 0 {
		target.SlowStart = Duration(source.SlowStart)
	}
	if target.SlowStartMinimum == 0 {
		target.SlowStartMinimum = source.SlowStartMinimum
	}
}

func (gateway *GatewayConfig) Validate() error {
	if gateway == nil {
		return nil
	}
	if gateway.APIVersion != GatewayAPIVersion {
		return fmt.Errorf("gateway.apiVersion must be %q", GatewayAPIVersion)
	}
	if gateway.HistoryLimit < 1 || gateway.HistoryLimit > 100 {
		return fmt.Errorf("gateway.history_limit must be between 1 and 100")
	}
	listeners := make(map[string]GatewayListenerConfig, len(gateway.Listeners))
	defaultListeners := 0
	for _, listener := range gateway.Listeners {
		if listener.ID == "" || listeners[listener.ID].ID != "" {
			return fmt.Errorf("gateway listener IDs must be unique and nonempty")
		}
		if !slices.Contains([]string{"http1", "h2c", "http2"}, listener.Protocol) {
			return fmt.Errorf("gateway listener %q has unsupported protocol %q", listener.ID, listener.Protocol)
		}
		if listener.Address == "" {
			return fmt.Errorf("gateway listener %q requires address", listener.ID)
		}
		if listener.Default {
			defaultListeners++
		}
		if err := listener.TLS.Validate(); err != nil {
			return fmt.Errorf("gateway listener %q: %w", listener.ID, err)
		}
		if listener.Protocol == "http2" && listener.TLS.CertFile == "" {
			return fmt.Errorf("gateway listener %q HTTP/2 requires TLS", listener.ID)
		}
		for _, host := range listener.Hostnames {
			if err := validateHostnamePattern(host); err != nil {
				return fmt.Errorf("gateway listener %q: %w", listener.ID, err)
			}
		}
		listeners[listener.ID] = listener
	}
	if len(listeners) == 0 {
		return fmt.Errorf("gateway requires at least one listener")
	}
	if defaultListeners > 1 {
		return fmt.Errorf("gateway can have at most one default listener")
	}

	clusters := make(map[string]GatewayClusterConfig, len(gateway.Clusters))
	for _, cluster := range gateway.Clusters {
		if err := validateGatewayCluster(cluster); err != nil {
			return err
		}
		if _, exists := clusters[cluster.ID]; exists {
			return fmt.Errorf("duplicate gateway cluster %q", cluster.ID)
		}
		clusters[cluster.ID] = cluster
	}
	routes := make(map[string]struct{}, len(gateway.Routes))
	for _, route := range gateway.Routes {
		if route.ID == "" {
			return fmt.Errorf("gateway route ID is required")
		}
		if _, exists := routes[route.ID]; exists {
			return fmt.Errorf("duplicate gateway route %q", route.ID)
		}
		routes[route.ID] = struct{}{}
		if route.Listener != "" {
			if _, exists := listeners[route.Listener]; !exists {
				return fmt.Errorf("gateway route %q references unknown listener %q", route.ID, route.Listener)
			}
		}
		if err := validateRoute(route, clusters); err != nil {
			return fmt.Errorf("gateway route %q: %w", route.ID, err)
		}
	}
	if len(routes) == 0 {
		return fmt.Errorf("gateway requires at least one route")
	}
	return nil
}

func validateGatewayCluster(cluster GatewayClusterConfig) error {
	if cluster.ID == "" {
		return fmt.Errorf("gateway cluster ID is required")
	}
	if !slices.Contains([]string{"round_robin", "weighted_round_robin", "least_request", "rendezvous"}, cluster.Strategy) {
		return fmt.Errorf("gateway cluster %q has unsupported strategy %q", cluster.ID, cluster.Strategy)
	}
	if cluster.Strategy == "rendezvous" && cluster.HashKey == "" {
		return fmt.Errorf("gateway cluster %q rendezvous strategy requires hash_key", cluster.ID)
	}
	if cluster.Strategy == "rendezvous" && !validHashKey(cluster.HashKey) {
		return fmt.Errorf("gateway cluster %q has unsupported hash_key %q", cluster.ID, cluster.HashKey)
	}
	if !slices.Contains([]string{"auto", "http1", "http2", "h2c"}, cluster.Transport.Protocol) {
		return fmt.Errorf("gateway cluster %q has unsupported transport protocol %q", cluster.ID, cluster.Transport.Protocol)
	}
	if cluster.Transport.DialTimeout.Duration() < 0 || cluster.Transport.TLSHandshakeTimeout.Duration() < 0 || cluster.Transport.ResponseHeaderTimeout.Duration() < 0 || cluster.Transport.ExpectContinueTimeout.Duration() < 0 || cluster.Transport.IdleConnTimeout.Duration() < 0 || cluster.Transport.MaxIdleConns < 0 || cluster.Transport.MaxIdleConnsPerHost < 0 || cluster.Transport.MaxConnsPerHost < 0 || cluster.Transport.MaxConcurrentRequests < 0 || cluster.Transport.MaxResponseHeaderBytes < 0 {
		return fmt.Errorf("gateway cluster %q has invalid transport limits", cluster.ID)
	}
	if cluster.TLS.Enabled && cluster.TLS.ServerName == "" && len(cluster.Endpoints) == 0 {
		return fmt.Errorf("gateway cluster %q TLS discovery requires server_name", cluster.ID)
	}
	if (cluster.TLS.ClientCertFile == "") != (cluster.TLS.ClientKeyFile == "") {
		return fmt.Errorf("gateway cluster %q client certificate and key must be configured together", cluster.ID)
	}
	if cluster.Discovery.Type == "" {
		cluster.Discovery.Type = "static"
	}
	if !slices.Contains([]string{"static", "dns", "kubernetes"}, cluster.Discovery.Type) {
		return fmt.Errorf("gateway cluster %q has unsupported discovery %q", cluster.ID, cluster.Discovery.Type)
	}
	if cluster.Discovery.Type == "static" && len(cluster.Endpoints) == 0 {
		return fmt.Errorf("gateway cluster %q requires at least one static endpoint", cluster.ID)
	}
	if cluster.Discovery.Type == "dns" && (cluster.Discovery.Hostname == "" || cluster.Discovery.Port < 1 || cluster.Discovery.Port > 65535) {
		return fmt.Errorf("gateway cluster %q DNS discovery requires hostname and valid port", cluster.ID)
	}
	if cluster.Discovery.Type == "kubernetes" && (cluster.Discovery.Namespace == "" || cluster.Discovery.Service == "" || cluster.Discovery.Port < 1 || cluster.Discovery.Port > 65535) {
		return fmt.Errorf("gateway cluster %q Kubernetes discovery requires namespace, service and explicit port", cluster.ID)
	}
	if cluster.Discovery.Type != "static" && (cluster.Discovery.RefreshInterval.Duration() <= 0 || cluster.Discovery.StaleAfter.Duration() <= 0) {
		return fmt.Errorf("gateway cluster %q discovery intervals must be positive", cluster.ID)
	}
	ids := make(map[string]struct{}, len(cluster.Endpoints))
	urls := make(map[string]struct{}, len(cluster.Endpoints))
	for _, endpoint := range cluster.Endpoints {
		parsed, err := url.Parse(endpoint.URL)
		if endpoint.ID == "" || err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || !slices.Contains([]string{"http", "https"}, parsed.Scheme) {
			return fmt.Errorf("gateway cluster %q has invalid endpoint %q", cluster.ID, endpoint.ID)
		}
		if cluster.Transport.Protocol == "h2c" && parsed.Scheme != "http" {
			return fmt.Errorf("gateway cluster %q h2c endpoint %q must use http", cluster.ID, endpoint.ID)
		}
		if cluster.Transport.Protocol == "http2" && parsed.Scheme != "https" {
			return fmt.Errorf("gateway cluster %q HTTP/2 endpoint %q must use https", cluster.ID, endpoint.ID)
		}
		if cluster.TLS.Enabled && parsed.Scheme != "https" {
			return fmt.Errorf("gateway cluster %q TLS endpoint %q must use https", cluster.ID, endpoint.ID)
		}
		if endpoint.Weight < 1 {
			return fmt.Errorf("gateway cluster %q endpoint %q weight must be positive", cluster.ID, endpoint.ID)
		}
		if _, exists := ids[endpoint.ID]; exists {
			return fmt.Errorf("gateway cluster %q has duplicate endpoint ID %q", cluster.ID, endpoint.ID)
		}
		if _, exists := urls[parsed.String()]; exists {
			return fmt.Errorf("gateway cluster %q has duplicate endpoint URL %q", cluster.ID, parsed.String())
		}
		ids[endpoint.ID] = struct{}{}
		urls[parsed.String()] = struct{}{}
	}
	if cluster.Health.Enabled {
		if !slices.Contains([]string{"tcp", "http", "https"}, cluster.Health.Mode) || cluster.Health.Interval.Duration() <= 0 || cluster.Health.Timeout.Duration() <= 0 || cluster.Health.FailureThreshold < 1 || cluster.Health.SuccessThreshold < 1 || cluster.Health.MaxConcurrency < 1 {
			return fmt.Errorf("gateway cluster %q has invalid health policy", cluster.ID)
		}
		if cluster.Health.Mode != "tcp" && !strings.HasPrefix(cluster.Health.Path, "/") {
			return fmt.Errorf("gateway cluster %q health path must start with /", cluster.ID)
		}
	}
	if cluster.Retry != nil {
		if err := validateGatewayRetry(*cluster.Retry); err != nil {
			return fmt.Errorf("gateway cluster %q: %w", cluster.ID, err)
		}
	}
	return nil
}

func validateRoute(route GatewayRouteConfig, clusters map[string]GatewayClusterConfig) error {
	if route.Match.PathExact != "" && route.Match.PathPrefix != "" {
		return fmt.Errorf("path_exact and path_prefix are mutually exclusive")
	}
	if route.Match.PathExact != "" && !strings.HasPrefix(route.Match.PathExact, "/") {
		return fmt.Errorf("path_exact must start with /")
	}
	if route.Match.PathPrefix != "" && !strings.HasPrefix(route.Match.PathPrefix, "/") {
		return fmt.Errorf("path_prefix must start with /")
	}
	for _, method := range route.Match.Methods {
		if method == "" || !validHTTPToken(method) {
			return fmt.Errorf("method %q is invalid", method)
		}
	}
	for _, host := range route.Match.Hosts {
		if err := validateHostnamePattern(host); err != nil {
			return err
		}
	}
	for _, header := range route.Match.Headers {
		if !validHeaderName(header.Name) || (header.Exact == "" && header.Present == nil) {
			return fmt.Errorf("header matches require name and exact or present")
		}
		if forbiddenGatewayHeader(header.Name) {
			return fmt.Errorf("header %q cannot be used for route matching", header.Name)
		}
		if strings.ContainsAny(header.Exact, "\r\n") {
			return fmt.Errorf("header match %q contains a line break", header.Name)
		}
		if header.Exact != "" && header.Present != nil && !*header.Present {
			return fmt.Errorf("header match cannot require an exact value and absence")
		}
	}
	actions := 0
	if route.Action.Cluster != "" {
		actions++
	}
	if len(route.Action.WeightedClusters) > 0 {
		actions++
	}
	if route.Action.Redirect != nil {
		actions++
	}
	if actions != 1 {
		return fmt.Errorf("action must define exactly one of cluster, weighted_clusters or redirect")
	}
	if route.Action.Cluster != "" {
		if _, exists := clusters[route.Action.Cluster]; !exists {
			return fmt.Errorf("unknown cluster %q", route.Action.Cluster)
		}
	}
	seen := map[string]struct{}{}
	for _, target := range route.Action.WeightedClusters {
		if _, exists := clusters[target.Cluster]; !exists {
			return fmt.Errorf("unknown weighted cluster %q", target.Cluster)
		}
		if target.Weight < 1 {
			return fmt.Errorf("weighted cluster %q weight must be positive", target.Cluster)
		}
		if _, exists := seen[target.Cluster]; exists {
			return fmt.Errorf("duplicate weighted cluster %q", target.Cluster)
		}
		seen[target.Cluster] = struct{}{}
	}
	if redirect := route.Action.Redirect; redirect != nil {
		if redirect.StatusCode == 0 {
			redirect.StatusCode = http.StatusTemporaryRedirect
		}
		if !slices.Contains([]int{301, 302, 303, 307, 308}, redirect.StatusCode) {
			return fmt.Errorf("unsupported redirect status %d", redirect.StatusCode)
		}
		if redirect.Scheme != "" && redirect.Scheme != "http" && redirect.Scheme != "https" {
			return fmt.Errorf("redirect scheme must be http or https")
		}
		if redirect.Host != "" && !validAuthority(redirect.Host) {
			return fmt.Errorf("redirect host %q is invalid", redirect.Host)
		}
		if redirect.Path != "" && (!strings.HasPrefix(redirect.Path, "/") || strings.ContainsAny(redirect.Path, "\r\n")) {
			return fmt.Errorf("redirect path must be an absolute path")
		}
	}
	if route.Action.HostRewrite != "" && route.Action.PreserveHost {
		return fmt.Errorf("host_rewrite and preserve_host are mutually exclusive")
	}
	if route.Action.HostRewrite != "" && !validAuthority(route.Action.HostRewrite) {
		return fmt.Errorf("host_rewrite %q is invalid", route.Action.HostRewrite)
	}
	if route.Retry != nil {
		if err := validateGatewayRetry(*route.Retry); err != nil {
			return err
		}
	}
	if route.Timeouts.Request.Duration() < 0 || route.Timeouts.PerTry.Duration() < 0 {
		return fmt.Errorf("timeouts cannot be negative")
	}
	if route.MaxRequestBodyBytes < 0 {
		return fmt.Errorf("max_request_body_bytes cannot be negative")
	}
	if route.RateLimit != nil && route.RateLimit.Enabled && (route.RateLimit.Capacity < 1 || route.RateLimit.RefillPerSecond <= 0) {
		return fmt.Errorf("route rate_limit values must be positive")
	}
	return validateHeaderPolicies(route.RequestHeaders, route.ResponseHeaders)
}

func validateGatewayRetry(retry GatewayRetryConfig) error {
	if retry.MaxAttempts < 1 || retry.MaxAttempts > 5 || retry.PerTryTimeout.Duration() <= 0 || retry.BodyLimit < 0 || retry.BudgetCapacity < 1 || retry.BudgetRefillPerSecond <= 0 {
		return fmt.Errorf("retry policy is invalid")
	}
	for _, status := range retry.Statuses {
		if status < 400 || status > 599 {
			return fmt.Errorf("retry status %d is invalid", status)
		}
	}
	return nil
}

func validateHeaderPolicies(policies ...GatewayHeaderPolicy) error {
	for _, policy := range policies {
		for name, value := range policy.Set {
			if !validHeaderName(name) {
				return fmt.Errorf("header name cannot be empty")
			}
			if forbiddenGatewayHeader(name) {
				return fmt.Errorf("header %q cannot be rewritten", name)
			}
			if strings.ContainsAny(value, "\r\n") {
				return fmt.Errorf("header %q contains a line break", name)
			}
		}
		for name, values := range policy.Add {
			if !validHeaderName(name) {
				return fmt.Errorf("header name cannot be empty")
			}
			if forbiddenGatewayHeader(name) {
				return fmt.Errorf("header %q cannot be rewritten", name)
			}
			for _, value := range values {
				if strings.ContainsAny(value, "\r\n") {
					return fmt.Errorf("header %q contains a line break", name)
				}
			}
		}
		for _, name := range policy.Remove {
			if !validHeaderName(name) {
				return fmt.Errorf("header name cannot be empty")
			}
			if forbiddenGatewayHeader(name) {
				return fmt.Errorf("header %q cannot be rewritten", name)
			}
		}
	}
	return nil
}

func forbiddenGatewayHeader(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "x-balancer-") || strings.HasPrefix(lower, "x-forwarded-") {
		return true
	}
	switch lower {
	case "connection", "content-length", "forwarded", "host", "keep-alive", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade", "x-real-ip":
		return true
	default:
		return false
	}
}

func validHeaderName(name string) bool {
	return name != "" && http.CanonicalHeaderKey(name) != ""
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	const separators = "()<>@,;:\\\"/[]?={} \t"
	for _, character := range value {
		if character < 0x21 || character > 0x7e || strings.ContainsRune(separators, character) {
			return false
		}
	}
	return true
}

func validAuthority(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "/?#@\\\r\n\t ") {
		return false
	}
	parsed, err := url.Parse("http://" + value)
	if err != nil || parsed.Host != value || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" {
		return false
	}
	if strings.HasSuffix(value, ":") {
		return false
	}
	return true
}

func validHashKey(value string) bool {
	if value == "client_ip" || value == "host" || value == "path" {
		return true
	}
	parts := strings.SplitN(value, ":", 2)
	return len(parts) == 2 && (parts[0] == "header" || parts[0] == "cookie") && strings.TrimSpace(parts[1]) != ""
}

func validateHostnamePattern(host string) error {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || strings.ContainsAny(host, "/?#@") {
		return fmt.Errorf("invalid hostname pattern %q", host)
	}
	if strings.Contains(host, "*") && !strings.HasPrefix(host, "*.") {
		return fmt.Errorf("wildcard hostname %q must use a leading wildcard label", host)
	}
	return nil
}
