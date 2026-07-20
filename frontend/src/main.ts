import './styles.css'

type StatusCode = number
type AppMode = 'demo' | 'live'

const appMode: AppMode = import.meta.env.VITE_APP_MODE === 'demo' ? 'demo' : 'live'
const isDemoMode = appMode === 'demo'

interface BackendNode {
  id: string
  number: string
  url: string
  alive: boolean
  healthy: boolean
  enabled: boolean
  handled: number
}

interface DashboardStatus {
  mode: 'live'
  strategy: string
  health_interval: string
  health_timeout: string
  client_ip: string
  backends: Array<{
    id: string
    url: string
    healthy: boolean
    enabled: boolean
    available: boolean
    requests: number
  }>
  bucket: {
    capacity: number
    tokens: number
    rate: string
  }
}

interface EventRecord {
  id: number
  time: string
  code: StatusCode | 'SYS'
  title: string
  detail: string
}

interface SimulationState {
  requestId: number
  capacity: number
  tokens: number
  total: number
  success: number
  rejected: number
  unavailable: number
  latencies: number[]
  events: EventRecord[]
  autoTraffic: boolean
  roundRobinCounter: number
}

const backends: BackendNode[] = [
  { id: 'backend1', number: '01', url: 'backend1:80', alive: isDemoMode, healthy: isDemoMode, enabled: true, handled: 0 },
  { id: 'backend2', number: '02', url: 'backend2:80', alive: isDemoMode, healthy: isDemoMode, enabled: true, handled: 0 },
]

const state: SimulationState = {
  requestId: 0,
  capacity: isDemoMode ? 8 : 100,
  tokens: isDemoMode ? 8 : 100,
  total: 0,
  success: 0,
  rejected: 0,
  unavailable: 0,
  latencies: [],
  events: [],
  autoTraffic: false,
  roundRobinCounter: 0,
}

const app = document.querySelector<HTMLDivElement>('#app')

if (!app) {
  throw new Error('App root was not found')
}

app.innerHTML = `
  <div class="site-shell">
    <header class="topbar">
      <a class="brand" href="#top" aria-label="Balancer Lab — наверх">
        <span class="brand-mark" aria-hidden="true">
          <svg viewBox="0 0 32 32" role="img">
            <path d="M7 9h8l3 4h7M7 23h8l3-4h7" />
            <circle cx="6" cy="9" r="2" />
            <circle cx="6" cy="23" r="2" />
            <circle cx="26" cy="13" r="2" />
            <circle cx="26" cy="19" r="2" />
          </svg>
        </span>
        <span class="brand-copy">
          <strong>Balancer</strong>
          <small>Go / HTTP</small>
        </span>
      </a>

      <nav class="desktop-nav" aria-label="Навигация">
        <a href="#simulator">Симулятор</a>
        <a href="#architecture">Обработка запроса</a>
        <a href="#config">Конфигурация</a>
      </nav>

      <div class="runtime-state is-connecting" id="runtime-state" title="Соединение с Go API">
        <span class="live-pulse" aria-hidden="true"></span>
        <span id="runtime-label">Backend connecting</span>
        <span class="runtime-version" id="runtime-version">API</span>
      </div>
    </header>

    <main id="top">
      <section class="hero" aria-labelledby="hero-title">
        <div class="hero-copy">
          <p class="eyebrow"><span>01</span> Go HTTP load balancer</p>
          <h1 id="hero-title">HTTP-балансировщик<br />на <em>Go</em></h1>
          <p class="hero-lead" id="hero-lead">
            Проект распределяет HTTP-запросы между backend-серверами по round-robin,
            ограничивает частоту по IP и исключает недоступные ноды по TCP health-check.
          </p>
          <div class="hero-actions">
            <a class="primary-link" href="#simulator">
              Перейти к визуализации
              <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M4 10h11M11 6l4 4-4 4" /></svg>
            </a>
            <span class="hero-note" id="hero-note">Режим live · данные получаются из Go API</span>
          </div>
        </div>

        <div class="hero-aside" aria-label="Параметры реализации">
          <div class="hero-orbit" aria-hidden="true">
            <span class="orbit-dot orbit-dot-one"></span>
            <span class="orbit-dot orbit-dot-two"></span>
            <span class="orbit-core">RR</span>
          </div>
          <div class="spec-list">
            <div><span>Strategy</span><strong>Round-robin</strong></div>
            <div><span>Health probe</span><strong>TCP · 5 sec</strong></div>
            <div><span>Runtime</span><strong>Go 1.24</strong></div>
          </div>
        </div>
      </section>

      <section class="simulator-section" id="simulator" aria-labelledby="simulator-title">
        <div class="section-index" aria-hidden="true">REQUEST FLOW / 01</div>
        <div class="console-frame">
          <div class="console-heading">
            <div>
              <p class="kicker">Request routing</p>
              <h2 id="simulator-title">Обработка HTTP-запроса</h2>
            </div>
            <div class="console-endpoint" aria-label="Тестовый endpoint">
              <span class="method">GET</span>
              <code id="endpoint-address">localhost:8080/</code>
              <button class="icon-button copy-button" type="button" aria-label="Скопировать адрес" title="Скопировать адрес">
                <svg viewBox="0 0 20 20" aria-hidden="true"><rect x="7" y="7" width="9" height="9" rx="1" /><path d="M13 7V4H4v9h3" /></svg>
              </button>
            </div>
          </div>

          <div class="lab-grid">
            <div class="network-stage" aria-label="Схема прохождения запросов">
              <div class="stage-topline">
                <span><i class="legend-dot legend-client"></i>Клиент</span>
                <span><i class="legend-dot legend-route"></i>Маршрут запроса</span>
                <span class="next-health">Health-check через <b id="health-countdown">5</b> сек</span>
              </div>

              <svg class="routes" viewBox="0 0 900 390" preserveAspectRatio="none" aria-hidden="true">
                <defs>
                  <filter id="packetGlow" x="-200%" y="-200%" width="500%" height="500%">
                    <feGaussianBlur stdDeviation="4" result="blur" />
                    <feMerge><feMergeNode in="blur" /><feMergeNode in="SourceGraphic" /></feMerge>
                  </filter>
                </defs>
                <path class="route-line route-ingress" d="M150 193 C220 193 255 193 330 193" />
                <path id="route-backend-1" class="route-line" d="M150 193 C220 193 255 193 330 193 C480 193 500 92 650 92" />
                <path id="route-backend-2" class="route-line" d="M150 193 C220 193 255 193 330 193 C480 193 500 278 650 278" />
                <path id="route-blocked" class="route-line route-hidden" d="M150 193 C220 193 255 193 390 193" />
                <path class="database-route" d="M410 235 L410 314" />
                <g class="route-junction"><circle cx="502" cy="193" r="4" /><circle cx="502" cy="193" r="9" /></g>
                <g id="packet-layer"></g>
              </svg>

              <div class="network-node client-node">
                <div class="node-icon client-icon" aria-hidden="true">
                  <svg viewBox="0 0 24 24"><rect x="3" y="5" width="18" height="12" rx="2" /><path d="M8 21h8M12 17v4" /></svg>
                </div>
                <span class="node-label">Client IP</span>
                <strong id="client-ip">connecting…</strong>
                <small>HTTP request</small>
              </div>

              <div class="network-node balancer-node" id="balancer-node">
                <div class="balancer-head">
                  <div>
                    <span class="node-label">Load balancer</span>
                    <strong>:8080</strong>
                  </div>
                  <span class="go-badge">GO</span>
                </div>
                <div class="strategy-row">
                  <span>Strategy</span>
                  <b>Round-robin</b>
                </div>
                <div class="bucket-block">
                  <div class="bucket-copy">
                    <span>Token bucket</span>
                    <strong><b id="token-count">100</b> / <b id="token-capacity">100</b></strong>
                  </div>
                  <div class="bucket-rail" role="progressbar" aria-label="Доступные токены" aria-valuemin="0" aria-valuemax="100" aria-valuenow="100">
                    <span id="bucket-fill"></span>
                  </div>
                </div>
              </div>

              <button class="network-node backend-node backend-one" type="button" data-backend="0" aria-pressed="false" aria-label="Остановить backend 1">
                <span class="backend-number">01</span>
                <span class="backend-copy">
                  <span class="node-label">Backend</span>
                  <strong>backend1:80</strong>
                  <small><i class="status-dot"></i><span class="backend-status">Connecting</span> · <span class="backend-latency">0 req</span></small>
                </span>
                <span class="power-icon" title="Переключить доступность">
                  <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M10 2v7M5.1 5.1a7 7 0 1 0 9.8 0" /></svg>
                </span>
              </button>

              <button class="network-node backend-node backend-two" type="button" data-backend="1" aria-pressed="false" aria-label="Остановить backend 2">
                <span class="backend-number">02</span>
                <span class="backend-copy">
                  <span class="node-label">Backend</span>
                  <strong>backend2:80</strong>
                  <small><i class="status-dot"></i><span class="backend-status">Connecting</span> · <span class="backend-latency">0 req</span></small>
                </span>
                <span class="power-icon" title="Переключить доступность">
                  <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M10 2v7M5.1 5.1a7 7 0 1 0 9.8 0" /></svg>
                </span>
              </button>

              <div class="database-node">
                <svg viewBox="0 0 24 24" aria-hidden="true"><ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v7c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 12v7c0 1.7 3.6 3 8 3s8-1.3 8-3v-7" /></svg>
                <span><strong>PostgreSQL</strong><small>bucket state</small></span>
              </div>
            </div>

            <aside class="activity-panel" aria-labelledby="activity-title">
              <div class="activity-head">
                <div>
                  <p class="kicker">Event log</p>
                  <h3 id="activity-title">Результаты запросов</h3>
                </div>
                <button class="text-button" id="clear-log" type="button">Очистить</button>
              </div>
              <div class="event-list" id="event-list" aria-live="polite"></div>
              <div class="activity-empty" id="activity-empty">
                <span class="empty-cross" aria-hidden="true"></span>
                <p>После отправки здесь появятся HTTP-код, выбранный backend и задержка.</p>
              </div>
            </aside>
          </div>

          <div class="control-deck">
            <div class="traffic-controls">
              <button class="send-button" id="send-request" type="button">
                <span class="send-icon">
                  <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M3 10h12M11 5l5 5-5 5" /></svg>
                </span>
                Отправить запрос
                <kbd>↵</kbd>
              </button>
              <button class="secondary-button" id="burst-request" type="button">Burst ×10</button>
              <button class="secondary-button auto-button" id="auto-traffic" type="button" aria-pressed="false">
                <span class="auto-indicator"></span> Автотрафик
              </button>
            </div>

            <div class="profile-control">
              <label for="capacity-select">Профиль bucket</label>
              <select id="capacity-select">
                <option value="8">Demo · 8</option>
                <option value="12">Demo · 12</option>
                <option value="100" selected>Config · 100</option>
              </select>
            </div>

            <button class="reset-button" id="reset-simulation" type="button" title="Сбросить состояние">
              <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M16 7a7 7 0 1 0 .4 5M16 3v4h-4" /></svg>
              Сбросить
            </button>
          </div>

          <div class="metrics-strip" aria-label="Метрики live-сессии">
            <div class="metric-item">
              <span>Всего запросов</span>
              <strong id="metric-total">0</strong>
              <small>Текущая сессия</small>
            </div>
            <div class="metric-item">
              <span>Успешно</span>
              <strong id="metric-success">0</strong>
              <small id="success-rate">0% success rate</small>
            </div>
            <div class="metric-item">
              <span>Отклонено</span>
              <strong id="metric-rejected">0</strong>
              <small>HTTP 429 / 503</small>
            </div>
            <div class="metric-item latency-metric">
              <span>Средняя задержка</span>
              <strong><b id="metric-latency">—</b><sup>ms</sup></strong>
              <div class="sparkline" id="sparkline" aria-hidden="true"></div>
            </div>
          </div>
        </div>
      </section>

      <section class="architecture" id="architecture" aria-labelledby="architecture-title">
        <div class="architecture-intro">
          <p class="eyebrow"><span>02</span> Processing pipeline</p>
          <h2 id="architecture-title">Порядок обработки<br />запроса</h2>
          <p>Запрос последовательно проходит rate limit middleware, выбор backend и reverse proxy.</p>
        </div>

        <div class="flow-steps">
          <article class="flow-step">
            <span class="step-number">01</span>
            <div class="step-rule"></div>
            <div>
              <p class="kicker">Gate</p>
              <h3>Token bucket</h3>
              <p>Ключ лимита — IP клиента. Каждый запрос расходует один токен. При пустом bucket middleware возвращает <code>429</code> до выбора backend.</p>
            </div>
            <span class="step-result">allow()</span>
          </article>
          <article class="flow-step">
            <span class="step-number">02</span>
            <div class="step-rule"></div>
            <div>
              <p class="kicker">Route</p>
              <h3>Round-robin</h3>
              <p>Стратегия последовательно выбирает следующую доступную ноду. Backend с отрицательным health-state или отключённый вручную пропускается.</p>
            </div>
            <span class="step-result">next % alive</span>
          </article>
          <article class="flow-step">
            <span class="step-number">03</span>
            <div class="step-rule"></div>
            <div>
              <p class="kicker">Forward</p>
              <h3>Reverse proxy</h3>
              <p><code>httputil.ReverseProxy</code> пересылает запрос на выбранный backend. Если доступных нод нет, клиент получает <code>503</code>.</p>
            </div>
            <span class="step-result">ServeHTTP()</span>
          </article>
        </div>
      </section>

      <section class="config-section" id="config" aria-labelledby="config-title">
        <div class="config-panel">
          <div class="config-heading">
            <div>
              <p class="kicker">config/config.yaml</p>
              <h2 id="config-title">Параметры конфигурации</h2>
            </div>
            <span class="config-state"><i></i> Valid configuration</span>
          </div>
          <pre aria-label="YAML-конфигурация"><code><span class="code-key">server:</span>
  port: <span class="code-value">"8080"</span>

<span class="code-key">backends:</span>
  - <span class="code-value">"http://backend1:80"</span>
  - <span class="code-value">"http://backend2:80"</span>

<span class="code-key">rate_limit:</span>
  default_capacity: <span class="code-number">100</span>
  default_rate: <span class="code-value">"1s"</span></code></pre>
          <p class="config-note" id="config-note"><span>!</span> В режиме <code>live</code> выбор профиля отправляет запрос в API и сбрасывает bucket текущего клиента до 8, 12 или 100 токенов.</p>
        </div>

        <div class="health-panel">
          <p class="eyebrow"><span>03</span> Health-check</p>
          <h2>TCP-проверка<br />каждые 5 секунд</h2>
          <p>Балансировщик открывает TCP-соединение с host:port backend'а с таймаутом 2 секунды. HTTP-код ответа не проверяется.</p>
          <div class="health-visual" aria-hidden="true">
            <span class="health-ring ring-one"></span>
            <span class="health-ring ring-two"></span>
            <span class="health-ring ring-three"></span>
            <span class="health-core"><b>5</b><small>SEC</small></span>
          </div>
          <div class="health-foot"><span>net.DialTimeout</span><strong>2s timeout</strong></div>
        </div>
      </section>
    </main>

    <footer>
      <div class="footer-mark">BALANCER<span>/</span>LAB</div>
      <p>Документация и визуализация Go HTTP-балансировщика.</p>
      <a href="#top">Наверх <span>↑</span></a>
    </footer>
  </div>

  <div class="toast" id="toast" role="status" aria-live="polite"></div>
`

function query<T extends Element>(selector: string): T {
  const element = document.querySelector<T>(selector)
  if (!element) throw new Error(`Element not found: ${selector}`)
  return element
}

const elements = {
  tokenCount: query<HTMLElement>('#token-count'),
  tokenCapacity: query<HTMLElement>('#token-capacity'),
  bucketFill: query<HTMLElement>('#bucket-fill'),
  bucketRail: query<HTMLElement>('.bucket-rail'),
  total: query<HTMLElement>('#metric-total'),
  success: query<HTMLElement>('#metric-success'),
  rejected: query<HTMLElement>('#metric-rejected'),
  successRate: query<HTMLElement>('#success-rate'),
  latency: query<HTMLElement>('#metric-latency'),
  sparkline: query<HTMLElement>('#sparkline'),
  eventList: query<HTMLElement>('#event-list'),
  activityEmpty: query<HTMLElement>('#activity-empty'),
  packetLayer: query<SVGGElement>('#packet-layer'),
  balancer: query<HTMLElement>('#balancer-node'),
  autoButton: query<HTMLButtonElement>('#auto-traffic'),
  sendButton: query<HTMLButtonElement>('#send-request'),
  burstButton: query<HTMLButtonElement>('#burst-request'),
  toast: query<HTMLElement>('#toast'),
  healthCountdown: query<HTMLElement>('#health-countdown'),
  runtimeState: query<HTMLElement>('#runtime-state'),
  runtimeLabel: query<HTMLElement>('#runtime-label'),
  runtimeVersion: query<HTMLElement>('#runtime-version'),
  heroLead: query<HTMLElement>('#hero-lead'),
  heroNote: query<HTMLElement>('#hero-note'),
  endpointAddress: query<HTMLElement>('#endpoint-address'),
  configNote: query<HTMLElement>('#config-note'),
  clientIP: query<HTMLElement>('#client-ip'),
  capacitySelect: query<HTMLSelectElement>('#capacity-select'),
}

let autoTrafficTimer: number | undefined
let toastTimer: number | undefined
let statusRefreshTimer: number | undefined
let healthCountdown = 5
let backendConnected = false

function formatTime(): string {
  return new Intl.DateTimeFormat('ru-RU', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date())
}

function addEvent(code: EventRecord['code'], title: string, detail: string): void {
  state.events.unshift({ id: Date.now() + Math.random(), time: formatTime(), code, title, detail })
  state.events = state.events.slice(0, 9)
  renderEvents()
}

function renderEvents(): void {
  elements.activityEmpty.hidden = state.events.length > 0
  elements.eventList.replaceChildren()

  state.events.forEach((record) => {
    const event = document.createElement('div')
    const statusClass = record.code === 200 ? 'event-ok' : record.code === 'SYS' ? 'event-system' : 'event-error'
    event.className = `event-row ${statusClass}`
    event.innerHTML = `
      <span class="event-time">${record.time}</span>
      <span class="event-code">${record.code}</span>
      <span class="event-copy"><strong>${record.title}</strong><small>${record.detail}</small></span>
    `
    elements.eventList.append(event)
  })
}

function animatePacket(backendIndex: number | null, blocked = false): void {
  const svgNamespace = 'http://www.w3.org/2000/svg'
  const circle = document.createElementNS(svgNamespace, 'circle')
  const motion = document.createElementNS(svgNamespace, 'animateMotion')
  const mpath = document.createElementNS(svgNamespace, 'mpath')
  const routeId = backendIndex === null ? 'route-blocked' : `route-backend-${backendIndex + 1}`

  circle.setAttribute('r', blocked ? '5' : '5.5')
  circle.setAttribute('class', blocked ? 'request-packet blocked-packet' : 'request-packet')
  circle.setAttribute('filter', 'url(#packetGlow)')
  motion.setAttribute('dur', backendIndex === null ? '420ms' : '720ms')
  motion.setAttribute('fill', 'freeze')
  motion.setAttribute('calcMode', 'spline')
  motion.setAttribute('keySplines', '0.45 0 0.2 1')
  mpath.setAttribute('href', `#${routeId}`)
  motion.append(mpath)
  circle.append(motion)
  elements.packetLayer.append(circle)
  motion.beginElement()

  elements.balancer.classList.remove('node-pulse')
  void elements.balancer.offsetWidth
  elements.balancer.classList.add('node-pulse')

  if (backendIndex !== null) {
    const backendElement = query<HTMLElement>(`[data-backend="${backendIndex}"]`)
    window.setTimeout(() => backendElement.classList.add('request-hit'), 500)
    window.setTimeout(() => backendElement.classList.remove('request-hit'), 980)
  }

  window.setTimeout(() => circle.remove(), 1000)
}

function setConnectionState(connected: boolean): void {
  if (isDemoMode) {
    backendConnected = true
    elements.runtimeState.classList.remove('is-offline', 'is-connecting')
    elements.runtimeState.title = 'Demo mode без подключения к API'
    elements.runtimeLabel.textContent = 'Demo mode'
    elements.runtimeVersion.textContent = 'STATIC'
    elements.heroLead.textContent =
      'Браузер локально воспроизводит token bucket, round-robin и состояние двух backend-серверов. Запросы в Go API не отправляются.'
    elements.heroNote.textContent = 'Режим demo · API и PostgreSQL не используются'
    elements.endpointAddress.textContent = 'demo://round-robin'
    elements.configNote.innerHTML =
      '<span>!</span> В режиме <code>demo</code> интерфейс использует локальную модель этих параметров. Режим <code>live</code> читает и изменяет состояние через Go API.'
    elements.clientIP.textContent = '192.168.1.24'
    elements.capacitySelect.value = String(state.capacity)
    elements.sendButton.disabled = false
    elements.burstButton.disabled = false
    elements.autoButton.disabled = false
    renderBackends()
    return
  }

  backendConnected = connected
  elements.runtimeState.classList.toggle('is-offline', !connected)
  elements.runtimeState.classList.toggle('is-connecting', false)
  elements.runtimeState.title = connected ? 'Соединено с Go API' : 'Нет соединения с Go API'
  elements.runtimeLabel.textContent = connected ? 'Live mode' : 'API unavailable'
  elements.runtimeVersion.textContent = connected ? 'API / LIVE' : 'RETRYING'
  elements.heroLead.textContent =
    'Интерфейс отправляет запросы в Go-балансировщик и показывает выбранный backend, остаток token bucket, HTTP-код и TCP health-state.'
  elements.heroNote.textContent = connected
    ? 'Режим live · данные получаются из /api/dashboard/*'
    : 'Go API недоступен · запустите docker compose up --build'
  elements.endpointAddress.textContent = 'localhost:8080/'
  elements.configNote.innerHTML =
    '<span>!</span> В режиме <code>live</code> выбор профиля отправляет запрос в API и сбрасывает bucket текущего клиента до 8, 12 или 100 токенов.'
  elements.sendButton.disabled = !connected
  elements.burstButton.disabled = !connected
  elements.autoButton.disabled = !connected
  if (!connected && state.autoTraffic) {
    window.clearInterval(autoTrafficTimer)
    state.autoTraffic = false
    elements.autoButton.classList.remove('is-active')
    elements.autoButton.setAttribute('aria-pressed', 'false')
  }
  renderBackends()
}

async function refreshStatus(announce = false): Promise<boolean> {
  if (isDemoMode) {
    const wasConnected = backendConnected
    setConnectionState(true)
    renderState()
    if (announce && !wasConnected) {
      addEvent('SYS', 'Демо-режим готов', 'автономная модель · API не требуется')
    }
    return true
  }

  try {
    const response = await fetch('/api/dashboard/status', { cache: 'no-store' })
    if (!response.ok) throw new Error(`Status API returned ${response.status}`)

    const dashboard = (await response.json()) as DashboardStatus
    state.capacity = dashboard.bucket.capacity
    state.tokens = dashboard.bucket.tokens
    if (Array.from(elements.capacitySelect.options).some((option) => Number(option.value) === state.capacity)) {
      elements.capacitySelect.value = String(state.capacity)
    }
    elements.clientIP.textContent = dashboard.client_ip

    dashboard.backends.forEach((snapshot) => {
      const backend = backends.find((item) => item.id === snapshot.id)
      if (!backend) return
      backend.url = snapshot.url
      backend.healthy = snapshot.healthy
      backend.enabled = snapshot.enabled
      backend.alive = snapshot.available
      backend.handled = snapshot.requests
    })

    const wasConnected = backendConnected
    setConnectionState(true)
    renderState()
    if (announce && !wasConnected) {
      addEvent('SYS', 'Go backend подключён', `${dashboard.strategy} · health ${dashboard.health_interval}`)
    }
    return true
  } catch {
    const wasConnected = backendConnected
    setConnectionState(false)
    if (announce || wasConnected) {
      addEvent('SYS', 'Go backend недоступен', 'ожидается API на /api/dashboard/status')
    }
    return false
  }
}

function queueStatusRefresh(): void {
  if (isDemoMode) return
  window.clearTimeout(statusRefreshTimer)
  statusRefreshTimer = window.setTimeout(() => void refreshStatus(), 500)
}

function pickDemoBackend(): number | null {
  const available = backends
    .map((backend, index) => ({ backend, index }))
    .filter(({ backend }) => backend.alive && backend.enabled)

  if (available.length === 0) return null
  const selected = available[state.roundRobinCounter % available.length]
  state.roundRobinCounter += 1
  return selected.index
}

function sendDemoRequest(): void {
  state.requestId += 1
  state.total += 1
  const requestLabel = `req_${String(state.requestId).padStart(3, '0')}`

  if (state.tokens <= 0) {
    state.rejected += 1
    animatePacket(null, true)
    addEvent(429, `${requestLabel} заблокирован`, 'демо token bucket исчерпан')
    showToast('429 · Too Many Requests', 'error')
    renderState()
    return
  }

  state.tokens -= 1
  const backendIndex = pickDemoBackend()
  if (backendIndex === null) {
    state.unavailable += 1
    animatePacket(null, true)
    addEvent(503, `${requestLabel} без маршрута`, 'в демо-pool нет доступных backend’ов')
    showToast('503 · Service Unavailable', 'error')
    renderState()
    return
  }

  const backend = backends[backendIndex]
  const latency = 17 + backendIndex * 7 + Math.floor(Math.random() * 8)
  backend.handled += 1
  state.success += 1
  state.latencies.push(latency)
  state.latencies = state.latencies.slice(-20)
  animatePacket(backendIndex)
  addEvent(200, `${requestLabel} → backend ${backend.number}`, `${backend.url} · ${latency} ms · simulated`)
  renderState()
}

async function sendRequest(): Promise<void> {
  if (isDemoMode) {
    sendDemoRequest()
    return
  }

  state.requestId += 1
  const requestLabel = `req_${String(state.requestId).padStart(3, '0')}`
  const startedAt = performance.now()

  try {
    const response = await fetch(`/api/dashboard/request?request=${encodeURIComponent(requestLabel)}`, {
      cache: 'no-store',
      headers: { Accept: 'text/html, application/json' },
    })
    await response.text()

    const latency = Math.max(1, Math.round(performance.now() - startedAt))
    const remaining = Number(response.headers.get('X-RateLimit-Remaining'))
    const capacity = Number(response.headers.get('X-RateLimit-Limit'))
    if (Number.isFinite(remaining)) state.tokens = remaining
    if (Number.isFinite(capacity) && capacity > 0) state.capacity = capacity

    state.total += 1

    if (response.ok) {
      const backendHost = response.headers.get('X-Balancer-Backend') ?? ''
      const backendIndex = backends.findIndex((backend) => backend.url === backendHost)
      const backend = backends[backendIndex]

      state.success += 1
      state.latencies.push(latency)
      state.latencies = state.latencies.slice(-20)
      animatePacket(backendIndex >= 0 ? backendIndex : null)
      addEvent(
        response.status,
        `${requestLabel} → ${backend ? `backend ${backend.number}` : backendHost || 'backend'}`,
        `${backendHost || 'proxied'} · ${latency} ms`,
      )
    } else {
      if (response.status === 429) state.rejected += 1
      if (response.status === 503) state.unavailable += 1
      animatePacket(null, true)
      const title = response.status === 429 ? `${requestLabel} заблокирован` : `${requestLabel} без маршрута`
      const detail = response.status === 429 ? 'реальный token bucket исчерпан' : `Go backend вернул HTTP ${response.status}`
      addEvent(response.status, title, detail)
      showToast(`${response.status} · ${response.statusText}`, 'error')
    }

    renderState()
    queueStatusRefresh()
  } catch {
    setConnectionState(false)
    addEvent('SYS', `${requestLabel} не отправлен`, 'нет соединения с Go backend')
    showToast('Go backend недоступен', 'error')
  }
}

function renderState(): void {
  elements.tokenCount.textContent = String(state.tokens)
  elements.tokenCapacity.textContent = String(state.capacity)
  elements.bucketFill.style.width = `${(state.tokens / state.capacity) * 100}%`
  elements.bucketRail.setAttribute('aria-valuemax', String(state.capacity))
  elements.bucketRail.setAttribute('aria-valuenow', String(state.tokens))
  elements.bucketRail.classList.toggle('is-low', state.tokens / state.capacity <= 0.25)

  elements.total.textContent = String(state.total)
  elements.success.textContent = String(state.success)
  elements.rejected.textContent = String(state.rejected + state.unavailable)
  elements.successRate.textContent = `${state.total === 0 ? 0 : Math.round((state.success / state.total) * 100)}% success rate`

  const averageLatency = state.latencies.length
    ? Math.round(state.latencies.reduce((total, value) => total + value, 0) / state.latencies.length)
    : null
  elements.latency.textContent = averageLatency === null ? '—' : String(averageLatency)

  renderBackends()
  renderSparkline()
}

function renderBackends(): void {
  backends.forEach((backend, index) => {
    const element = query<HTMLButtonElement>(`[data-backend="${index}"]`)
    const status = element.querySelector<HTMLElement>('.backend-status')
    const latency = element.querySelector<HTMLElement>('.backend-latency')
    if (!status || !latency) return

    element.classList.toggle('is-offline', !backend.alive)
    element.classList.toggle('is-disabled', !backend.enabled)
    element.disabled = !backendConnected
    element.setAttribute('aria-pressed', String(!backend.enabled))
    element.setAttribute('aria-label', `${backend.enabled ? 'Исключить' : 'Вернуть'} backend ${index + 1}`)
    status.textContent = !backend.enabled ? 'Disabled' : backend.healthy ? 'Healthy' : 'Unhealthy'
    latency.textContent = `${backend.handled} req`
  })
}

function renderSparkline(): void {
  elements.sparkline.replaceChildren()
  const values = state.latencies.slice(-12)

  for (let index = 0; index < 12; index += 1) {
    const bar = document.createElement('span')
    const value = values[index]
    bar.style.height = value ? `${Math.min(100, 18 + value * 2.3)}%` : `${16 + ((index * 13) % 22)}%`
    bar.classList.toggle('is-placeholder', value === undefined)
    elements.sparkline.append(bar)
  }
}

function showToast(message: string, tone: 'default' | 'error' = 'default'): void {
  window.clearTimeout(toastTimer)
  elements.toast.textContent = message
  elements.toast.className = `toast is-visible ${tone === 'error' ? 'toast-error' : ''}`
  toastTimer = window.setTimeout(() => {
    elements.toast.classList.remove('is-visible')
  }, 1800)
}

function toggleAutoTraffic(): void {
  state.autoTraffic = !state.autoTraffic
  elements.autoButton.classList.toggle('is-active', state.autoTraffic)
  elements.autoButton.setAttribute('aria-pressed', String(state.autoTraffic))

  if (state.autoTraffic) {
    void sendRequest()
    autoTrafficTimer = window.setInterval(() => void sendRequest(), 1400)
    showToast('Автотрафик включён')
  } else {
    window.clearInterval(autoTrafficTimer)
    showToast('Автотрафик остановлен')
  }
}

async function resetSimulation(): Promise<void> {
  window.clearInterval(autoTrafficTimer)
  state.requestId = 0
  state.roundRobinCounter = 0
  state.total = 0
  state.success = 0
  state.rejected = 0
  state.unavailable = 0
  state.latencies = []
  state.events = []
  state.autoTraffic = false
  elements.autoButton.classList.remove('is-active')
  elements.autoButton.setAttribute('aria-pressed', 'false')
  renderEvents()
  renderState()

  if (isDemoMode) {
    state.tokens = state.capacity
    backends.forEach((backend) => {
      backend.enabled = true
      backend.healthy = true
      backend.alive = true
      backend.handled = 0
    })
    renderState()
    showToast('Демо-состояние сброшено')
    return
  }

  try {
    const requests = [
      fetch('/api/dashboard/limit', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ capacity: state.capacity }),
      }),
      ...backends.map((backend) =>
        fetch(`/api/dashboard/backends/${encodeURIComponent(backend.id)}`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled: true }),
        }),
      ),
    ]
    const responses = await Promise.all(requests)
    if (responses.some((response) => !response.ok)) throw new Error('Reset API failed')
    await refreshStatus()
    showToast('Лаборатория сброшена')
  } catch {
    showToast('Не удалось сбросить backend', 'error')
  }
}

elements.sendButton.addEventListener('click', () => void sendRequest())

query<HTMLButtonElement>('#burst-request').addEventListener('click', () => {
  for (let index = 0; index < 10; index += 1) {
    void sendRequest()
  }
})

elements.autoButton.addEventListener('click', toggleAutoTraffic)
query<HTMLButtonElement>('#reset-simulation').addEventListener('click', () => void resetSimulation())

query<HTMLSelectElement>('#capacity-select').addEventListener('change', async (event) => {
  const select = event.currentTarget as HTMLSelectElement
  const capacity = Number(select.value)

  if (isDemoMode) {
    state.capacity = capacity
    state.tokens = capacity
    renderState()
    addEvent('SYS', 'Демо bucket сброшен', `${capacity} tokens · refill 1 token/sec`)
    showToast(`Демо bucket: ${capacity} токенов`)
    return
  }

  try {
    const response = await fetch('/api/dashboard/limit', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ capacity }),
    })
    if (!response.ok) throw new Error('Limit API failed')
    const bucket = (await response.json()) as DashboardStatus['bucket']
    state.capacity = bucket.capacity
    state.tokens = bucket.tokens
    renderState()
    addEvent('SYS', 'Token bucket сброшен', `${bucket.capacity} tokens · refill ${bucket.rate}`)
    showToast(`Реальный bucket: ${bucket.capacity} токенов`)
  } catch {
    showToast('Не удалось изменить bucket', 'error')
    await refreshStatus()
    select.value = String(state.capacity)
  }
})

document.querySelectorAll<HTMLButtonElement>('[data-backend]').forEach((element) => {
  element.addEventListener('click', async () => {
    const index = Number(element.dataset.backend)
    const backend = backends[index]
    const enabled = !backend.enabled

    if (isDemoMode) {
      backend.enabled = enabled
      backend.alive = backend.healthy && enabled
      renderBackends()
      addEvent(
        'SYS',
        `Backend ${backend.number}: ${enabled ? 'enabled' : 'disabled'}`,
        enabled ? 'возвращён в демо-pool' : 'исключён из демо-pool',
      )
      showToast(`Backend ${backend.number}: ${enabled ? 'enabled' : 'disabled'}`)
      return
    }

    element.disabled = true

    try {
      const response = await fetch(`/api/dashboard/backends/${encodeURIComponent(backend.id)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled }),
      })
      if (!response.ok) throw new Error('Backend state API failed')
      backend.enabled = enabled
      backend.alive = backend.healthy && enabled
      renderBackends()
      addEvent('SYS', `Backend ${backend.number}: ${enabled ? 'enabled' : 'disabled'}`, enabled ? 'возвращён в реальный pool' : 'исключён из реального pool')
      showToast(`Backend ${backend.number}: ${enabled ? 'enabled' : 'disabled'}`)
      queueStatusRefresh()
    } catch {
      showToast('Не удалось изменить backend', 'error')
      await refreshStatus()
    }
  })
})

query<HTMLButtonElement>('#clear-log').addEventListener('click', () => {
  state.events = []
  renderEvents()
})

query<HTMLButtonElement>('.copy-button').addEventListener('click', async () => {
  const address = isDemoMode ? 'demo://round-robin' : 'http://localhost:8080/'
  try {
    await navigator.clipboard.writeText(address)
    showToast('Адрес скопирован')
  } catch {
    showToast(address)
  }
})

document.addEventListener('keydown', (event) => {
  const target = event.target as HTMLElement
  if (event.key === 'Enter' && !['SELECT', 'BUTTON', 'A'].includes(target.tagName)) {
    event.preventDefault()
    void sendRequest()
  }
})

window.setInterval(() => {
  healthCountdown -= 1
  if (healthCountdown === 0) {
    healthCountdown = 5
    document.querySelectorAll<HTMLElement>('.backend-node').forEach((node) => {
      node.classList.add('health-scan')
      window.setTimeout(() => node.classList.remove('health-scan'), 650)
    })
    if (!isDemoMode) void refreshStatus()
  }
  elements.healthCountdown.textContent = String(healthCountdown)
}, 1000)

window.setInterval(() => {
  if (!isDemoMode || state.tokens >= state.capacity) return
  state.tokens += 1
  renderState()
}, 1000)

renderEvents()
renderState()
if (isDemoMode) {
  void refreshStatus(true)
} else {
  setConnectionState(false)
  void refreshStatus(true)
  window.setInterval(() => void refreshStatus(), 2000)
}
