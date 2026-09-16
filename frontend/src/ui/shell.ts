import type { AppMode, SectionID } from '../types.js'
import type { ConsoleState } from '../state.js'
import { escapeHTML, icon, statusDot } from './html.js'

const navigation: Array<{ id: SectionID; label: string; icon: Parameters<typeof icon>[0] }> = [
  { id: 'overview', label: 'Overview', icon: 'overview' },
  { id: 'request', label: 'Request lab', icon: 'request' },
  { id: 'routes', label: 'Routes', icon: 'routes' },
  { id: 'clusters', label: 'Clusters', icon: 'clusters' },
  { id: 'config', label: 'Config', icon: 'config' },
  { id: 'guide', label: 'Как это работает', icon: 'guide' },
]

const descriptions: Record<SectionID, string> = {
  overview: 'Состояние data plane и топология',
  request: 'Проверка routing, retry и distribution',
  routes: 'Порядок сопоставления и per-route policies',
  clusters: 'Endpoint lifecycle, discovery и transport',
  config: 'Валидация и атомарная смена snapshot',
  guide: 'Простое руководство и инженерная справка',
}

export function renderShell(state: ConsoleState, mode: AppMode, view: string): string {
  const section = navigation.find((item) => item.id === state.section) ?? navigation[0]
  const revision = state.status?.gateway.revision ?? '—'
  const hash = state.status?.gateway.hash?.slice(0, 8) ?? '—'
  return `
    <div class="app-shell">
      <aside class="sidebar" aria-label="Основная навигация">
        <a class="brand" href="#overview" data-section="overview" aria-label="Proxy Console">
          <span class="brand-mark">P<span>/</span></span>
          <span><b>proxy</b><small>console</small></span>
        </a>
        <nav class="nav-list">
          ${navigation
            .map(
              (item) => `<a href="#${item.id}" data-section="${item.id}" aria-label="${escapeHTML(item.label)}" class="nav-item ${
                state.section === item.id ? 'is-active' : ''
              }" ${state.section === item.id ? 'aria-current="page"' : ''}>${icon(item.icon)}<span>${item.label}</span></a>`,
            )
            .join('')}
        </nav>
        <div class="sidebar-state">
          <div>${statusDot(state.connected ? 'ok' : state.loading ? 'warn' : 'off')}<span>${
            state.connected ? (mode === 'demo' ? 'Симулятор готов' : 'API доступен') : state.loading ? 'Синхронизация' : 'Нет связи'
          }</span></div>
          <small>${mode === 'demo' ? 'browser simulator' : escapeHTML(state.status?.instance_id ?? 'live')}</small>
        </div>
      </aside>
      <section class="workspace">
        <header class="topbar">
          <div>
            <span class="eyebrow">${section.label}</span>
            <h1>${descriptions[state.section]}</h1>
          </div>
          <div class="runtime-strip" aria-label="Runtime metadata">
            <span class="mode-badge mode-badge--${mode}">${mode}</span>
            <span><small>ROLE</small><b>${escapeHTML(state.status?.principal?.role ?? 'viewer')}</b></span>
            <span><small>REV</small><b>${revision}</b></span>
            <span><small>HASH</small><code>${escapeHTML(hash)}</code></span>
          </div>
        </header>
        <div class="global-error" role="alert" ${state.error ? '' : 'hidden'}><span>${escapeHTML(state.error)}</span><button data-action="dismiss-error" aria-label="Закрыть">×</button></div>
        <main class="main-view" id="main-view" tabindex="-1">${view}</main>
      </section>
    </div>
    <div class="busy-layer ${state.busy ? 'is-visible' : ''}" aria-hidden="${!state.busy}"><span></span></div>
    <div class="toast-region" data-dom-owned aria-live="polite" aria-atomic="true"></div>
  `
}
