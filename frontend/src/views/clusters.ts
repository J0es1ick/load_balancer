import type { ConsoleState } from '../state.js'
import type { ClusterConfig, ClusterRuntime, EndpointRuntime, Strategy } from '../types.js'
import { escapeHTML, formatNumber, icon, statusDot } from '../ui/html.js'

const strategies: Array<{ value: Strategy; label: string }> = [
  { value: 'round_robin', label: 'Round robin' },
  { value: 'weighted_round_robin', label: 'Weighted round robin' },
  { value: 'least_request', label: 'Least request' },
  { value: 'rendezvous', label: 'Rendezvous hash' },
]

function endpointRow(cluster: ClusterRuntime, endpoint: EndpointRuntime, mutable: boolean): string {
  const state = !endpoint.enabled ? 'off' : endpoint.draining ? 'warn' : endpoint.available ? 'ok' : 'off'
  return `<div class="endpoint-row" data-render-key="${escapeHTML(cluster.id)}/${escapeHTML(endpoint.id)}">
    <div class="endpoint-identity">${statusDot(state)}<div><strong>${escapeHTML(endpoint.id)}</strong><span>${escapeHTML(endpoint.url)}</span></div></div>
    <div class="endpoint-numbers"><span><b>${formatNumber(endpoint.requests)}</b> requests</span><span><b>${formatNumber(endpoint.inflight)}</b> inflight</span><span><b>${endpoint.weight}</b> weight</span></div>
    <div class="endpoint-actions">
      <button class="icon-button ${endpoint.draining ? 'is-warning' : ''}" data-action="drain-endpoint" data-cluster="${escapeHTML(cluster.id)}" data-endpoint="${escapeHTML(endpoint.id)}" data-value="${!endpoint.draining}" ${!mutable ? 'disabled' : ''} title="${endpoint.draining ? 'Stop draining' : 'Drain endpoint'}" aria-label="${endpoint.draining ? 'Stop draining' : 'Drain'} ${escapeHTML(endpoint.id)}">D</button>
      <button class="icon-button ${endpoint.enabled ? '' : 'is-off'}" data-action="toggle-endpoint" data-cluster="${escapeHTML(cluster.id)}" data-endpoint="${escapeHTML(endpoint.id)}" data-value="${!endpoint.enabled}" ${!mutable ? 'disabled' : ''} title="${endpoint.enabled ? 'Disable endpoint' : 'Enable endpoint'}" aria-label="${endpoint.enabled ? 'Disable' : 'Enable'} ${escapeHTML(endpoint.id)}">${icon('power')}</button>
    </div>
  </div>`
}

function clusterCard(runtime: ClusterRuntime, config: ClusterConfig | undefined, endpointMutable: boolean, configMutable: boolean): string {
  const active = runtime.endpoints.filter((endpoint) => endpoint.enabled).length
  const discovery = config?.discovery ?? { type: runtime.discovery.type }
  return `<article class="panel cluster-card" data-render-key="${escapeHTML(runtime.id)}">
    <header class="cluster-header">
      <div><span class="eyebrow">Cluster</span><h2>${escapeHTML(runtime.id)}</h2></div>
      <div class="cluster-health"><strong>${runtime.endpoints.filter((endpoint) => endpoint.available).length}/${runtime.endpoints.length}</strong><span>available</span></div>
    </header>
      <div class="cluster-controls">
      <label><span>Strategy <i>admin</i></span><select data-cluster-strategy="${escapeHTML(runtime.id)}" ${!configMutable ? 'disabled' : ''}>
        ${strategies.map((strategy) => `<option value="${strategy.value}" ${runtime.strategy === strategy.value ? 'selected' : ''}>${strategy.label}</option>`).join('')}
      </select></label>
      <label><span>Enabled endpoints <i>operator</i></span><select data-endpoint-count="${escapeHTML(runtime.id)}" ${!endpointMutable ? 'disabled' : ''}>
        ${Array.from({ length: runtime.endpoints.length + 1 }, (_, count) => `<option value="${count}" ${count === active ? 'selected' : ''}>${count} / ${runtime.endpoints.length}</option>`).join('')}
      </select></label>
      <div class="cluster-stat"><span>requests</span><strong>${formatNumber(runtime.stats?.requests ?? runtime.endpoints.reduce((sum, endpoint) => sum + endpoint.requests, 0))}</strong></div>
      <div class="cluster-stat"><span>p95</span><strong>${runtime.stats?.p95_ms?.toFixed(1) ?? '—'} ms</strong></div>
    </div>
    <div class="endpoint-table">${runtime.endpoints.map((endpoint) => endpointRow(runtime, endpoint, endpointMutable)).join('')}</div>
    <details class="cluster-details">
      <summary>Discovery, TLS and transport</summary>
      <div class="detail-columns">
        <dl><div><dt>discovery</dt><dd>${escapeHTML(discovery.type)}</dd></div><div><dt>last update</dt><dd>${escapeHTML(runtime.discovery.last_update ?? '—')}</dd></div><div><dt>stale</dt><dd>${runtime.discovery.stale ? 'yes' : 'no'}</dd></div><div><dt>source</dt><dd>${escapeHTML(discovery.service ?? discovery.hostname ?? 'static config')}</dd></div></dl>
        <dl><div><dt>upstream TLS</dt><dd>${runtime.tls.enabled ? 'enabled' : 'disabled'}</dd></div><div><dt>server name</dt><dd>${escapeHTML(runtime.tls.server_name ?? '—')}</dd></div><div><dt>protocol</dt><dd>${escapeHTML(config?.transport.protocol ?? '—')}</dd></div><div><dt>max connections</dt><dd>${formatNumber(config?.transport.max_conns_per_host)}</dd></div></dl>
        <dl><div><dt>health mode</dt><dd>${escapeHTML(config?.health.mode ?? '—')}</dd></div><div><dt>health path</dt><dd>${escapeHTML(config?.health.path ?? '—')}</dd></div><div><dt>interval</dt><dd>${escapeHTML(config?.health.interval ?? '—')}</dd></div><div><dt>retry budget</dt><dd>${runtime.retry ? `${runtime.retry.budget.tokens.toFixed(0)} / ${runtime.retry.budget.capacity}` : '—'}</dd></div></dl>
      </div>
    </details>
  </article>`
}

export function renderClusters(state: ConsoleState): string {
  const runtime = state.status?.gateway.clusters ?? []
  const role = state.status?.principal?.role ?? 'viewer'
  const endpointMutable = Boolean(state.status?.runtime_mutations_enabled) && (role === 'operator' || role === 'admin')
  const configMutable = Boolean(state.status?.runtime_mutations_enabled) && role === 'admin'
  return `
    <div class="section-intro">
      <div><span class="eyebrow">Endpoint lifecycle</span><h2>Изменения применяются к текущей revision</h2></div>
      <p>${endpointMutable ? `Endpoint lifecycle доступен роли ${role}; strategy apply требует admin.` : 'Deployment работает read-only для этого principal.'}</p>
    </div>
    <div class="cluster-list">
      ${runtime.map((cluster) => clusterCard(cluster, state.config?.clusters.find((item) => item.id === cluster.id), endpointMutable, configMutable)).join('')}
    </div>
  `
}
