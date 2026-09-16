import demoConfig from './demo.gateway.json' with { type: 'json' }
import type { ClusterConfig, ClusterRuntime, GatewayConfig } from '../types.js'

export function createDemoConfig(): GatewayConfig {
  return cloneConfig(demoConfig as GatewayConfig)
}

export function cloneConfig(config: GatewayConfig): GatewayConfig {
  return JSON.parse(JSON.stringify(config)) as GatewayConfig
}

export function preserveRuntimeEndpointEnablement(cluster: ClusterConfig, runtime: ClusterRuntime): void {
  const runtimeByID = new Map(runtime.endpoints.map((endpoint) => [endpoint.id, endpoint]))
  cluster.endpoints = cluster.endpoints.map((endpoint) => {
    const current = runtimeByID.get(endpoint.id)
    return current ? { ...endpoint, disabled: !current.enabled } : endpoint
  })
}
