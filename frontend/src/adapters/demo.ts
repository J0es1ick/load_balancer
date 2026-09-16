import { cloneConfig, createDemoConfig } from '../config/defaults.js'
import type {
  EndpointMutation,
  GatewayConfig,
  GatewayStatus,
  RequestResult,
  RequestSpec,
  RateLimitUpdate,
  ValidationResult,
} from '../types.js'
import type { GatewayAdapter } from './gateway.js'
import { GatewaySimulator, validateGatewayConfig } from './simulator.js'

const configKey = 'proxy-console.demo-config.v1'
const persistenceKey = 'proxy-console.demo-persist'

function storedConfig(storage: Storage | null): GatewayConfig {
  if (storage?.getItem(persistenceKey) !== 'true') return createDemoConfig()
  try {
    const parsed = JSON.parse(storage.getItem(configKey) ?? '') as GatewayConfig
    return validateGatewayConfig(parsed).valid ? parsed : createDemoConfig()
  } catch {
    return createDemoConfig()
  }
}

export class DemoAdapter implements GatewayAdapter {
  readonly mode = 'demo' as const
  private readonly simulator: GatewaySimulator
  private persistent: boolean

  constructor(private readonly storage: Storage | null = window.localStorage) {
    this.simulator = new GatewaySimulator(storedConfig(storage))
    this.persistent = storage?.getItem(persistenceKey) === 'true'
  }

  async getStatus(): Promise<GatewayStatus> {
    return this.simulator.getStatus()
  }

  async getConfig(): Promise<GatewayConfig> {
    return this.simulator.getConfig()
  }

  async validateConfig(config: GatewayConfig): Promise<ValidationResult> {
    return this.simulator.validate(config)
  }

  async applyConfig(config: GatewayConfig, expectedRevision: number): Promise<GatewayStatus> {
    const status = this.simulator.apply(cloneConfig(config), expectedRevision)
    this.persist()
    return status
  }

  async rollbackConfig(expectedRevision: number): Promise<GatewayStatus> {
    const status = this.simulator.rollback(expectedRevision)
    this.persist()
    return status
  }

  async mutateEndpoint(cluster: string, endpoint: string, mutation: EndpointMutation): Promise<GatewayStatus> {
    return this.simulator.mutateEndpoint(cluster, endpoint, mutation)
  }

  async sendRequest(request: RequestSpec): Promise<RequestResult> {
    await new Promise((resolve) => window.setTimeout(resolve, 80))
    return this.simulator.request(request)
  }

  async updateRateLimit(update: RateLimitUpdate): Promise<GatewayStatus> {
    return this.simulator.updateRateLimit(update)
  }

  async resetRateLimit(): Promise<GatewayStatus> {
    return this.simulator.resetRateLimit()
  }

  setPersistence(enabled: boolean): void {
    if (!this.storage) {
      this.persistent = false
      return
    }
    this.persistent = enabled
    this.storage.setItem(persistenceKey, String(enabled))
    if (enabled) this.persist()
    else this.storage.removeItem(configKey)
  }

  persistenceEnabled(): boolean {
    return this.persistent
  }

  private persist(): void {
    if (this.persistent && this.storage) this.storage.setItem(configKey, JSON.stringify(this.simulator.getConfig()))
  }
}
