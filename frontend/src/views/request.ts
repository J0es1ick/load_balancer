import type { ConsoleState } from '../state.js'
import { escapeHTML, formatNumber, icon } from '../ui/html.js'

function responsePanel(state: ConsoleState): string {
  const response = state.response
  if (!response) {
    return `<div class="response-empty">${icon('request')}<strong>Запрос ещё не отправлялся</strong><span>Результат покажет выбранные route, cluster и endpoint.</span></div>`
  }
  const statusClass = response.status < 400 ? 'ok' : response.status < 500 ? 'warn' : 'bad'
  return `
    <div class="response-head">
      <span class="http-status http-status--${statusClass}">${response.status}</span>
      <div><strong>${escapeHTML(response.route ?? 'no route')}</strong><span>${escapeHTML(response.cluster ?? '—')} → ${escapeHTML(response.backend ?? '—')}</span></div>
      <dl><div><dt>duration</dt><dd>${response.duration_ms.toFixed(1)} ms</dd></div><div><dt>attempts</dt><dd>${response.attempts}</dd></div></dl>
    </div>
    <div class="response-tabs"><span>Body</span><span>${response.truncated ? 'truncated' : 'complete'}</span></div>
    <pre class="response-body"><code>${escapeHTML(response.body || '(empty body)')}</code></pre>
    <details class="raw-details"><summary>Response headers</summary><pre>${escapeHTML(JSON.stringify(response.headers, null, 2))}</pre></details>
  `
}

export function renderRequestLab(state: ConsoleState): string {
  const role = state.status?.principal?.role ?? 'viewer'
  const canRequest = Boolean(state.status?.runtime_mutations_enabled) && (role === 'operator' || role === 'admin')
  const canTuneRateLimit = role === 'admin' && Boolean(state.status?.runtime_mutations_enabled)
  const rateLimit = state.status?.rate_limit
  const rateDraft = state.rateLimitDraft
  const distribution = Object.entries(state.traffic.distribution).sort((left, right) => right[1] - left[1])
  const max = Math.max(1, ...distribution.map(([, count]) => count))
  return `
    <div class="request-layout">
      <section class="panel request-builder">
        <header class="panel-header"><div><span class="eyebrow">Synthetic request</span><h2>Request builder</h2></div><span class="contract-chip">POST /api/v1/request</span></header>
        <form id="request-form" class="form-stack">
          ${!canRequest ? '<div class="permission-note">Synthetic request требует роль operator/admin и включённые runtime mutations.</div>' : ''}
          <div class="request-line">
            <label><span>Method</span><select name="method" data-request-field="method">
              ${['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS']
                .map((method) => `<option ${state.request.method === method ? 'selected' : ''}>${method}</option>`)
                .join('')}
            </select></label>
            <label class="grow"><span>Host</span><input name="host" data-request-field="host" value="${escapeHTML(state.request.host)}" autocomplete="off" /></label>
          </div>
          <label><span>Path + query <i>/api/ matches route api</i></span><input name="path" data-request-field="path" value="${escapeHTML(state.request.path)}" autocomplete="off" spellcheck="false" /></label>
          <label><span>Headers <i>JSON object</i></span><textarea name="headers" data-request-field="headersText" rows="5" spellcheck="false">${escapeHTML(state.request.headersText)}</textarea></label>
          <label><span>Body</span><textarea name="body" data-request-field="body" rows="6" spellcheck="false" placeholder="optional request body">${escapeHTML(state.request.body)}</textarea></label>
          <div class="form-actions">
            <button class="button button--primary" type="submit" ${state.busy || !canRequest ? 'disabled' : ''}>${icon('arrow')}Send once</button>
            <button class="button" type="button" data-action="toggle-traffic" ${!canRequest ? 'disabled' : ''}>${state.traffic.running ? '■ Stop traffic' : '▶ Start traffic'}</button>
            <button class="button" type="button" data-action="burst" ${!canRequest ? 'disabled' : ''}>Burst ×20</button>
            <label class="rps-control"><span>RPS</span><input type="number" min="1" max="25" value="${state.traffic.rps}" data-traffic-rps /></label>
          </div>
        </form>
      </section>
      <section class="panel response-panel">
        <header class="panel-header"><div><span class="eyebrow">Result</span><h2>Response inspector</h2></div></header>
        ${responsePanel(state)}
      </section>
    </div>
    <section class="panel traffic-panel">
      <header class="panel-header compact"><div><span class="eyebrow">Session counters</span><h2>Traffic distribution</h2></div><button class="text-button" data-action="reset-traffic">reset</button></header>
      <div class="traffic-summary">
        <div><span>sent</span><strong>${formatNumber(state.traffic.sent)}</strong></div>
        <div><span>2xx–3xx</span><strong>${formatNumber(state.traffic.ok)}</strong></div>
        <div><span>4xx–5xx</span><strong>${formatNumber(state.traffic.errors)}</strong></div>
        <div><span>last endpoint</span><strong>${escapeHTML(state.traffic.lastBackend)}</strong></div>
      </div>
      <div class="distribution-list">
        ${
          distribution.length
            ? distribution
                .map(
                  ([backend, count]) => `<div><code>${escapeHTML(backend)}</code><span><i style="width:${(count / max) * 100}%"></i></span><b>${count}</b></div>`,
                )
                .join('')
            : '<p class="muted-copy">Распределение появится после первого успешного request.</p>'
        }
      </div>
    </section>
    <section class="panel rate-limit-panel">
      <header class="panel-header compact">
        <div><span class="eyebrow">Runtime protection</span><h2>Global token bucket</h2></div>
        <span class="contract-chip">PATCH /api/v1/rate-limit</span>
      </header>
      <form id="rate-limit-form" class="rate-limit-form">
        <label class="switch-field"><input type="checkbox" data-rate-limit-field="enabled" ${rateDraft.enabled ? 'checked' : ''} ${!canTuneRateLimit ? 'disabled' : ''}/><span><b>Enabled</b><small>Apply before new requests</small></span></label>
        <label><span>Capacity</span><input type="number" min="1" step="1" data-rate-limit-field="capacity" value="${rateDraft.capacity}" ${!canTuneRateLimit ? 'disabled' : ''}/></label>
        <label><span>Refill / second</span><input type="number" min="0.001" step="0.001" data-rate-limit-field="refillPerSecond" value="${rateDraft.refillPerSecond}" ${!canTuneRateLimit ? 'disabled' : ''}/></label>
        <label><span>Storage failure</span><select data-rate-limit-field="failureMode" ${!canTuneRateLimit ? 'disabled' : ''}>
          ${['fail-open', 'fail-closed', 'local-fallback'].map((mode) => `<option ${rateDraft.failureMode === mode ? 'selected' : ''}>${mode}</option>`).join('')}
        </select></label>
        <div class="rate-limit-runtime"><span>reported buckets <b>${formatNumber(rateLimit?.local_buckets)}</b></span><span>evictions <b>${formatNumber(rateLimit?.local_evictions)}</b></span></div>
        <div class="rate-limit-actions">
          <button class="button button--primary" type="submit" ${!canTuneRateLimit || !state.rateLimitDirty ? 'disabled' : ''}>Apply override</button>
          <button class="button" type="button" data-action="reset-rate-limit" ${!canTuneRateLimit ? 'disabled' : ''}>Reset my bucket</button>
        </div>
      </form>
      <p class="runtime-warning">Admin-only runtime override. Он действует на текущем instance, не изменяет GatewayConfig и будет потерян при restart. Reset относится только к bucket текущего verified client. Для нескольких replicas применяйте одинаковую настройку через deployment automation.</p>
    </section>
  `
}
