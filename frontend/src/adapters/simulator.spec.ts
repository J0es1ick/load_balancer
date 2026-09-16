import { createDemoConfig, preserveRuntimeEndpointEnablement } from '../config/defaults.js'
import type { RequestSpec } from '../types.js'
import { GatewaySimulator, validateGatewayConfig } from './simulator.js'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

const request: RequestSpec = { method: 'GET', host: 'localhost', path: '/api/users', headers: {}, body: '' }

function roundRobinUsesDifferentHealthyEndpoints(): void {
  const simulator = new GatewaySimulator(createDemoConfig())
  const first = simulator.request(request)
  const second = simulator.request(request)
  assert(first.status === 200 && second.status === 200, 'healthy requests should succeed')
  assert(first.route === 'api', 'the most specific route should match')
  assert(first.backend === 'backend-1', 'round-robin should start with the first endpoint')
  assert(second.backend === 'backend-2', 'round-robin should advance to the next endpoint')
}

function drainingEndpointIsNotSelected(): void {
  const simulator = new GatewaySimulator(createDemoConfig())
  simulator.mutateEndpoint('primary', 'backend-1', { draining: true })
  for (let index = 0; index < 8; index += 1) {
    assert(simulator.request(request).backend !== 'backend-1', 'draining endpoint accepted a new request')
  }
}

function configValidationFindsBrokenReferences(): void {
  const config = createDemoConfig()
  config.routes[0].action.cluster = 'missing'
  const validation = validateGatewayConfig(config)
  assert(!validation.valid, 'unknown cluster reference must be rejected')
  assert(validation.errors?.some((issue) => issue.path.includes('routes[0].action')), 'validation path is missing')
}

function applyUsesOptimisticRevisionAndRollback(): void {
  const simulator = new GatewaySimulator(createDemoConfig())
  const next = simulator.getConfig()
  next.routes[0].priority = 40
  const applied = simulator.apply(next, 1)
  assert(applied.gateway.revision === 2, 'apply should increment revision')
  let conflicted = false
  try {
    simulator.apply(next, 1)
  } catch {
    conflicted = true
  }
  assert(conflicted, 'stale revision should be rejected')
  const rolledBack = simulator.rollback(2)
  assert(rolledBack.gateway.revision === 3, 'rollback should create a new revision')
  assert(simulator.getConfig().routes[0].priority === 30, 'rollback should restore previous config')
}

function runtimeRateLimitCanBeUpdatedAndReset(): void {
  const simulator = new GatewaySimulator(createDemoConfig())
  let status = simulator.updateRateLimit({ enabled: true, capacity: 2, refill_per_second: 0.001, failure_mode: 'fail-closed' })
  assert(status.rate_limit?.capacity === 2 && status.rate_limit.failure_mode === 'fail-closed', 'runtime rate-limit update was not reflected')
  assert(simulator.request(request).status === 200, 'first token should be available')
  assert(simulator.request(request).status === 200, 'second token should be available')
  assert(simulator.request(request).status === 429, 'empty bucket should reject the request')
  status = simulator.resetRateLimit()
  assert(status.rate_limit?.remaining === 2, 'reset should restore bucket capacity')
  assert(simulator.request(request).status === 200, 'request should succeed after reset')
}

function strategyApplyPreservesRuntimeEndpointEnablement(): void {
  const simulator = new GatewaySimulator(createDemoConfig())
  simulator.mutateEndpoint('primary', 'backend-3', { enabled: true })
  const config = simulator.getConfig()
  const cluster = config.clusters.find((item) => item.id === 'primary')
  const runtime = simulator.getStatus().gateway.clusters.find((item) => item.id === 'primary')
  assert(cluster && runtime, 'primary cluster should exist in config and runtime status')
  preserveRuntimeEndpointEnablement(cluster, runtime)
  assert(cluster.endpoints.find((item) => item.id === 'backend-3')?.disabled === false, 'runtime-enabled endpoint was reset by config mutation')
  assert(cluster.endpoints.find((item) => item.id === 'backend-4')?.disabled === true, 'runtime-disabled endpoint was unexpectedly enabled')
}

roundRobinUsesDifferentHealthyEndpoints()
drainingEndpointIsNotSelected()
configValidationFindsBrokenReferences()
applyUsesOptimisticRevisionAndRollback()
runtimeRateLimitCanBeUpdatedAndReset()
strategyApplyPreservesRuntimeEndpointEnablement()

console.log('simulator contract tests passed')
