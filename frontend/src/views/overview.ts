import type { ConsoleState } from '../state.js'
import type { ClusterRuntime, EndpointRuntime } from '../types.js'
import { escapeHTML, formatDate, formatNumber, icon, statusDot } from '../ui/html.js'

function endpointState(endpoint: EndpointRuntime): { label: string; className: string } {
  if (!endpoint.enabled) return { label: 'disabled', className: 'off' }
  if (endpoint.draining) return { label: 'draining', className: 'warn' }
  if (!endpoint.healthy || endpoint.ejected || endpoint.circuit_open || endpoint.circuit_state === 'open') {
    return { label: endpoint.ejected || endpoint.circuit_open || endpoint.circuit_state === 'open' ? 'ejected' : 'unhealthy', className: 'bad' }
  }
  return { label: 'healthy', className: 'ok' }
}

function renderEndpoint(cluster: ClusterRuntime, endpoint: EndpointRuntime, active: string): string {
  const state = endpointState(endpoint)
  const key = `${cluster.id}/${endpoint.id}`
  return `
    <article class="topology-endpoint endpoint--${state.className} ${active === key ? 'is-active' : ''}"
      data-endpoint-key="${escapeHTML(key)}" id="endpoint-${escapeHTML(cluster.id)}-${escapeHTML(endpoint.id)}">
      <div class="endpoint-index">${String(cluster.endpoints.indexOf(endpoint) + 1).padStart(2, '0')}</div>
      <div class="endpoint-copy">
        <span class="node-label">endpoint</span>
        <strong>${escapeHTML(endpoint.id)}</strong>
        <small title="${escapeHTML(endpoint.url)}">${escapeHTML(endpoint.url)}</small>
      </div>
      <div class="endpoint-telemetry">
        <span>${statusDot(state.className === 'ok' ? 'ok' : state.className === 'warn' ? 'warn' : 'off')}${state.label}</span>
        <span>${formatNumber(endpoint.requests)} req</span>
        <span>${formatNumber(endpoint.inflight)} active</span>
      </div>
    </article>
  `
}

function renderCluster(cluster: ClusterRuntime, active: string): string {
  const ready = cluster.endpoints.filter((endpoint) => endpoint.available).length
  return `
    <section class="topology-cluster" data-cluster="${escapeHTML(cluster.id)}">
      <header>
        <div><span class="node-label">cluster</span><strong>${escapeHTML(cluster.id)}</strong></div>
        <span>${ready}/${cluster.endpoints.length} ready</span>
      </header>
      <div class="topology-endpoint-grid">
        ${cluster.endpoints.map((endpoint) => renderEndpoint(cluster, endpoint, active)).join('')}
      </div>
    </section>
  `
}

function events(state: ConsoleState): string {
  if (!state.events.length) {
    return '<div class="empty-state"><span>event stream</span><p>События появятся после request, mutation или config apply.</p></div>'
  }
  return `<ol class="event-list">${state.events
    .slice(0, 8)
    .map(
      (event) => `<li class="event event--${event.kind}">
        <time>${escapeHTML(event.time)}</time>
        <div><strong>${escapeHTML(event.title)}</strong><span>${escapeHTML(event.detail)}</span></div>
        ${event.status ? `<code>${event.status}</code>` : ''}
      </li>`,
    )
    .join('')}</ol>`
}

export function renderOverview(state: ConsoleState): string {
  const status = state.status
  if (!status) return '<div class="loading-panel"><span></span><p>Читаю status и config…</p></div>'
  const clusters = status.gateway.clusters
  const endpoints = clusters.flatMap((cluster) => cluster.endpoints)
  const ready = endpoints.filter((endpoint) => endpoint.available).length
  const inflight = clusters.reduce((sum, cluster) => sum + cluster.endpoints.reduce((clusterSum, endpoint) => clusterSum + endpoint.inflight, 0), 0)
  const requestCount = status.gateway.stats.requests
  const unmatched = status.gateway.stats.route_not_found
  const unmatchedRate = requestCount ? (unmatched / requestCount) * 100 : 0
  const overload = status.protection?.overload ?? status.protection
  const storageType = typeof status.storage === 'string' ? status.storage : status.storage?.type ?? 'local'
  const storageHealthy = typeof status.storage === 'string' || status.storage?.healthy !== false
  const defaultListenerID = state.config?.listeners.find((listener) => listener.default)?.id
  const defaultListener = status.gateway.listeners.find((listener) => listener.id === defaultListenerID) ?? status.gateway.listeners[0]
  return `
    <section class="overview-guide-callout" data-render-key="overview-guide-intro" aria-labelledby="overview-guide-title">
      <div><span class="eyebrow">Первый запуск</span><h2 id="overview-guide-title">Не знаете, с чего начать?</h2><p>Клиент отправляет запрос прокси, а прокси выбирает подходящий работающий сервер. В руководстве есть простой пример, безопасный сценарий для demo и объяснение всех разделов.</p></div>
      <a href="#guide">Открыть «Как это работает» <span aria-hidden="true">→</span></a>
    </section>
    <section class="metric-grid" aria-label="Gateway metrics">
      <article class="metric-card metric-card--primary"><span>Total requests</span><strong>${formatNumber(requestCount)}</strong><small>current config lifetime</small></article>
      <article class="metric-card"><span>Endpoints ready</span><strong>${ready}<i>/ ${endpoints.length}</i></strong><small>${clusters.length} clusters</small></article>
      <article class="metric-card"><span>Inflight</span><strong>${formatNumber(inflight)}</strong><small>limit ${formatNumber(overload?.max_concurrent_requests)}</small></article>
      <article class="metric-card"><span>Unmatched routes</span><strong>${unmatchedRate.toFixed(2)}<i>%</i></strong><small>${formatNumber(unmatched)} responses</small></article>
    </section>

    <section class="panel topology-panel">
      <header class="panel-header">
        <div><span class="eyebrow">Live topology</span><h2>Request path</h2></div>
        <div class="topology-legend"><span>${statusDot('ok')}available</span><span>${statusDot('warn')}draining</span><span>${statusDot('off')}excluded</span></div>
      </header>
      <div class="topology-canvas" id="topology-canvas">
        <svg class="topology-lines" id="topology-lines" data-dom-owned aria-hidden="true"></svg>
        <article class="topology-source topology-node" id="topology-client">
          <span class="node-label">client request</span>
          <strong>${escapeHTML(state.request.host)}</strong>
          <small>${escapeHTML(state.request.method)} ${escapeHTML(state.request.path)}</small>
        </article>
        <article class="topology-gateway topology-node" id="topology-gateway">
          <div class="gateway-heading"><span class="node-label">L7 reverse proxy</span><em>GO</em></div>
          <strong>${escapeHTML(defaultListener?.address ?? ':8080')}</strong>
          <dl>
            <div><dt>routes</dt><dd>${status.gateway.routes.length}</dd></div>
            <div><dt>revision</dt><dd>${status.gateway.revision}</dd></div>
            <div><dt>rate limit</dt><dd>${status.rate_limit?.enabled ? 'active' : 'off'}</dd></div>
          </dl>
        </article>
        <div class="topology-clusters" id="topology-clusters">
          ${clusters.map((cluster) => renderCluster(cluster, state.activeEndpoint)).join('')}
        </div>
      </div>
      <footer class="topology-footer">
        <span>${icon('pulse')}last apply ${formatDate(status.gateway.applied_at)}</span>
        <span>${icon('shield')}${escapeHTML(storageType)} · ${storageHealthy ? 'healthy' : 'degraded'}</span>
      </footer>
    </section>

    <div class="overview-lower">
      <section class="panel events-panel">
        <header class="panel-header compact"><div><span class="eyebrow">Runtime</span><h2>Recent events</h2></div><button class="text-button" data-action="clear-events">clear</button></header>
        ${events(state)}
      </section>
      <section class="panel snapshot-panel">
        <header class="panel-header compact"><div><span class="eyebrow">Snapshot</span><h2>Applied config</h2></div></header>
        <dl class="fact-list">
          <div><dt>revision</dt><dd>${status.gateway.revision}</dd></div>
          <div><dt>hash</dt><dd><code>${escapeHTML(status.gateway.hash)}</code></dd></div>
          <div><dt>instance</dt><dd>${escapeHTML(status.instance_id)}</dd></div>
          <div><dt>mutations</dt><dd>${status.runtime_mutations_enabled ? 'allowed' : 'read-only'}</dd></div>
          <div><dt>last error</dt><dd>${escapeHTML(status.gateway.last_error || 'none')}</dd></div>
        </dl>
      </section>
    </div>
  `
}
