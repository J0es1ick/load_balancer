import { cloneConfig } from '../config/defaults.js'
import type {
  ClusterConfig,
  ClusterRuntime,
  EndpointMutation,
  EndpointRuntime,
  GatewayConfig,
  GatewayRuntime,
  GatewayStatus,
  HeaderMatch,
  RequestResult,
  RequestSpec,
  RateLimitUpdate,
  RouteConfig,
  ValidationIssue,
  ValidationResult,
} from '../types.js'

interface RateBucket {
  tokens: number
  updatedAt: number
}

interface RuntimeCounters {
  requests: number
  errors: number
  inflight: number
}

function shortHash(value: string): string {
  let hash = 2_166_136_261
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index)
    hash = Math.imul(hash, 16_777_619)
  }
  return (hash >>> 0).toString(16).padStart(8, '0')
}

function normalizedHost(host: string): string {
  return host.trim().toLowerCase().replace(/:\d+$/, '').replace(/\.$/, '')
}

function hostMatches(pattern: string, host: string): boolean {
  const normalized = normalizedHost(pattern)
  if (normalized === '*' || normalized === host) return true
  return normalized.startsWith('*.') && host.endsWith(normalized.slice(1))
}

function headerMatches(match: HeaderMatch, headers: Record<string, string>): boolean {
  const entry = Object.entries(headers).find(([name]) => name.toLowerCase() === match.name.toLowerCase())
  if (match.present === true && !entry) return false
  if (match.present === false && entry) return false
  return match.exact === undefined || entry?.[1] === match.exact
}

function requestURL(path: string): URL {
  return new URL(path, 'http://browser-simulator')
}

function pathPrefixMatches(path: string, prefix: string): boolean {
  if (prefix === '/') return path.startsWith('/')
  if (!path.startsWith(prefix)) return false
  return path.length === prefix.length || prefix.endsWith('/') || path[prefix.length] === '/'
}

function routeMatches(route: RouteConfig, request: RequestSpec): boolean {
  const match = route.match
  const host = normalizedHost(request.host)
  const path = requestURL(request.path).pathname
  if (match.hosts?.length && !match.hosts.some((pattern) => hostMatches(pattern, host))) return false
  if (match.path_exact !== undefined && path !== match.path_exact) return false
  if (match.path_prefix !== undefined && !pathPrefixMatches(path, match.path_prefix)) return false
  if (match.methods?.length && !match.methods.some((method) => method.toUpperCase() === request.method.toUpperCase())) return false
  return !match.headers?.length || match.headers.every((header) => headerMatches(header, request.headers))
}

function orderedRoutes(routes: RouteConfig[]): RouteConfig[] {
  return routes
    .map((route, order) => ({ route, order }))
    .sort((left, right) => {
      if (left.route.priority !== right.route.priority) return right.route.priority - left.route.priority
      const leftExact = left.route.match.path_exact !== undefined
      const rightExact = right.route.match.path_exact !== undefined
      if (leftExact !== rightExact) return leftExact ? -1 : 1
      const prefixDifference = (right.route.match.path_prefix?.length ?? 0) - (left.route.match.path_prefix?.length ?? 0)
      return prefixDifference || left.order - right.order
    })
    .map(({ route }) => route)
}

function findHeader(headers: Record<string, string>, name: string): string {
  return Object.entries(headers).find(([candidate]) => candidate.toLowerCase() === name.toLowerCase())?.[1] ?? ''
}

function rendezvousValue(cluster: ClusterConfig, request: RequestSpec): string {
  const [kind, name = ''] = (cluster.hash_key ?? '').split(':', 2)
  if (kind === 'client_ip') return 'browser-client'
  if (kind === 'host') return request.host
  if (kind === 'path') return requestURL(request.path).pathname
  if (kind === 'header') return findHeader(request.headers, name)
  if (kind === 'cookie') {
    const cookie = findHeader(request.headers, 'cookie')
      .split(';')
      .map((part) => part.trim().split('=', 2))
      .find(([cookieName]) => cookieName === name)
    return cookie?.[1] ?? ''
  }
  return ''
}

function rewrittenPath(route: RouteConfig, path: string): string {
  const source = requestURL(path)
  const matched = route.match.path_prefix
  const replacement = route.action.rewrite_prefix
  if (!matched || replacement === undefined || !pathPrefixMatches(source.pathname, matched)) return `${source.pathname}${source.search}`
  const suffix = source.pathname.slice(matched.length)
  const left = replacement.startsWith('/') ? replacement : `/${replacement}`
  const joined = `${left.replace(/\/$/, '')}/${suffix.replace(/^\//, '')}` || '/'
  return `${joined}${source.search}`
}

function validateDuration(value: string | undefined): boolean {
  return value === undefined || /^\d+(?:\.\d+)?(?:ms|s|m|h)$/.test(value)
}

export function validateGatewayConfig(config: GatewayConfig): ValidationResult {
  const errors: ValidationIssue[] = []
  if (config.apiVersion !== 'proxy/v1') errors.push({ path: 'apiVersion', message: 'expected proxy/v1' })
  if (!Number.isInteger(config.history_limit) || config.history_limit < 1) {
    errors.push({ path: 'history_limit', message: 'must be a positive integer' })
  }

  const listenerIDs = new Set<string>()
  config.listeners.forEach((listener, index) => {
    if (!listener.id) errors.push({ path: `listeners[${index}].id`, message: 'is required' })
    if (listenerIDs.has(listener.id)) errors.push({ path: `listeners[${index}].id`, message: 'must be unique' })
    listenerIDs.add(listener.id)
    if (!listener.address) errors.push({ path: `listeners[${index}].address`, message: 'is required' })
  })

  const clusterIDs = new Set<string>()
  config.clusters.forEach((cluster, index) => {
    if (clusterIDs.has(cluster.id)) errors.push({ path: `clusters[${index}].id`, message: 'must be unique' })
    clusterIDs.add(cluster.id)
    if (!cluster.endpoints.length && (!cluster.discovery || cluster.discovery.type === 'static')) {
      errors.push({ path: `clusters[${index}].endpoints`, message: 'static cluster needs at least one endpoint' })
    }
    if (cluster.strategy === 'rendezvous' && !/^(client_ip|host|path|header:[^:]+|cookie:[^:]+)$/.test(cluster.hash_key ?? '')) {
      errors.push({ path: `clusters[${index}].hash_key`, message: 'rendezvous needs client_ip, host, path, header:NAME or cookie:NAME' })
    }
    const endpointIDs = new Set<string>()
    cluster.endpoints.forEach((endpoint, endpointIndex) => {
      if (endpointIDs.has(endpoint.id)) {
        errors.push({ path: `clusters[${index}].endpoints[${endpointIndex}].id`, message: 'must be unique in cluster' })
      }
      endpointIDs.add(endpoint.id)
      try {
        const parsed = new URL(endpoint.url)
        if (!['http:', 'https:'].includes(parsed.protocol)) throw new Error('protocol')
      } catch {
        errors.push({ path: `clusters[${index}].endpoints[${endpointIndex}].url`, message: 'must be an HTTP URL' })
      }
    })
    if (!validateDuration(cluster.health.interval) || !validateDuration(cluster.health.timeout)) {
      errors.push({ path: `clusters[${index}].health`, message: 'contains an invalid duration' })
    }
  })

  const routeIDs = new Set<string>()
  config.routes.forEach((route, index) => {
    if (routeIDs.has(route.id)) errors.push({ path: `routes[${index}].id`, message: 'must be unique' })
    routeIDs.add(route.id)
    if (!listenerIDs.has(route.listener)) {
      errors.push({ path: `routes[${index}].listener`, message: `unknown listener ${route.listener}` })
    }
    const targets = [route.action.cluster, ...(route.action.weighted_clusters ?? []).map((item) => item.cluster)].filter(
      (value): value is string => Boolean(value),
    )
    if (!route.action.redirect && targets.length === 0) {
      errors.push({ path: `routes[${index}].action`, message: 'needs cluster, weighted_clusters or redirect' })
    }
    targets.forEach((target) => {
      if (!clusterIDs.has(target)) errors.push({ path: `routes[${index}].action`, message: `unknown cluster ${target}` })
    })
    if (route.timeouts && (!validateDuration(route.timeouts.request) || !validateDuration(route.timeouts.per_try))) {
      errors.push({ path: `routes[${index}].timeouts`, message: 'contains an invalid duration' })
    }
  })
  return { valid: errors.length === 0, errors: errors.length ? errors : undefined }
}

export class GatewaySimulator {
  private config: GatewayConfig
  private previousConfig: GatewayConfig | null = null
  private revision = 1
  private previousRevision: number | null = null
  private appliedAt = new Date().toISOString()
  private lastError = ''
  private readonly endpoints = new Map<string, EndpointRuntime>()
  private readonly cursors = new Map<string, number>()
  private readonly clusterCounters = new Map<string, RuntimeCounters>()
  private readonly rateBuckets = new Map<string, RateBucket>()
  private rateLimit: RateLimitUpdate & { remaining: number; updatedAt: number } = {
    enabled: true,
    capacity: 120,
    refill_per_second: 60,
    failure_mode: 'local-fallback',
    remaining: 118,
    updatedAt: Date.now(),
  }
  private requestSequence = 0
  private stats: GatewayRuntime['stats'] = {
    requests: 0,
    route_not_found: 0,
    redirects: 0,
    rate_limited: 0,
    body_rejected: 0,
    apply_success: 0,
    apply_failures: 0,
  }

  constructor(config: GatewayConfig) {
    this.config = cloneConfig(config)
    this.reconcileEndpoints()
  }

  getConfig(): GatewayConfig {
    return cloneConfig(this.config)
  }

  getStatus(): GatewayStatus {
    const gateway: GatewayRuntime = {
      revision: this.revision,
      hash: shortHash(JSON.stringify(this.config)),
      previous_revision: this.previousRevision,
      applied_at: this.appliedAt,
      last_error: this.lastError || undefined,
      listeners: this.config.listeners.map((listener) => ({
        id: listener.id,
        address: listener.address,
        protocol: listener.protocol,
        tls: Boolean(listener.tls?.cert_file && listener.tls.key_file),
      })),
      routes: this.config.routes.map((route) => ({ id: route.id, listener: route.listener, priority: route.priority })),
      clusters: this.config.clusters.map((cluster) => this.clusterRuntime(cluster)),
      stats: { ...this.stats },
    }
    return {
      mode: 'demo',
      instance_id: 'browser-simulator',
      principal: { name: 'browser-owner', role: 'admin' },
      runtime_mutations_enabled: true,
      gateway,
      rate_limit: {
        enabled: this.rateLimit.enabled,
        capacity: this.rateLimit.capacity,
        refill_per_second: this.rateLimit.refill_per_second,
        remaining: Math.max(0, this.rateLimit.remaining),
        failure_mode: this.rateLimit.failure_mode,
        local_buckets: 1,
        local_evictions: 0,
      },
      storage: { type: 'browser-memory', healthy: true },
      protection: { inflight: 0, max_concurrent_requests: 512, queued: 0, rejected: 0 },
    }
  }

  validate(config: GatewayConfig): ValidationResult {
    return validateGatewayConfig(config)
  }

  apply(config: GatewayConfig, expectedRevision: number): GatewayStatus {
    if (expectedRevision !== this.revision) throw new Error(`revision conflict: current revision is ${this.revision}`)
    const validation = validateGatewayConfig(config)
    if (!validation.valid) {
      this.lastError = validation.errors?.map((issue) => `${issue.path}: ${issue.message}`).join('; ') ?? 'invalid config'
      this.stats.apply_failures += 1
      throw new Error(this.lastError)
    }
    this.previousConfig = this.config
    this.previousRevision = this.revision
    this.config = cloneConfig(config)
    this.revision += 1
    this.appliedAt = new Date().toISOString()
    this.lastError = ''
    this.stats.apply_success += 1
    this.reconcileEndpoints()
    return this.getStatus()
  }

  rollback(expectedRevision: number): GatewayStatus {
    if (expectedRevision !== this.revision) throw new Error(`revision conflict: current revision is ${this.revision}`)
    if (!this.previousConfig) throw new Error('no previous configuration to roll back to')
    const current = this.config
    this.config = this.previousConfig
    this.previousConfig = current
    this.previousRevision = this.revision
    this.revision += 1
    this.appliedAt = new Date().toISOString()
    this.stats.apply_success += 1
    this.reconcileEndpoints()
    return this.getStatus()
  }

  mutateEndpoint(clusterID: string, endpointID: string, mutation: EndpointMutation): GatewayStatus {
    const key = `${clusterID}/${endpointID}`
    const endpoint = this.endpoints.get(key)
    if (!endpoint) throw new Error(`endpoint ${key} not found`)
    if (mutation.enabled !== undefined) endpoint.enabled = mutation.enabled
    if (mutation.draining !== undefined) endpoint.draining = mutation.draining
    endpoint.available = endpoint.enabled && endpoint.healthy && !endpoint.draining && !endpoint.circuit_open
    return this.getStatus()
  }

  updateRateLimit(update: RateLimitUpdate): GatewayStatus {
    if (!Number.isInteger(update.capacity) || update.capacity < 1) throw new Error('rate limit capacity must be a positive integer')
    if (!Number.isFinite(update.refill_per_second) || update.refill_per_second <= 0) {
      throw new Error('rate limit refill_per_second must be positive')
    }
    if (!['fail-open', 'fail-closed', 'local-fallback'].includes(update.failure_mode)) {
      throw new Error('unsupported rate limit failure_mode')
    }
    this.rateLimit = { ...update, remaining: Math.min(this.rateLimit.remaining, update.capacity), updatedAt: Date.now() }
    return this.getStatus()
  }

  resetRateLimit(): GatewayStatus {
    this.rateLimit.remaining = this.rateLimit.capacity
    this.rateLimit.updatedAt = Date.now()
    return this.getStatus()
  }

  request(request: RequestSpec): RequestResult {
    this.requestSequence += 1
    this.stats.requests += 1
    if (!this.takeGlobalToken()) {
      this.stats.rate_limited = (this.stats.rate_limited ?? 0) + 1
      return this.result(429, 'global rate limit exceeded', request, undefined, undefined, 1)
    }
    const route = orderedRoutes(this.config.routes).find((candidate) => routeMatches(candidate, request))
    if (!route) {
      this.stats.route_not_found += 1
      return this.result(404, 'route not found', request, undefined, undefined, 1)
    }
    if (route.rate_limit?.enabled && !this.takeRouteToken(route)) {
      this.stats.rate_limited = (this.stats.rate_limited ?? 0) + 1
      return this.result(429, 'route rate limit exceeded', request, route, undefined, 1)
    }
    if (route.max_request_body_bytes && new TextEncoder().encode(request.body).length > route.max_request_body_bytes) {
      this.stats.body_rejected = (this.stats.body_rejected ?? 0) + 1
      return this.result(413, 'request body exceeds route limit', request, route, undefined, 1)
    }
    if (route.action.redirect) {
      this.stats.redirects += 1
      const redirect = route.action.redirect
      const source = requestURL(request.path)
      const targetPath = redirect.path || source.pathname
      const query = redirect.preserve_query ? source.search : ''
      const location = `${redirect.scheme ?? 'http'}://${redirect.host ?? request.host}${targetPath}${query}`
      const response = this.result(redirect.status_code, '', request, route, undefined, 1)
      response.headers.Location = location
      return response
    }
    const cluster = this.selectCluster(route)
    if (!cluster) return this.result(503, 'configured cluster is unavailable', request, route, undefined, 1)
    const endpoint = this.selectEndpoint(cluster, request)
    if (!endpoint) {
      this.counter(cluster.id).errors += 1
      return this.result(503, 'no healthy endpoint', request, route, cluster, 1)
    }
    endpoint.requests += 1
    const counters = this.counter(cluster.id)
    counters.requests += 1
    const result = this.result(
      200,
      JSON.stringify(
        {
          ok: true,
          route: route.id,
          cluster: cluster.id,
          backend: endpoint.id,
          method: request.method,
          path: request.path,
          upstream_path: rewrittenPath(route, request.path),
        },
        null,
        2,
      ),
      request,
      route,
      cluster,
      1,
      endpoint,
    )
    result.headers['Content-Type'] = 'application/json'
    return result
  }

  private result(
    status: number,
    body: string,
    request: RequestSpec,
    route?: RouteConfig,
    cluster?: ClusterConfig,
    attempts = 1,
    endpoint?: EndpointRuntime,
  ): RequestResult {
    const duration = 3 + ((this.requestSequence * 7 + request.path.length * 3 + (endpoint?.requests ?? 0)) % 24)
    return {
      status,
      headers: endpoint ? { 'X-Balancer-Backend': endpoint.id, 'X-Balancer-Attempts': String(attempts) } : {},
      body,
      duration_ms: duration,
      truncated: false,
      route: route?.id,
      cluster: cluster?.id,
      backend: endpoint?.id,
      attempts,
    }
  }

  private takeRouteToken(route: RouteConfig): boolean {
    const policy = route.rate_limit
    if (!policy) return true
    const now = Date.now()
    const bucket = this.rateBuckets.get(route.id) ?? { tokens: policy.capacity, updatedAt: now }
    const elapsed = Math.max(0, now - bucket.updatedAt) / 1000
    bucket.tokens = Math.min(policy.capacity, bucket.tokens + elapsed * policy.refill_per_second)
    bucket.updatedAt = now
    if (bucket.tokens < 1) {
      this.rateBuckets.set(route.id, bucket)
      return false
    }
    bucket.tokens -= 1
    this.rateBuckets.set(route.id, bucket)
    return true
  }

  private takeGlobalToken(): boolean {
    if (!this.rateLimit.enabled) return true
    const now = Date.now()
    const elapsed = Math.max(0, now - this.rateLimit.updatedAt) / 1000
    this.rateLimit.remaining = Math.min(
      this.rateLimit.capacity,
      this.rateLimit.remaining + elapsed * this.rateLimit.refill_per_second,
    )
    this.rateLimit.updatedAt = now
    if (this.rateLimit.remaining < 1) return false
    this.rateLimit.remaining -= 1
    return true
  }

  private selectCluster(route: RouteConfig): ClusterConfig | undefined {
    if (route.action.cluster) return this.config.clusters.find((cluster) => cluster.id === route.action.cluster)
    const weighted = route.action.weighted_clusters ?? []
    const total = weighted.reduce((sum, target) => sum + Math.max(0, target.weight), 0)
    if (!total) return undefined
    let point = (this.requestSequence - 1) % total
    const selected = weighted.find((target) => {
      point -= Math.max(0, target.weight)
      return point < 0
    })
    return this.config.clusters.find((cluster) => cluster.id === selected?.cluster)
  }

  private selectEndpoint(cluster: ClusterConfig, request: RequestSpec): EndpointRuntime | undefined {
    const available = cluster.endpoints
      .map((endpoint) => this.endpoints.get(`${cluster.id}/${endpoint.id}`))
      .filter((endpoint): endpoint is EndpointRuntime => Boolean(endpoint?.available))
    if (!available.length) return undefined
    if (cluster.strategy === 'least_request') {
      return [...available].sort((left, right) => left.inflight - right.inflight || left.requests - right.requests)[0]
    }
    if (cluster.strategy === 'rendezvous') {
      const key = rendezvousValue(cluster, request)
      return [...available].sort(
        (left, right) => Number.parseInt(shortHash(`${key}\u0000${right.id}`), 16) - Number.parseInt(shortHash(`${key}\u0000${left.id}`), 16),
      )[0]
    }
    const sequence =
      cluster.strategy === 'weighted_round_robin'
        ? available.flatMap((endpoint) => Array.from({ length: Math.max(1, endpoint.weight) }, () => endpoint))
        : available
    const cursor = this.cursors.get(cluster.id) ?? 0
    this.cursors.set(cluster.id, cursor + 1)
    return sequence[cursor % sequence.length]
  }

  private reconcileEndpoints(): void {
    const nextKeys = new Set<string>()
    this.config.clusters.forEach((cluster) => {
      this.counter(cluster.id)
      cluster.endpoints.forEach((endpoint) => {
        const key = `${cluster.id}/${endpoint.id}`
        nextKeys.add(key)
        const previous = this.endpoints.get(key)
        const enabled = previous?.enabled ?? !endpoint.disabled
        const healthy = previous?.healthy ?? true
        const draining = previous?.draining ?? false
        this.endpoints.set(key, {
          id: endpoint.id,
          url: endpoint.url,
          weight: endpoint.weight ?? 1,
          enabled,
          draining,
          healthy,
          available: enabled && healthy && !draining,
          inflight: previous?.inflight ?? 0,
          requests: previous?.requests ?? 0,
          success_rate: previous?.success_rate ?? 100,
          circuit_open: previous?.circuit_open ?? false,
          slow_start_percent: previous?.slow_start_percent ?? 100,
        })
      })
    })
    ;[...this.endpoints.keys()].forEach((key) => {
      if (!nextKeys.has(key)) this.endpoints.delete(key)
    })
  }

  private clusterRuntime(cluster: ClusterConfig): ClusterRuntime {
    const retry = cluster.retry
    return {
      id: cluster.id,
      strategy: cluster.strategy,
      endpoints: cluster.endpoints
        .map((endpoint) => this.endpoints.get(`${cluster.id}/${endpoint.id}`))
        .filter((endpoint): endpoint is EndpointRuntime => Boolean(endpoint))
        .map((endpoint) => ({ ...endpoint })),
      discovery: {
        type: cluster.discovery?.type ?? 'static',
        last_update: this.appliedAt,
        stale: false,
      },
      tls: { enabled: cluster.tls.enabled, server_name: cluster.tls.server_name },
      retry: retry
        ? {
            max_attempts: retry.max_attempts,
            per_try_timeout: retry.per_try_timeout,
            methods: [...retry.methods],
            statuses: [...retry.statuses],
            budget: {
              capacity: retry.budget_capacity,
              tokens: Math.max(0, retry.budget_capacity - (this.requestSequence % 7)),
              refill_per_second: retry.budget_refill_per_second,
            },
          }
        : undefined,
      stats: { ...this.counter(cluster.id), p95_ms: 18 },
    }
  }

  private counter(clusterID: string): RuntimeCounters {
    let counter = this.clusterCounters.get(clusterID)
    if (!counter) {
      counter = { requests: 0, errors: 0, inflight: 0 }
      this.clusterCounters.set(clusterID, counter)
    }
    return counter
  }
}
