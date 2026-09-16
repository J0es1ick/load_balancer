import type { GatewayAdapter } from './adapters/gateway.js'
import { cloneConfig, preserveRuntimeEndpointEnablement } from './config/defaults.js'
import { initialState, type ConsoleState } from './state.js'
import type {
  AppMode,
  ConsoleEvent,
  GatewayConfig,
  HTTPMethod,
  RequestResult,
  RequestSpec,
  SectionID,
  Strategy,
} from './types.js'
import { renderShell } from './ui/shell.js'
import { patchMarkup } from './ui/dom.js'
import { TopologyDiagram } from './ui/topology.js'
import { renderClusters } from './views/clusters.js'
import { renderConfig } from './views/config.js'
import { renderGuide } from './views/guide.js'
import { renderOverview } from './views/overview.js'
import { renderRequestLab } from './views/request.js'
import { renderRoutes } from './views/routes.js'

const sections: SectionID[] = ['overview', 'request', 'routes', 'clusters', 'config', 'guide']

function sectionFromHash(): SectionID {
  const value = window.location.hash.replace('#', '') as SectionID
  return sections.includes(value) ? value : 'overview'
}

function parseConfig(text: string): GatewayConfig {
  const parsed = JSON.parse(text) as unknown
  if (!parsed || typeof parsed !== 'object') throw new Error('Config must be a JSON object')
  return parsed as GatewayConfig
}

function cloneTraffic(): ConsoleState['traffic'] {
  return { running: false, rps: 4, sent: 0, ok: 0, errors: 0, lastBackend: '—', distribution: {} }
}

export class ProxyConsole {
  private readonly state: ConsoleState
  private readonly topology = new TopologyDiagram()
  private trafficTimer = 0
  private pollTimer = 0
  private eventSequence = 0
  private renderTimer = 0
  private renderedSection: SectionID | null = null

  constructor(
    private readonly root: HTMLElement,
    private readonly adapter: GatewayAdapter,
    private readonly mode: AppMode,
  ) {
    this.state = initialState(sectionFromHash(), adapter.persistenceEnabled?.() ?? false)
    this.bindEvents()
  }

  async start(): Promise<void> {
    this.render()
    try {
      const [status, config] = await Promise.all([this.adapter.getStatus(), this.adapter.getConfig()])
      this.state.status = status
      this.state.config = config
      this.syncRateLimit(status)
      this.state.editorText = JSON.stringify(config, null, 2)
      this.state.connected = true
      this.pushEvent('system', `${this.mode} adapter ready`, `${status.instance_id} · revision ${status.gateway.revision}`)
    } catch (error) {
      this.fail(error)
    } finally {
      this.state.loading = false
      this.render()
    }
    if (this.mode === 'live') this.pollTimer = window.setInterval(() => void this.poll(), 5_000)
  }

  private bindEvents(): void {
    window.addEventListener('hashchange', () => {
      this.state.section = sectionFromHash()
      this.render()
      document.querySelector<HTMLElement>('#main-view')?.focus({ preventScroll: true })
    })
    this.root.addEventListener('click', (event) => void this.handleClick(event))
    this.root.addEventListener('submit', (event) => {
      const form = event.target as HTMLFormElement
      if (form.id !== 'request-form' && form.id !== 'rate-limit-form') return
      event.preventDefault()
      if (form.id === 'request-form') void this.sendOnce(true)
      else void this.updateRateLimit()
    })
    this.root.addEventListener('input', (event) => this.handleInput(event))
    this.root.addEventListener('change', (event) => void this.handleChange(event))
    this.root.addEventListener('focusout', () => this.scheduleRender())
    window.addEventListener('beforeunload', () => {
      window.clearInterval(this.trafficTimer)
      window.clearInterval(this.pollTimer)
      window.clearTimeout(this.renderTimer)
      this.topology.disconnect()
    })
  }

  private async handleClick(event: Event): Promise<void> {
    const target = (event.target as HTMLElement).closest<HTMLElement>('[data-action]')
    if (!target) return
    const action = target.dataset.action
    if (action === 'dismiss-error') {
      this.state.error = ''
      this.render()
    } else if (action === 'clear-events') {
      this.state.events = []
      this.render()
    } else if (action === 'toggle-traffic') {
      this.state.traffic.running ? this.stopTraffic() : this.startTraffic()
    } else if (action === 'burst') {
      await this.burst()
    } else if (action === 'reset-traffic') {
      const running = this.state.traffic.running
      const rps = this.state.traffic.rps
      this.state.traffic = { ...cloneTraffic(), running, rps }
      this.render()
    } else if (action === 'toggle-endpoint' || action === 'drain-endpoint') {
      await this.mutateEndpoint(target, action === 'drain-endpoint')
    } else if (action === 'format-config') {
      this.formatConfig()
    } else if (action === 'validate-config') {
      await this.validateEditor()
    } else if (action === 'apply-config') {
      await this.applyEditor()
    } else if (action === 'rollback-config') {
      await this.rollback()
    } else if (action === 'reset-rate-limit') {
      await this.resetRateLimit()
    }
  }

  private handleInput(event: Event): void {
    const target = event.target as HTMLInputElement | HTMLTextAreaElement
    if (target.id === 'config-editor') {
      this.state.editorText = target.value
      this.state.editorDirty = true
      this.state.validation = null
      this.refreshDirtyActions()
      return
    }
    const requestField = target.dataset.requestField as keyof ConsoleState['request'] | undefined
    if (requestField) this.state.request[requestField] = target.value as never
    if (target.matches('[data-traffic-rps]')) this.state.traffic.rps = Math.max(1, Math.min(25, Number(target.value) || 1))
    this.captureRateLimitField(target)
  }

  private async handleChange(event: Event): Promise<void> {
    const target = event.target as HTMLInputElement | HTMLSelectElement
    const requestField = target.dataset.requestField as keyof ConsoleState['request'] | undefined
    if (requestField) this.state.request[requestField] = target.value as never
    if (target.matches('[data-demo-persistence]')) {
      this.state.persistence = (target as HTMLInputElement).checked
      this.adapter.setPersistence?.(this.state.persistence)
      this.toast(this.state.persistence ? 'Demo config is stored locally' : 'Demo persistence is off')
    }
    const strategyCluster = target.dataset.clusterStrategy
    if (strategyCluster) await this.changeStrategy(strategyCluster, target.value as Strategy)
    const countCluster = target.dataset.endpointCount
    if (countCluster) await this.changeEndpointCount(countCluster, Number(target.value))
    if (target.matches('[data-traffic-rps]') && this.state.traffic.running) {
      this.stopTraffic(false)
      this.startTraffic()
    }
    this.captureRateLimitField(target)
  }

  private render(): void {
    window.clearTimeout(this.renderTimer)
    this.renderTimer = 0
    if (this.renderedSection !== this.state.section) {
      this.topology.disconnect()
      this.root.querySelector('#main-view')?.replaceChildren()
      this.renderedSection = this.state.section
    }
    patchMarkup(this.root, renderShell(this.state, this.mode, this.renderView()))
    if (this.state.section === 'overview') this.topology.connect()
  }

  private scheduleRender(): void {
    if (this.renderTimer) return
    this.renderTimer = window.setTimeout(() => {
      this.renderTimer = 0
      this.render()
    }, 100)
  }

  private renderView(): string {
    switch (this.state.section) {
      case 'request': return renderRequestLab(this.state)
      case 'routes': return renderRoutes(this.state)
      case 'clusters': return renderClusters(this.state)
      case 'config': return renderConfig(this.state, this.mode)
      case 'guide': return renderGuide(this.mode)
      default: return renderOverview(this.state)
    }
  }

  private async poll(): Promise<void> {
    try {
      this.state.status = await this.adapter.getStatus()
      if (!this.state.rateLimitDirty) this.syncRateLimit(this.state.status)
      this.state.connected = true
      this.scheduleRender()
    } catch (error) {
      this.state.connected = false
      this.state.error = this.errorMessage(error)
      this.scheduleRender()
    }
  }

  private requestSpec(): RequestSpec {
    let headers: unknown
    try {
      headers = JSON.parse(this.state.request.headersText || '{}')
    } catch {
      throw new Error('Headers must be a valid JSON object')
    }
    if (!headers || typeof headers !== 'object' || Array.isArray(headers)) throw new Error('Headers must be a JSON object')
    const normalizedHeaders: Record<string, string> = {}
    Object.entries(headers).forEach(([name, value]) => { normalizedHeaders[name] = String(value) })
    const path = this.state.request.path.startsWith('/') ? this.state.request.path : `/${this.state.request.path}`
    return { method: this.state.request.method as HTTPMethod, host: this.state.request.host.trim(), path, headers: normalizedHeaders, body: this.state.request.body }
  }

  private async sendOnce(showBusy: boolean): Promise<RequestResult | null> {
    if (!this.canOperate()) {
      this.state.error = 'Synthetic request requires operator/admin role and enabled runtime mutations'
      this.render()
      return null
    }
    if (showBusy) {
      this.state.busy = true
      this.render()
    }
    try {
      const result = await this.adapter.sendRequest(this.requestSpec())
      this.consumeResult(result)
      if (this.mode === 'demo' || this.state.traffic.sent % 10 === 0) this.state.status = await this.adapter.getStatus()
      if (!showBusy && ['overview', 'request', 'clusters'].includes(this.state.section)) this.scheduleRender()
      return result
    } catch (error) {
      this.state.error = this.errorMessage(error)
      this.pushEvent('error', 'Request failed', this.state.error)
      if (!showBusy) this.scheduleRender()
      return null
    } finally {
      if (showBusy) {
        this.state.busy = false
        this.render()
      }
    }
  }

  private consumeResult(result: RequestResult): void {
    this.state.response = result
    this.state.traffic.sent += 1
    result.status < 400 ? (this.state.traffic.ok += 1) : (this.state.traffic.errors += 1)
    this.state.traffic.lastBackend = result.backend ?? '—'
    if (result.backend) {
      this.state.traffic.distribution[result.backend] = (this.state.traffic.distribution[result.backend] ?? 0) + 1
      this.state.activeEndpoint = `${result.cluster ?? ''}/${result.backend}`
      if (this.state.section === 'overview') this.topology.pulse(this.state.activeEndpoint)
    }
    this.pushEvent('request', `${result.status} ${this.state.request.method} ${this.state.request.path}`, `${result.route ?? 'no route'} · ${result.backend ?? 'no endpoint'} · ${result.duration_ms.toFixed(1)} ms`, result.status)
  }

  private startTraffic(): void {
    if (!this.canOperate()) return
    window.clearInterval(this.trafficTimer)
    this.state.traffic.running = true
    const delay = Math.max(40, Math.round(1000 / this.state.traffic.rps))
    this.trafficTimer = window.setInterval(() => void this.sendOnce(false), delay)
    this.pushEvent('system', 'Traffic started', `${this.state.traffic.rps} requests/s`)
    this.render()
  }

  private stopTraffic(render = true): void {
    window.clearInterval(this.trafficTimer)
    this.trafficTimer = 0
    this.state.traffic.running = false
    this.pushEvent('system', 'Traffic stopped', `${this.state.traffic.sent} requests in this session`)
    if (render) this.render()
  }

  private async burst(): Promise<void> {
    if (!this.canOperate()) return
    this.state.busy = true
    this.render()
    await Promise.all(Array.from({ length: 20 }, () => this.sendOnce(false)))
    this.state.busy = false
    this.pushEvent('system', 'Burst completed', '20 concurrent synthetic requests')
    this.render()
  }

  private async mutateEndpoint(target: HTMLElement, draining: boolean): Promise<void> {
    if (!this.canOperate() || !this.state.status?.runtime_mutations_enabled) return
    const cluster = target.dataset.cluster
    const endpoint = target.dataset.endpoint
    if (!cluster || !endpoint) return
    await this.operation(async () => {
      const value = target.dataset.value === 'true'
      this.state.status = await this.adapter.mutateEndpoint(cluster, endpoint, draining ? { draining: value } : { enabled: value })
      this.pushEvent('health', `${endpoint} ${draining ? (value ? 'draining' : 'active') : value ? 'enabled' : 'disabled'}`, cluster)
    })
  }

  private async changeEndpointCount(clusterID: string, count: number): Promise<void> {
    if (!this.canOperate() || !this.state.status?.runtime_mutations_enabled) return
    const cluster = this.state.status?.gateway.clusters.find((item) => item.id === clusterID)
    if (!cluster) return
    await this.operation(async () => {
      let status = this.state.status
      for (let index = 0; index < cluster.endpoints.length; index += 1) {
        const endpoint = cluster.endpoints[index]
        const shouldEnable = index < count
        if (endpoint.enabled !== shouldEnable) status = await this.adapter.mutateEndpoint(clusterID, endpoint.id, { enabled: shouldEnable })
      }
      this.state.status = status
      this.pushEvent('health', `${clusterID} endpoint count changed`, `${count}/${cluster.endpoints.length} enabled`)
    })
  }

  private async changeStrategy(clusterID: string, strategy: Strategy): Promise<void> {
    if (!this.canAdmin() || !this.state.status?.runtime_mutations_enabled) return
    if (!this.state.config || !this.state.status) return
    const config = cloneConfig(this.state.config)
    const cluster = config.clusters.find((item) => item.id === clusterID)
    if (!cluster) return
    const runtimeCluster = this.state.status.gateway.clusters.find((item) => item.id === clusterID)
    if (runtimeCluster) preserveRuntimeEndpointEnablement(cluster, runtimeCluster)
    cluster.strategy = strategy
    if (strategy === 'rendezvous' && !cluster.hash_key) cluster.hash_key = 'path'
    await this.operation(async () => {
      const validation = await this.adapter.validateConfig(config)
      if (!validation.valid) throw new Error(validation.errors?.map((issue) => issue.message).join('; ') || 'strategy rejected')
      this.state.status = await this.adapter.applyConfig(config, this.state.status!.gateway.revision)
      this.state.config = await this.adapter.getConfig()
      this.state.editorText = JSON.stringify(this.state.config, null, 2)
      this.state.editorDirty = false
      this.pushEvent('config', `${clusterID} strategy applied`, strategy)
    })
  }

  private formatConfig(): void {
    try {
      this.state.editorText = JSON.stringify(parseConfig(this.state.editorText), null, 2)
      this.state.error = ''
      this.state.editorDirty = true
    } catch (error) {
      this.state.error = this.errorMessage(error)
    }
    this.render()
  }

  private async validateEditor(): Promise<boolean> {
    try {
      this.state.validation = await this.adapter.validateConfig(parseConfig(this.state.editorText))
      this.state.error = ''
      this.pushEvent('config', this.state.validation.valid ? 'Config validation passed' : 'Config validation rejected', `${this.state.validation.errors?.length ?? 0} issues`)
      this.render()
      return this.state.validation.valid
    } catch (error) {
      this.state.validation = { valid: false, errors: [{ path: '$', message: this.errorMessage(error) }] }
      this.state.error = this.errorMessage(error)
      this.render()
      return false
    }
  }

  private async applyEditor(): Promise<void> {
    if (!this.canAdmin() || !this.state.status?.runtime_mutations_enabled) return
    if (!this.state.status || !(await this.validateEditor())) return
    const config = parseConfig(this.state.editorText)
    await this.operation(async () => {
      this.state.status = await this.adapter.applyConfig(config, this.state.status!.gateway.revision)
      this.state.config = await this.adapter.getConfig()
      this.state.editorText = JSON.stringify(this.state.config, null, 2)
      this.state.editorDirty = false
      this.state.validation = { valid: true }
      this.pushEvent('config', 'Config snapshot applied', `revision ${this.state.status.gateway.revision}`)
    })
  }

  private async rollback(): Promise<void> {
    if (!this.canAdmin() || !this.state.status?.runtime_mutations_enabled) return
    if (!this.state.status) return
    await this.operation(async () => {
      this.state.status = await this.adapter.rollbackConfig(this.state.status!.gateway.revision)
      this.state.config = await this.adapter.getConfig()
      this.state.editorText = JSON.stringify(this.state.config, null, 2)
      this.state.editorDirty = false
      this.state.validation = null
      this.pushEvent('config', 'Previous snapshot restored', `new revision ${this.state.status.gateway.revision}`)
    })
  }

  private captureRateLimitField(target: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement): void {
    const field = target.dataset.rateLimitField
    if (!field) return
    if (field === 'enabled' && target instanceof HTMLInputElement) this.state.rateLimitDraft.enabled = target.checked
    if (field === 'capacity') this.state.rateLimitDraft.capacity = Number(target.value)
    if (field === 'refillPerSecond') this.state.rateLimitDraft.refillPerSecond = Number(target.value)
    if (field === 'failureMode') {
      this.state.rateLimitDraft.failureMode = target.value as ConsoleState['rateLimitDraft']['failureMode']
    }
    this.state.rateLimitDirty = true
    this.refreshDirtyActions()
  }

  private refreshDirtyActions(): void {
    const runtimeMutable = Boolean(this.state.status?.runtime_mutations_enabled)
    const apply = this.root.querySelector<HTMLButtonElement>('[data-action="apply-config"]')
    if (apply) apply.disabled = !runtimeMutable || !this.canAdmin() || !this.state.editorDirty
    const rateLimitApply = this.root.querySelector<HTMLButtonElement>('#rate-limit-form button[type="submit"]')
    if (rateLimitApply) rateLimitApply.disabled = !runtimeMutable || !this.canAdmin() || !this.state.rateLimitDirty
  }

  private syncRateLimit(status: NonNullable<ConsoleState['status']>): void {
    const rateLimit = status.rate_limit
    this.state.rateLimitDraft = {
      enabled: rateLimit?.enabled ?? false,
      capacity: rateLimit?.capacity ?? 1,
      refillPerSecond: rateLimit?.refill_per_second ?? 1,
      failureMode:
        rateLimit?.failure_mode === 'fail-open' || rateLimit?.failure_mode === 'fail-closed' || rateLimit?.failure_mode === 'local-fallback'
          ? rateLimit.failure_mode
          : 'fail-open',
    }
    this.state.rateLimitDirty = false
  }

  private async updateRateLimit(): Promise<void> {
    if (!this.canAdmin() || !this.state.status?.runtime_mutations_enabled) return
    const draft = this.state.rateLimitDraft
    if (!Number.isInteger(draft.capacity) || draft.capacity < 1 || !Number.isFinite(draft.refillPerSecond) || draft.refillPerSecond <= 0) {
      this.state.error = 'Capacity must be a positive integer and refill must be positive'
      this.render()
      return
    }
    await this.operation(async () => {
      this.state.status = await this.adapter.updateRateLimit({
        enabled: draft.enabled,
        capacity: draft.capacity,
        refill_per_second: draft.refillPerSecond,
        failure_mode: draft.failureMode,
      })
      this.syncRateLimit(this.state.status)
      this.pushEvent('config', 'Runtime rate limit updated', `${draft.capacity} tokens · ${draft.refillPerSecond}/s · ${draft.failureMode}`)
    })
  }

  private async resetRateLimit(): Promise<void> {
    if (!this.canAdmin() || !this.state.status?.runtime_mutations_enabled) return
    await this.operation(async () => {
      this.state.status = await this.adapter.resetRateLimit()
      this.syncRateLimit(this.state.status)
      this.pushEvent('config', 'Runtime rate-limit bucket reset', 'current verified client · current instance')
    })
  }

  private async operation(callback: () => Promise<void>): Promise<void> {
    this.state.busy = true
    this.render()
    try {
      await callback()
      this.state.connected = true
      this.state.error = ''
    } catch (error) {
      this.fail(error)
    } finally {
      this.state.busy = false
      this.render()
    }
  }

  private pushEvent(kind: ConsoleEvent['kind'], title: string, detail: string, status?: number): void {
    this.eventSequence += 1
    this.state.events.unshift({ id: this.eventSequence, time: new Date().toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' }), kind, title, detail, status })
    this.state.events = this.state.events.slice(0, 40)
  }

  private fail(error: unknown): void {
    this.state.error = this.errorMessage(error)
    this.state.connected = false
    this.pushEvent('error', 'Operation failed', this.state.error)
  }

  private errorMessage(error: unknown): string {
    return error instanceof Error ? error.message : String(error)
  }

  private canOperate(): boolean {
    const role = this.state.status?.principal?.role ?? 'viewer'
    return Boolean(this.state.status?.runtime_mutations_enabled) && (role === 'operator' || role === 'admin')
  }

  private canAdmin(): boolean {
    return this.state.status?.principal?.role === 'admin'
  }

  private toast(message: string): void {
    const region = this.root.querySelector<HTMLElement>('.toast-region')
    if (!region) return
    const toast = document.createElement('div')
    toast.className = 'toast'
    toast.textContent = message
    region.append(toast)
    window.setTimeout(() => toast.remove(), 2_500)
  }
}
