import type {
  EndpointMutation,
  GatewayConfig,
  GatewayStatus,
  RequestResult,
  RequestSpec,
  RateLimitUpdate,
  ValidationResult,
} from '../types.js'
import { GatewayAPIError, type GatewayAdapter } from './gateway.js'

const mutationHeaders = {
  'Content-Type': 'application/json',
  'X-Balancer-CSRF': '1',
}

export class LiveAdapter implements GatewayAdapter {
  readonly mode = 'live' as const

  async getStatus(): Promise<GatewayStatus> {
    return this.request<GatewayStatus>('/api/v1/status')
  }

  async getConfig(): Promise<GatewayConfig> {
    return this.request<GatewayConfig>('/api/v1/config')
  }

  async validateConfig(config: GatewayConfig): Promise<ValidationResult> {
    return this.request<ValidationResult>('/api/v1/config/validate', {
      method: 'POST',
      headers: mutationHeaders,
      body: JSON.stringify({ config }),
    })
  }

  async applyConfig(config: GatewayConfig, expectedRevision: number): Promise<GatewayStatus> {
    return this.request<GatewayStatus>('/api/v1/config', {
      method: 'PUT',
      headers: mutationHeaders,
      body: JSON.stringify({ config, expected_revision: expectedRevision }),
    })
  }

  async rollbackConfig(expectedRevision: number): Promise<GatewayStatus> {
    return this.request<GatewayStatus>('/api/v1/config/rollback', {
      method: 'POST',
      headers: mutationHeaders,
      body: JSON.stringify({ expected_revision: expectedRevision }),
    })
  }

  async mutateEndpoint(cluster: string, endpoint: string, mutation: EndpointMutation): Promise<GatewayStatus> {
    return this.request<GatewayStatus>(
      `/api/v1/clusters/${encodeURIComponent(cluster)}/endpoints/${encodeURIComponent(endpoint)}`,
      { method: 'PATCH', headers: mutationHeaders, body: JSON.stringify(mutation) },
    )
  }

  async sendRequest(request: RequestSpec): Promise<RequestResult> {
    return this.request<RequestResult>('/api/v1/request', {
      method: 'POST',
      headers: mutationHeaders,
      body: JSON.stringify(request),
    })
  }

  async updateRateLimit(update: RateLimitUpdate): Promise<GatewayStatus> {
    return this.request<GatewayStatus>('/api/v1/rate-limit', {
      method: 'PATCH',
      headers: mutationHeaders,
      body: JSON.stringify(update),
    })
  }

  async resetRateLimit(): Promise<GatewayStatus> {
    return this.request<GatewayStatus>('/api/v1/rate-limit/reset', {
      method: 'POST',
      headers: mutationHeaders,
      body: '{}',
    })
  }

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    let response: Response
    try {
      response = await fetch(path, { ...init, credentials: 'same-origin', headers: { Accept: 'application/json', ...init?.headers } })
    } catch (error) {
      throw new GatewayAPIError(error instanceof Error ? error.message : 'management API is unavailable', 0)
    }
    const text = await response.text()
    let payload: unknown
    try {
      payload = text ? (JSON.parse(text) as unknown) : undefined
    } catch {
      payload = text
    }
    if (!response.ok) {
      const message =
        typeof payload === 'object' && payload !== null && 'error' in payload
          ? String((payload as { error: unknown }).error)
          : `${response.status} ${response.statusText}`
      throw new GatewayAPIError(message, response.status, payload)
    }
    return payload as T
  }
}
