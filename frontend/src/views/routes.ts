import type { ConsoleState } from '../state.js'
import type { RouteConfig } from '../types.js'
import { escapeHTML, formatBytes } from '../ui/html.js'

function routeTarget(route: RouteConfig): string {
  if (route.action.redirect) return `redirect ${route.action.redirect.status_code}`
  if (route.action.cluster) return route.action.cluster
  return (route.action.weighted_clusters ?? []).map((target) => `${target.cluster}:${target.weight}`).join(' / ')
}

function routeMatch(route: RouteConfig): string {
  const parts = [
    ...(route.match.hosts ?? []).map((host) => `host=${host}`),
    route.match.path_exact ? `path=${route.match.path_exact}` : '',
    route.match.path_prefix ? `path^=${route.match.path_prefix}` : '',
    route.match.methods?.join('|') ?? '',
  ].filter(Boolean)
  return parts.join(' · ') || 'all requests'
}

export function renderRoutes(state: ConsoleState): string {
  const routes: RouteConfig[] = state.config?.routes ?? []
  return `
    <section class="panel routes-panel">
      <header class="panel-header">
        <div><span class="eyebrow">Evaluation order</span><h2>${routes.length} configured routes</h2></div>
        <span class="contract-chip">priority descending</span>
      </header>
      <div class="route-list">
        ${routes
          .slice()
          .sort((left, right) => right.priority - left.priority)
          .map(
            (route, index) => `<article class="route-card">
              <div class="route-order">${String(index + 1).padStart(2, '0')}</div>
              <div class="route-main">
                <header><div><span>${escapeHTML(route.listener)}</span><h3>${escapeHTML(route.id)}</h3></div><code>priority ${route.priority}</code></header>
                <p class="route-match">${escapeHTML(routeMatch(route))}</p>
                <div class="route-flow"><span>match</span><i></i><strong>${escapeHTML(routeTarget(route))}</strong></div>
              </div>
              <dl class="policy-grid">
                <div><dt>request timeout</dt><dd>${escapeHTML(route.timeouts?.request ?? 'inherited')}</dd></div>
                <div><dt>attempts</dt><dd>${route.retry?.max_attempts ?? 'inherited'}</dd></div>
                <div><dt>body limit</dt><dd>${formatBytes(route.max_request_body_bytes)}</dd></div>
                <div><dt>route limiter</dt><dd>${route.rate_limit?.enabled ? `${route.rate_limit.refill_per_second}/s` : 'off'}</dd></div>
              </dl>
              <details class="route-details">
                <summary>Policy details</summary>
                <div>
                  <section><span>Retry</span><pre>${escapeHTML(JSON.stringify(route.retry ?? {}, null, 2))}</pre></section>
                  <section><span>Header transforms</span><pre>${escapeHTML(JSON.stringify({ request: route.request_headers, response: route.response_headers }, null, 2))}</pre></section>
                </div>
              </details>
            </article>`,
          )
          .join('')}
      </div>
    </section>
  `
}
