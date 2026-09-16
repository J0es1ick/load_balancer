export type AppMode = 'demo' | 'live'
export type SectionID = 'overview' | 'request' | 'routes' | 'clusters' | 'config' | 'guide'
export type HTTPMethod = 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'OPTIONS'

export interface HeaderMatch {
  name: string
  exact?: string
  present?: boolean
}

export interface RouteMatch {
  hosts?: string[]
  path_exact?: string
  path_prefix?: string
  methods?: string[]
  headers?: HeaderMatch[]
}

export interface WeightedCluster {
  cluster: string
  weight: number
}

export interface RedirectAction {
  scheme?: string
  host?: string
  path?: string
  status_code: number
  preserve_query?: boolean
}

export interface RouteAction {
  cluster?: string
  weighted_clusters?: WeightedCluster[]
  redirect?: RedirectAction
  rewrite_prefix?: string
  preserve_host?: boolean
  host_rewrite?: string
}

export interface RetryPolicy {
  max_attempts: number
  per_try_timeout: string
  body_limit: number
  methods: string[]
  statuses: number[]
  budget_capacity: number
  budget_refill_per_second: number
}

export interface RouteTimeouts {
  request?: string
  per_try?: string
}

export interface HeaderPolicy {
  set?: Record<string, string>
  add?: Record<string, string[]>
  remove?: string[]
}

export interface RouteRateLimit {
  enabled: boolean
  capacity: number
  refill_per_second: number
}

export interface RouteConfig {
  id: string
  listener: string
  priority: number
  match: RouteMatch
  action: RouteAction
  retry?: RetryPolicy
  timeouts?: RouteTimeouts
  max_request_body_bytes?: number
  request_headers?: HeaderPolicy
  response_headers?: HeaderPolicy
  rate_limit?: RouteRateLimit
}

export interface ListenerTLSConfig {
  cert_file?: string
  key_file?: string
  ca_file?: string
  require_client_cert?: boolean
}

export interface ListenerConfig {
  id: string
  address: string
  protocol: 'http1' | 'h2c' | 'http2'
  hostnames?: string[]
  default?: boolean
  tls?: ListenerTLSConfig
}

export interface EndpointConfig {
  id: string
  url: string
  weight?: number
  disabled?: boolean
}

export interface HealthConfig {
  enabled: boolean
  mode: 'tcp' | 'http' | 'https'
  path: string
  interval: string
  timeout: string
  failure_threshold: number
  success_threshold: number
  max_concurrency: number
  jitter: string
  cooldown: string
  expected_statuses: number[]
  slow_start: string
  slow_start_minimum_percent: number
}

export interface TransportConfig {
  protocol: 'http1' | 'http2' | 'h2c' | 'auto'
  dial_timeout: string
  tls_handshake_timeout: string
  response_header_timeout: string
  expect_continue_timeout: string
  idle_conn_timeout: string
  max_idle_conns: number
  max_idle_conns_per_host: number
  max_conns_per_host: number
  max_concurrent_requests?: number
  max_response_header_bytes: number
}

export interface UpstreamTLSConfig {
  enabled: boolean
  ca_file?: string
  server_name?: string
  client_cert_file?: string
  client_key_file?: string
}

export interface DiscoveryConfig {
  type: 'static' | 'dns' | 'kubernetes'
  hostname?: string
  port?: number
  scheme?: string
  namespace?: string
  service?: string
  api_server?: string
  token_file?: string
  ca_file?: string
  refresh_interval?: string
  stale_after?: string
}

export type Strategy = 'round_robin' | 'weighted_round_robin' | 'least_request' | 'rendezvous'

export interface ClusterConfig {
  id: string
  strategy: Strategy
  hash_key?: string
  endpoints: EndpointConfig[]
  health: HealthConfig
  retry?: RetryPolicy
  transport: TransportConfig
  tls: UpstreamTLSConfig
  discovery?: DiscoveryConfig
}

export interface GatewayConfig {
  apiVersion: 'proxy/v1'
  history_limit: number
  listeners: ListenerConfig[]
  routes: RouteConfig[]
  clusters: ClusterConfig[]
}

export interface EndpointRuntime {
  id: string
  url: string
  weight: number
  enabled: boolean
  draining: boolean
  healthy: boolean
  available: boolean
  inflight: number
  requests: number
  active_requests?: number
  success_rate?: number
  circuit_open?: boolean
  circuit_state?: string
  ejected?: boolean
  passive_failures?: number
  slow_start_percent?: number
}

export interface DiscoveryRuntime {
  type: DiscoveryConfig['type']
  last_update?: string
  stale?: boolean
  error?: string
}

export interface RetryRuntime {
  max_attempts: number
  per_try_timeout: string
  methods: string[]
  statuses: number[]
  budget: {
    capacity: number
    tokens: number
    refill_per_second: number
  }
}

export interface ClusterStats {
  requests: number
  errors: number
  inflight: number
  p95_ms?: number
}

export interface ClusterRuntime {
  id: string
  strategy: Strategy
  endpoints: EndpointRuntime[]
  discovery: DiscoveryRuntime
  tls: { enabled: boolean; server_name?: string }
  retry?: RetryRuntime
  stats?: ClusterStats
}

export interface GatewayStats {
  requests: number
  route_not_found: number
  redirects: number
  rate_limited?: number
  body_rejected?: number
  apply_success: number
  apply_failures: number
}

export interface GatewayRuntime {
  revision: number
  hash: string
  previous_revision: number | null
  applied_at: string
  last_error?: string
  listeners: ListenerRuntime[]
  routes: RouteRuntime[]
  clusters: ClusterRuntime[]
  stats: GatewayStats
}

export interface ListenerRuntime {
  id: string
  address: string
  protocol: string
  tls: boolean
}

export interface RouteRuntime {
  id: string
  listener?: string
  priority: number
}

export interface RateLimitStatus {
  enabled: boolean
  capacity?: number
  refill_per_second?: number
  remaining?: number
  failure_mode?: string
  local_buckets?: number
  local_evictions?: number
}

export interface RateLimitUpdate {
  enabled: boolean
  capacity: number
  refill_per_second: number
  failure_mode: 'fail-open' | 'fail-closed' | 'local-fallback'
}

export interface StorageStatus {
  type: string
  healthy: boolean
  degraded?: boolean
  error?: string
}

export interface ProtectionStatus {
  inflight?: number
  max_concurrent_requests?: number
  queued?: number
  rejected?: number
  overload?: {
    inflight: number
    max_concurrent_requests: number
    queue_timeout: string
  }
}

export interface GatewayStatus {
  mode: AppMode
  instance_id: string
  principal: {
    name: string
    role: 'viewer' | 'operator' | 'admin'
  }
  runtime_mutations_enabled: boolean
  gateway: GatewayRuntime
  rate_limit?: RateLimitStatus
  storage?: StorageStatus | string
  protection?: ProtectionStatus
}

export interface RequestSpec {
  method: HTTPMethod
  host: string
  path: string
  headers: Record<string, string>
  body: string
}

export interface RequestResult {
  status: number
  headers: Record<string, string>
  body: string
  duration_ms: number
  truncated: boolean
  route?: string
  cluster?: string
  backend?: string
  attempts: number
}

export interface ValidationIssue {
  path: string
  message: string
}

export interface ValidationResult {
  valid: boolean
  errors?: ValidationIssue[]
}

export interface EndpointMutation {
  enabled?: boolean
  draining?: boolean
}

export interface ConsoleEvent {
  id: number
  time: string
  kind: 'request' | 'config' | 'health' | 'system' | 'error'
  title: string
  detail: string
  status?: number
}
