import type {
  EndpointMutation,
  GatewayConfig,
  GatewayStatus,
  RequestResult,
  RequestSpec,
  RateLimitUpdate,
  ValidationResult,
} from '../types.js'

export interface GatewayAdapter {
  readonly mode: 'demo' | 'live'
  getStatus(): Promise<GatewayStatus>
  getConfig(): Promise<GatewayConfig>
  validateConfig(config: GatewayConfig): Promise<ValidationResult>
  applyConfig(config: GatewayConfig, expectedRevision: number): Promise<GatewayStatus>
  rollbackConfig(expectedRevision: number): Promise<GatewayStatus>
  mutateEndpoint(cluster: string, endpoint: string, mutation: EndpointMutation): Promise<GatewayStatus>
  sendRequest(request: RequestSpec): Promise<RequestResult>
  updateRateLimit(update: RateLimitUpdate): Promise<GatewayStatus>
  resetRateLimit(): Promise<GatewayStatus>
  setPersistence?(enabled: boolean): void
  persistenceEnabled?(): boolean
}

export class GatewayAPIError extends Error {
  readonly status: number
  readonly details?: unknown

  constructor(message: string, status: number, details?: unknown) {
    super(message)
    this.name = 'GatewayAPIError'
    this.status = status
    this.details = details
  }
}
