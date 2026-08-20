import './styles.css'

type StatusCode = number
type AppMode = 'demo' | 'live'

const appMode: AppMode = import.meta.env.VITE_APP_MODE === 'demo' ? 'demo' : 'live'
const isDemoMode = appMode === 'demo'
const publicBalancerURL = String(import.meta.env.VITE_PUBLIC_URL || 'http://localhost:8080/')
const managementMutationHeaders = {
  'Content-Type': 'application/json',
  'X-Balancer-CSRF': '1',
}
const demoBackendLimit = 8
const initialBackendCount = 2

interface BackendNode {
  id: string
  number: string
  url: string
  alive: boolean
  healthy: boolean
  enabled: boolean
  handled: number
  inflight: number
  draining: boolean
  slowStartPercent: number
  circuitState: string
}

interface DashboardStatus {
  mode: 'live'
  instance_id: string
  runtime_mutations_enabled: boolean
  strategy: string
  client_ip: string
  ready: boolean
  storage: string
  backends: Array<{
    id: string
    url: string
    healthy: boolean
    enabled: boolean
    available: boolean
    requests: number
    inflight: number
    draining: boolean
    slow_start_percent: number
    circuit_state: string
  }>
  bucket: {
    capacity: number
    tokens: number
    refill_per_second: number
    storage: string
    degraded: boolean
  }
  rate_limit: {
    enabled: boolean
    capacity: number
    refill_per_second: number
    failure_mode: string
    operation_timeout: string
    ipv4_prefix_bits: number
    ipv6_prefix_bits: number
    local_buckets: number
    local_evictions: number
  }
  health_check: {
    mode: string
    path: string
    interval: string
    timeout: string
    failure_threshold: number
    success_threshold: number
    max_concurrency: number
    jitter: string
    cooldown: string
    expected_statuses: number[]
    slow_start: string
    slow_start_minimum_percent: number
  }
  retry: {
    max_attempts: number
    per_try_timeout: string
    methods: string[]
    statuses: number[]
    budget: {
      capacity: number
      tokens: number
      refill_per_second: number
    }
  }
  protection: {
    overload: {
      max_concurrent_requests: number
      inflight: number
      queue_timeout: string
    }
    backend_max_concurrent_requests: number
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
  refillPerSecond: number
  total: number
  success: number
  rejected: number
  unavailable: number
  latencies: number[]
  events: EventRecord[]
  autoTraffic: boolean
  roundRobinCounter: number
}

function createDemoBackend(index: number): BackendNode {
  const position = index + 1
  return {
    id: `backend-${position}`,
    number: String(position).padStart(2, '0'),
    url: `http://backend${position}:80`,
    alive: true,
    healthy: true,
    enabled: true,
    handled: 0,
    inflight: 0,
    draining: false,
    slowStartPercent: 100,
    circuitState: 'closed',
  }
}

let selectedBackendCount = initialBackendCount
let backends: BackendNode[] = Array.from({ length: initialBackendCount }, (_, index) => createDemoBackend(index))

const state: SimulationState = {
  requestId: 0,
  capacity: isDemoMode ? 8 : 100,
  tokens: isDemoMode ? 8 : 100,
  refillPerSecond: 1,
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
        <a href="#simulator">Трафик</a>
        <a href="#architecture">Архитектура</a>
        <a href="#runtime">Runtime</a>
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
            HTTP reverse proxy с round-robin маршрутизацией, повторными попытками на другую ноду,
            активными и пассивными health-check и распределённым token bucket по IP клиента.
          </p>
          <div class="hero-actions">
            <a class="primary-link" href="#simulator">
              Проверить обработку запроса
              <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M4 10h11M11 6l4 4-4 4" /></svg>
            </a>
            <span class="hero-note" id="hero-note">Live: панель читает состояние Go-процесса</span>
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
            <div><span>Health probe</span><strong>HTTP + thresholds</strong></div>
            <div><span>Rate storage</span><strong>Redis / PostgreSQL / local</strong></div>
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
                <g id="backend-route-layer"></g>
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

              <div class="backend-nodes" id="backend-nodes" aria-label="Backend pool"></div>

              <div class="database-node">
                <svg viewBox="0 0 24 24" aria-hidden="true"><ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v7c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 12v7c0 1.7 3.6 3 8 3s8-1.3 8-3v-7" /></svg>
                <span><strong id="storage-label">Redis</strong><small id="storage-detail">shared bucket state</small></span>
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

            <div class="profile-control backend-count-control">
              <label for="backend-count-select">Активных backend</label>
              <select id="backend-count-select" aria-label="Количество активных backend-серверов">
                ${Array.from({ length: demoBackendLimit }, (_, index) => `<option value="${index + 1}"${index + 1 === initialBackendCount ? ' selected' : ''}>${index + 1}</option>`).join('')}
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
          <p>Публичный data plane не содержит административных обработчиков. Management API и метрики слушают отдельный адрес и требуют bearer token; изменяющие JSON-запросы дополнительно защищены заголовком <code>X-Balancer-CSRF</code>. Тестовый запрос панели собирается заново по whitelist, поэтому credential, cookies и служебные management-заголовки не попадают в backend.</p>
        </div>

        <div class="flow-steps">
          <article class="flow-step">
            <span class="step-number">01</span>
            <div class="step-rule"></div>
            <div>
              <p class="kicker">Gate</p>
              <h3>Token bucket</h3>
              <p>Атомарная операция в Redis, PostgreSQL или шардированном local store. Local/fallback ограничен по числу bucket-ов, а IPv6 по умолчанию группируется по /64, поэтому ротация адресов не создаёт неограниченное состояние.</p>
            </div>
            <span class="step-result">allow()</span>
          </article>
          <article class="flow-step">
            <span class="step-number">02</span>
            <div class="step-rule"></div>
            <div>
              <p class="kicker">Route</p>
              <h3>Round-robin</h3>
              <p>Atomic-счётчик выбирает следующую доступную ноду из snapshot пула. Неуспешные active/passive health-check исключают backend без блокировки request path. Новый пул при полном reload сначала прогревается и только затем атомарно заменяет текущий.</p>
            </div>
            <span class="step-result">next % alive</span>
          </article>
          <article class="flow-step">
            <span class="step-number">03</span>
            <div class="step-rule"></div>
            <div>
              <p class="kicker">Forward</p>
              <h3>Reverse proxy</h3>
              <p><code>httputil.ReverseProxy</code> использует общий transport. Forwarding-заголовки пересобираются из проверенного IP, а encoded path сохраняется без декодирования <code>%2F</code>. Per-try timeout ограничивает ожидание заголовков, не весь streaming response.</p>
            </div>
            <span class="step-result">ServeHTTP()</span>
          </article>
          <article class="flow-step">
            <span class="step-number">04</span>
            <div class="step-rule"></div>
            <div>
              <p class="kicker">Protect</p>
              <h3>Bounded amplification</h3>
              <p>Глобальный concurrency gate, лимит на backend и retry budget ограничивают объём работы. Зависший body retryable-ответа закрывается до новой попытки; bounded local fallback вытесняет bucket за O(1), без сканирования shard-а.</p>
            </div>
            <span class="step-result">budget / inflight</span>
          </article>
        </div>
      </section>

      <section class="runtime-section" id="runtime" aria-labelledby="runtime-title">
        <div class="runtime-intro">
          <p class="eyebrow"><span>03</span> Runtime configuration</p>
          <h2 id="runtime-title">Настройки работающего процесса</h2>
          <p id="runtime-copy">В live-режиме форма вызывает защищённый management API. В demo-режиме те же поля изменяют только модель в браузере — это режим интерактивной документации для GitHub Pages.</p>
          <dl class="runtime-facts">
            <div><dt>Состояние</dt><dd id="ready-value">DEMO</dd></div>
            <div><dt>Хранилище</dt><dd id="runtime-storage">browser memory</dd></div>
            <div><dt>Режим отказа</dt><dd id="runtime-failure-mode">local-fallback</dd></div>
            <div><dt>Retry</dt><dd id="runtime-retry">2 attempts</dd></div>
            <div><dt>Retry budget</dt><dd id="runtime-retry-budget">100 / 100</dd></div>
            <div><dt>Concurrency</dt><dd id="runtime-concurrency">0 / 2048</dd></div>
            <div><dt>Client key</dt><dd id="runtime-client-prefix">IPv4 /32 · IPv6 /64</dd></div>
            <div><dt>Local fallback</dt><dd id="runtime-local-store">0 buckets · 0 evictions</dd></div>
          </dl>
        </div>

        <form class="runtime-form" id="runtime-form">
          <fieldset>
            <legend>Rate limit</legend>
            <label>Capacity<input id="setting-capacity" name="capacity" type="number" min="1" max="1000000" value="100" /></label>
            <label>Refill / sec<input id="setting-refill" name="refill" type="number" min="0.01" max="1000000" step="any" value="1" /></label>
            <label>Failure mode<select id="setting-failure" name="failure"><option value="local-fallback">local-fallback</option><option value="fail-open">fail-open</option><option value="fail-closed">fail-closed</option></select></label>
          </fieldset>
          <fieldset>
            <legend>Health-check</legend>
            <label>Interval<input id="setting-health-interval" name="healthInterval" type="text" value="5s" pattern="[0-9]+(ms|s|m)" /></label>
            <label>Timeout<input id="setting-health-timeout" name="healthTimeout" type="text" value="2s" pattern="[0-9]+(ms|s|m)" /></label>
            <label>Failure threshold<input id="setting-failure-threshold" name="failureThreshold" type="number" min="1" max="100" value="3" /></label>
            <label>Success threshold<input id="setting-success-threshold" name="successThreshold" type="number" min="1" max="100" value="2" /></label>
            <label>Slow start<input id="setting-slow-start" name="slowStart" type="text" value="30s" pattern="[0-9]+(ms|s|m)" /></label>
            <label>Minimum traffic %<input id="setting-slow-minimum" name="slowMinimum" type="number" min="1" max="100" value="10" /></label>
          </fieldset>
          <fieldset>
            <legend>Retry</legend>
            <label>Max attempts<input id="setting-retry-attempts" name="retryAttempts" type="number" min="1" max="5" value="2" /></label>
            <label>Per-try timeout<input id="setting-retry-timeout" name="retryTimeout" type="text" value="2s" pattern="[0-9]+(ms|s|m)" /></label>
            <label>Budget capacity<input id="setting-retry-budget" name="retryBudget" type="number" min="1" max="100000" value="100" /></label>
            <label>Budget refill / sec<input id="setting-retry-budget-refill" name="retryBudgetRefill" type="number" min="0.01" max="100000" step="any" value="10" /></label>
          </fieldset>
          <fieldset class="backend-operations">
            <legend>Connection draining</legend>
            <div class="drain-actions" id="drain-actions"></div>
            <p>Drain прекращает новые назначения, но уже начатые запросы продолжают учитываться до закрытия response body.</p>
          </fieldset>
          <div class="runtime-form-actions">
            <button class="send-button" type="submit">Применить параметры</button>
            <p id="runtime-form-status" role="status">Изменяемые поля применяются без перезапуска. Остальные параметры меняются через YAML и SIGHUP.</p>
          </div>
        </form>
      </section>

      <section class="deployment-section" aria-labelledby="deployment-title">
        <div class="deployment-heading">
          <p class="eyebrow"><span>04</span> Deployment template</p>
          <h2 id="deployment-title">Что входит в production-контур</h2>
          <p>Репозиторий содержит не только бинарник: Kubernetes-база, локальный TLS edge, наблюдаемость, нагрузочные профили и выпуск подписанных OCI-образов описаны и проверяются отдельно.</p>
        </div>
        <div class="deployment-grid">
          <article><span>01 / Availability</span><h3>3–12 replicas</h3><p>Rolling update, probes, PDB и HPA. Console работает read-only; для PostgreSQL есть отдельный migration Job, а advisory lock защищает от параллельного запуска схемы.</p><code>deploy/kubernetes/base</code></article>
          <article><span>02 / Edge</span><h3>TLS + access boundary</h3><p>Публичный data plane проходит через Ingress или Caddy. Management и console остаются внутри сети; при внешней публикации console перед ней обязателен OIDC/SSO или mTLS.</p><code>deploy/kubernetes/base</code></article>
          <article><span>03 / Observe</span><h3>Metrics + alerts</h3><p>Prometheus учитывает весь трафик без общего mutex на hot path. Access-логи семплируются, URL path по умолчанию скрыт; видны health, p95, overload, retry budget и storage.</p><code>deploy/compose.observability.yml</code></article>
          <article><span>04 / Supply chain</span><h3>Immutable images</h3><p>Сторонние образы закреплены по digest, security scan повторяется еженедельно. Release добавляет SBOM/provenance и подписывает digest через Sigstore.</p><code>.github/workflows/workflow.yml</code></article>
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
          <pre aria-label="YAML-конфигурация"><code><span class="code-key">management:</span>
  address: <span class="code-value">":9090"</span>
  auth_token_env: <span class="code-value">"BALANCER_ADMIN_TOKEN"</span>
  write_timeout: <span class="code-value">"30s"</span>

<span class="code-key">backends:</span>
  - id: <span class="code-value">"backend-1"</span>
    url: <span class="code-value">"http://backend1:80"</span>

<span class="code-key">rate_limit:</span>
  storage: <span class="code-value">"redis"</span>
  failure_mode: <span class="code-value">"local-fallback"</span>
  capacity: <span class="code-number">100</span>
  refill_per_second: <span class="code-number">1</span>
  local_max_buckets: <span class="code-number">100000</span>
  ipv6_prefix_bits: <span class="code-number">64</span>

<span class="code-key">server:</span>
  access_log_include_path: <span class="code-value">false</span>
  write_timeout: <span class="code-value">"0s"</span>
  overload:
    max_concurrent_requests: <span class="code-number">2048</span>
  retry:
    budget_capacity: <span class="code-number">100</span>

<span class="code-key">health_check:</span>
  slow_start: <span class="code-value">"30s"</span></code></pre>
          <p class="config-note" id="config-note"><span>!</span> Секреты читаются из переменных окружения. YAML содержит только имена переменных, адреса и несекретные параметры.</p>
        </div>

        <div class="health-panel">
          <p class="eyebrow"><span>05</span> Health-check</p>
          <h2><span id="health-mode-copy">HTTP</span>-проверка<br /><span id="health-interval-copy">каждые 5 секунд</span></h2>
          <p>Проверка учитывает разрешённые HTTP-статусы, пороги последовательных ошибок и успехов, jitter, cooldown и ограничение параллелизма. Восстановившийся backend проходит slow start; passive failures открывают circuit.</p>
          <div class="health-visual" aria-hidden="true">
            <span class="health-ring ring-one"></span>
            <span class="health-ring ring-two"></span>
            <span class="health-ring ring-three"></span>
            <span class="health-core"><b>5</b><small>SEC</small></span>
          </div>
          <div class="health-foot"><span id="health-path-copy">GET /</span><strong id="health-timeout-copy">2s timeout</strong></div>
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
  routes: query<SVGSVGElement>('.routes'),
  routeIngress: query<SVGPathElement>('.route-ingress'),
  routeBlocked: query<SVGPathElement>('#route-blocked'),
  databaseRoute: query<SVGPathElement>('.database-route'),
  routeJunction: query<SVGGElement>('.route-junction'),
  backendRouteLayer: query<SVGGElement>('#backend-route-layer'),
  networkStage: query<HTMLElement>('.network-stage'),
  clientNode: query<HTMLElement>('.client-node'),
  backendNodes: query<HTMLElement>('#backend-nodes'),
  databaseNode: query<HTMLElement>('.database-node'),
  drainActions: query<HTMLElement>('#drain-actions'),
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
  backendCountSelect: query<HTMLSelectElement>('#backend-count-select'),
  resetButton: query<HTMLButtonElement>('#reset-simulation'),
  storageLabel: query<HTMLElement>('#storage-label'),
  storageDetail: query<HTMLElement>('#storage-detail'),
  readyValue: query<HTMLElement>('#ready-value'),
  runtimeStorage: query<HTMLElement>('#runtime-storage'),
  runtimeFailureMode: query<HTMLElement>('#runtime-failure-mode'),
  runtimeRetry: query<HTMLElement>('#runtime-retry'),
  runtimeRetryBudget: query<HTMLElement>('#runtime-retry-budget'),
  runtimeConcurrency: query<HTMLElement>('#runtime-concurrency'),
  runtimeClientPrefix: query<HTMLElement>('#runtime-client-prefix'),
  runtimeLocalStore: query<HTMLElement>('#runtime-local-store'),
  runtimeForm: query<HTMLFormElement>('#runtime-form'),
  runtimeCopy: query<HTMLElement>('#runtime-copy'),
  runtimeFormStatus: query<HTMLElement>('#runtime-form-status'),
  settingCapacity: query<HTMLInputElement>('#setting-capacity'),
  settingRefill: query<HTMLInputElement>('#setting-refill'),
  settingFailure: query<HTMLSelectElement>('#setting-failure'),
  settingHealthInterval: query<HTMLInputElement>('#setting-health-interval'),
  settingHealthTimeout: query<HTMLInputElement>('#setting-health-timeout'),
  settingFailureThreshold: query<HTMLInputElement>('#setting-failure-threshold'),
  settingSuccessThreshold: query<HTMLInputElement>('#setting-success-threshold'),
  settingSlowStart: query<HTMLInputElement>('#setting-slow-start'),
  settingSlowMinimum: query<HTMLInputElement>('#setting-slow-minimum'),
  settingRetryAttempts: query<HTMLInputElement>('#setting-retry-attempts'),
  settingRetryTimeout: query<HTMLInputElement>('#setting-retry-timeout'),
  settingRetryBudget: query<HTMLInputElement>('#setting-retry-budget'),
  settingRetryBudgetRefill: query<HTMLInputElement>('#setting-retry-budget-refill'),
  healthModeCopy: query<HTMLElement>('#health-mode-copy'),
  healthIntervalCopy: query<HTMLElement>('#health-interval-copy'),
  healthPathCopy: query<HTMLElement>('#health-path-copy'),
  healthTimeoutCopy: query<HTMLElement>('#health-timeout-copy'),
}

let autoTrafficTimer: number | undefined
let toastTimer: number | undefined
let statusRefreshTimer: number | undefined
let healthCountdown = 5
let healthIntervalSeconds = 5
let backendConnected = false
let runtimeFormDirty = false
let runtimeMutationsEnabled = isDemoMode
let connectedInstanceID = isDemoMode ? 'browser' : 'unknown'

function configureBackendCountOptions(maximum: number): void {
  Array.from(elements.backendCountSelect.options).forEach((option) => {
    option.disabled = Number(option.value) > maximum
  })
  elements.backendCountSelect.title = `Настроено backend-серверов: ${maximum}`
}

function syncBackendCountControl(): void {
  const activeCount = backends.filter((backend) => backend.enabled).length
  selectedBackendCount = Math.max(1, activeCount)
  elements.backendCountSelect.value = String(selectedBackendCount)
}

function formatTime(): string {
  return new Intl.DateTimeFormat('ru-RU', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date())
}

function durationSeconds(value: string): number {
  const match = value.trim().match(/^(\d+(?:\.\d+)?)(ms|s|m)$/)
  if (!match) return 0
  const amount = Number(match[1])
  if (match[2] === 'ms') return amount / 1000
  if (match[2] === 'm') return amount * 60
  return amount
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
    runtimeMutationsEnabled = true
    elements.runtimeState.classList.remove('is-offline', 'is-connecting')
    elements.runtimeState.title = 'Demo mode без подключения к API'
    elements.runtimeLabel.textContent = 'Demo mode'
    elements.runtimeVersion.textContent = 'STATIC'
    elements.heroLead.textContent =
      'Браузер воспроизводит token bucket, round-robin и пул из 1–8 backend-серверов. Сетевые запросы к Go API в этом режиме не выполняются.'
    elements.heroNote.textContent = 'Demo: автономная интерактивная документация для GitHub Pages'
    elements.endpointAddress.textContent = 'demo://round-robin'
    elements.configNote.innerHTML =
      '<span>!</span> В режиме <code>demo</code> интерфейс использует локальную модель этих параметров. Режим <code>live</code> читает и изменяет состояние через Go API.'
    elements.clientIP.textContent = '192.168.1.24'
    elements.capacitySelect.value = String(state.capacity)
    elements.settingCapacity.value = String(state.capacity)
    elements.settingRefill.value = String(state.refillPerSecond)
    elements.sendButton.disabled = false
    elements.burstButton.disabled = false
    elements.autoButton.disabled = false
    elements.capacitySelect.disabled = false
    elements.backendCountSelect.disabled = false
    elements.resetButton.disabled = false
    elements.readyValue.textContent = 'DEMO'
    elements.runtimeStorage.textContent = 'browser memory'
    elements.runtimeFailureMode.textContent = elements.settingFailure.value
    elements.runtimeRetry.textContent = `${elements.settingRetryAttempts.value} attempts`
    elements.runtimeRetryBudget.textContent = `${elements.settingRetryBudget.value} / ${elements.settingRetryBudget.value}`
    elements.runtimeConcurrency.textContent = '0 / 2048'
    elements.storageLabel.textContent = 'Browser state'
    elements.storageDetail.textContent = 'demo model'
    renderBackends()
    return
  }

  backendConnected = connected
  elements.runtimeState.classList.toggle('is-offline', !connected)
  elements.runtimeState.classList.toggle('is-connecting', false)
  elements.runtimeState.title = connected ? 'Соединено с Go API' : 'Нет соединения с Go API'
  elements.runtimeLabel.textContent = connected ? 'Live mode' : 'API unavailable'
  elements.runtimeVersion.textContent = connected ? `API / ${connectedInstanceID}` : 'RETRYING'
  elements.heroLead.textContent =
    'Панель отправляет запросы через Go-балансировщик и показывает выбранную ноду, остаток token bucket, HTTP-код, retry и итоговое health-состояние.'
  elements.heroNote.textContent = connected
    ? `Live: replica ${connectedInstanceID} · ${runtimeMutationsEnabled ? 'runtime control' : 'read-only'} `
    : 'Management API недоступен · запустите docker compose up --build'
  elements.endpointAddress.textContent = publicBalancerURL.replace(/^https?:\/\//, '')
  elements.configNote.innerHTML = runtimeMutationsEnabled
    ? '<span>!</span> В локальном режиме <code>live</code> профиль bucket и форма Runtime меняют состояние одного работающего Go-процесса через management API.'
    : '<span>!</span> В multi-replica режиме панель работает только на чтение. Измените ConfigMap и выполните rolling rollout, чтобы все реплики получили одну версию конфигурации.'
  elements.runtimeCopy.textContent = runtimeMutationsEnabled
    ? 'Форма вызывает защищённый management API и изменяет локальный процесс. Этот режим предназначен для локальной лаборатории.'
    : 'Эта реплика работает в декларативном режиме: панель показывает состояние, но изменения применяются через ConfigMap и rolling rollout сразу ко всем репликам.'
  elements.sendButton.disabled = !connected
  elements.burstButton.disabled = !connected
  elements.autoButton.disabled = !connected
  elements.capacitySelect.disabled = !connected || !runtimeMutationsEnabled
  elements.backendCountSelect.disabled = !connected || !runtimeMutationsEnabled
  elements.resetButton.disabled = !connected || !runtimeMutationsEnabled
  elements.runtimeForm.querySelectorAll('input, select, button').forEach((control) => {
    ;(control as HTMLInputElement | HTMLSelectElement | HTMLButtonElement).disabled = !connected || !runtimeMutationsEnabled
  })
  if (connected && !runtimeMutationsEnabled) {
    elements.runtimeFormStatus.textContent = `Read-only replica ${connectedInstanceID}: измените ConfigMap и выполните rolling rollout.`
  }
  if (!connected) elements.readyValue.textContent = 'UNKNOWN'
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
    runtimeMutationsEnabled = dashboard.runtime_mutations_enabled !== false
    connectedInstanceID = dashboard.instance_id || 'unknown'
    state.capacity = dashboard.bucket.capacity
    state.tokens = dashboard.bucket.tokens
    state.refillPerSecond = dashboard.bucket.refill_per_second
    if (Array.from(elements.capacitySelect.options).some((option) => Number(option.value) === state.capacity)) {
      elements.capacitySelect.value = String(state.capacity)
    }
    elements.clientIP.textContent = dashboard.client_ip
    elements.readyValue.textContent = dashboard.ready ? 'READY' : 'NOT READY'
    elements.readyValue.classList.toggle('is-error', !dashboard.ready)
    elements.runtimeStorage.textContent = dashboard.storage
    elements.runtimeFailureMode.textContent = dashboard.rate_limit.failure_mode
    elements.runtimeRetry.textContent = `${dashboard.retry.max_attempts} attempts · ${dashboard.retry.per_try_timeout}`
    elements.runtimeRetryBudget.textContent = `${dashboard.retry.budget.tokens.toFixed(1)} / ${dashboard.retry.budget.capacity}`
    elements.runtimeConcurrency.textContent = `${dashboard.protection.overload.inflight} / ${dashboard.protection.overload.max_concurrent_requests}`
    elements.runtimeClientPrefix.textContent = `IPv4 /${dashboard.rate_limit.ipv4_prefix_bits} · IPv6 /${dashboard.rate_limit.ipv6_prefix_bits}`
    elements.runtimeLocalStore.textContent = `${dashboard.rate_limit.local_buckets} buckets · ${dashboard.rate_limit.local_evictions} evictions`
    elements.storageLabel.textContent = dashboard.bucket.storage || dashboard.storage
    elements.storageDetail.textContent = dashboard.bucket.degraded ? 'degraded / fallback' : 'shared bucket state'
    if (!runtimeFormDirty) {
      elements.settingCapacity.value = String(dashboard.rate_limit.capacity)
      elements.settingRefill.value = String(dashboard.rate_limit.refill_per_second)
      elements.settingFailure.value = dashboard.rate_limit.failure_mode
      elements.settingHealthInterval.value = dashboard.health_check.interval
      elements.settingHealthTimeout.value = dashboard.health_check.timeout
      elements.settingFailureThreshold.value = String(dashboard.health_check.failure_threshold)
      elements.settingSuccessThreshold.value = String(dashboard.health_check.success_threshold)
      elements.settingSlowStart.value = dashboard.health_check.slow_start
      elements.settingSlowMinimum.value = String(dashboard.health_check.slow_start_minimum_percent)
      elements.settingRetryAttempts.value = String(dashboard.retry.max_attempts)
      elements.settingRetryTimeout.value = dashboard.retry.per_try_timeout
      elements.settingRetryBudget.value = String(dashboard.retry.budget.capacity)
      elements.settingRetryBudgetRefill.value = String(dashboard.retry.budget.refill_per_second)
    }
    elements.healthModeCopy.textContent = dashboard.health_check.mode.toUpperCase()
    elements.healthIntervalCopy.textContent = `каждые ${dashboard.health_check.interval}`
    elements.healthPathCopy.textContent = `${dashboard.health_check.mode === 'tcp' ? 'CONNECT' : 'GET'} ${dashboard.health_check.path || 'host:port'}`
    elements.healthTimeoutCopy.textContent = `${dashboard.health_check.timeout} timeout`
    const parsedInterval = durationSeconds(dashboard.health_check.interval)
    if (parsedInterval > 0) {
      healthIntervalSeconds = Math.max(1, Math.ceil(parsedInterval))
      healthCountdown = healthIntervalSeconds
    }

    backends = dashboard.backends.map((snapshot, index) => ({
      id: snapshot.id,
      number: String(index + 1).padStart(2, '0'),
      url: snapshot.url,
      healthy: snapshot.healthy,
      enabled: snapshot.enabled,
      alive: snapshot.available,
      handled: snapshot.requests,
      inflight: snapshot.inflight,
      draining: snapshot.draining,
      slowStartPercent: snapshot.slow_start_percent,
      circuitState: snapshot.circuit_state,
    }))
    configureBackendCountOptions(backends.length)
    syncBackendCountControl()

    const wasConnected = backendConnected
    setConnectionState(true)
    renderState()
    if (announce && !wasConnected) {
      addEvent('SYS', 'Go backend подключён', `${dashboard.strategy} · ${dashboard.storage} · health ${dashboard.health_check.interval}`)
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
      const backendID = response.headers.get('X-Balancer-Backend') ?? ''
      const attempts = response.headers.get('X-Balancer-Attempts') ?? '1'
      const backendIndex = backends.findIndex((backend) => backend.id === backendID)
      const backend = backends[backendIndex]

      state.success += 1
      state.latencies.push(latency)
      state.latencies = state.latencies.slice(-20)
      animatePacket(backendIndex >= 0 ? backendIndex : null)
      addEvent(
        response.status,
        `${requestLabel} → ${backend ? `backend ${backend.number}` : backendID || 'backend'}`,
        `${backendID || 'proxied'} · ${latency} ms · attempts ${attempts}`,
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
  elements.tokenCount.textContent = state.tokens.toFixed(state.tokens % 1 === 0 ? 0 : 1)
  elements.tokenCapacity.textContent = String(state.capacity)
  elements.bucketFill.style.width = `${(state.tokens / state.capacity) * 100}%`
  elements.bucketRail.setAttribute('aria-valuemax', String(state.capacity))
  elements.bucketRail.setAttribute('aria-valuenow', state.tokens.toFixed(2))
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
	ensureBackendStructure()
  backends.forEach((backend, index) => {
	const element = elements.backendNodes.querySelector<HTMLButtonElement>(`[data-backend="${index}"]`)
    if (!element) return
    const status = element.querySelector<HTMLElement>('.backend-status')
    const latency = element.querySelector<HTMLElement>('.backend-latency')
    const address = element.querySelector<HTMLElement>('.backend-copy > strong')
    if (!status || !latency || !address) return

    element.classList.toggle('is-offline', !backend.alive)
    element.classList.toggle('is-disabled', !backend.enabled)
    element.classList.toggle('is-draining', backend.draining)
    element.disabled = !backendConnected || (!isDemoMode && !runtimeMutationsEnabled)
    element.setAttribute('aria-pressed', String(!backend.enabled))
    element.setAttribute('aria-label', `${backend.enabled ? 'Исключить' : 'Вернуть'} backend ${index + 1}`)
    status.textContent = backend.draining ? 'Draining' : !backend.enabled ? 'Disabled' : backend.healthy ? 'Healthy' : 'Unhealthy'
    address.textContent = backend.url.replace(/^https?:\/\//, '')
    latency.textContent = `${backend.handled} req · ${backend.inflight} active · ${backend.slowStartPercent}%`
    element.title = `Circuit: ${backend.circuitState}; inflight: ${backend.inflight}; slow start: ${backend.slowStartPercent}%`
	const drainButton = elements.drainActions.querySelector<HTMLButtonElement>(`[data-drain-backend="${index}"]`)
	if (drainButton) {
		drainButton.disabled = !backendConnected || (!isDemoMode && !runtimeMutationsEnabled)
	}
  })
  syncBackendCountControl()
  queueRouteGeometryUpdate()
}

function ensureBackendStructure(): void {
	elements.networkStage.style.setProperty('--backend-count', String(backends.length))
	const expectedIDs = backends.map((backend) => backend.id).join('\u0000')
	if (elements.backendNodes.dataset.backendIds === expectedIDs) return
	elements.backendNodes.dataset.backendIds = expectedIDs
	elements.backendNodes.replaceChildren()
	elements.drainActions.replaceChildren()
	elements.backendRouteLayer.replaceChildren()

	const svgNamespace = 'http://www.w3.org/2000/svg'
	backends.forEach((backend, index) => {
		const button = document.createElement('button')
		button.className = 'network-node backend-node'
		button.type = 'button'
		button.dataset.backend = String(index)
		button.dataset.backendId = backend.id
		button.innerHTML = `
			<span class="backend-number"></span>
			<span class="backend-copy">
				<span class="node-label">Backend</span>
				<strong></strong>
				<small><i class="status-dot"></i><span class="backend-status">Connecting</span> · <span class="backend-latency">0 req</span></small>
			</span>
			<span class="power-icon" title="Переключить доступность">
				<svg viewBox="0 0 20 20" aria-hidden="true"><path d="M10 2v7M5.1 5.1a7 7 0 1 0 9.8 0" /></svg>
			</span>`
		const number = button.querySelector<HTMLElement>('.backend-number')
		if (number) number.textContent = backend.number
		elements.backendNodes.append(button)

		const drain = document.createElement('button')
		drain.className = 'secondary-button'
		drain.type = 'button'
		drain.dataset.drainBackend = String(index)
		drain.textContent = `Drain ${backend.id}`
		elements.drainActions.append(drain)

		const path = document.createElementNS(svgNamespace, 'path')
		path.id = `route-backend-${index + 1}`
		path.setAttribute('class', 'route-line')
		elements.backendRouteLayer.append(path)
	})
	queueRouteGeometryUpdate()
}

let routeGeometryFrame = 0

function queueRouteGeometryUpdate(): void {
	window.cancelAnimationFrame(routeGeometryFrame)
	routeGeometryFrame = window.requestAnimationFrame(updateRouteGeometry)
}

function updateRouteGeometry(): void {
	const routesRect = elements.routes.getBoundingClientRect()
	if (routesRect.width < 1 || routesRect.height < 1) return

	// Match SVG user units to CSS pixels. Besides making the route coordinates
	// predictable, this keeps the packet and junction circles perfectly round.
	elements.routes.setAttribute('viewBox', `0 0 ${routesRect.width} ${routesRect.height}`)

	const clientRect = elements.clientNode.getBoundingClientRect()
	const balancerRect = elements.balancer.getBoundingClientRect()
	const databaseRect = elements.databaseNode.getBoundingClientRect()
	const localX = (screenX: number): number => screenX - routesRect.left
	const localY = (screenY: number): number => screenY - routesRect.top
	const middleY = (rect: DOMRect): number => localY(rect.top + rect.height / 2)
	const point = (x: number, y: number): string => `${x.toFixed(2)} ${y.toFixed(2)}`

	const clientExit = { x: localX(clientRect.right), y: middleY(clientRect) }
	const balancerEntry = { x: localX(balancerRect.left), y: middleY(balancerRect) }
	const balancerExit = { x: localX(balancerRect.right) + 10, y: middleY(balancerRect) }
	const ingressSpan = Math.max(1, balancerEntry.x - clientExit.x)
	const ingressPath = [
		`M ${point(clientExit.x, clientExit.y)}`,
		`C ${point(clientExit.x + ingressSpan * 0.46, clientExit.y)}`,
		point(balancerEntry.x - ingressSpan * 0.46, balancerEntry.y),
		point(balancerEntry.x, balancerEntry.y),
	].join(' ')

	elements.routeIngress.setAttribute('d', ingressPath)
	elements.routeBlocked.setAttribute(
		'd',
		`${ingressPath} L ${point(balancerEntry.x + balancerRect.width * 0.42, balancerEntry.y)}`,
	)

	backends.forEach((_backend, index) => {
		const path = elements.backendRouteLayer.querySelector<SVGPathElement>(`#route-backend-${index + 1}`)
		const backendNode = elements.backendNodes.querySelector<HTMLElement>(`[data-backend="${index}"]`)
		if (!path || !backendNode) return

		const backendRect = backendNode.getBoundingClientRect()
		const backendEntry = { x: localX(backendRect.left) + 1, y: middleY(backendRect) }
		const branchSpan = Math.max(1, backendEntry.x - balancerExit.x)
		path.setAttribute(
			'd',
			`${ingressPath} L ${point(balancerExit.x, balancerExit.y)} `
			+ `C ${point(balancerExit.x + branchSpan * 0.38, balancerExit.y)} `
			+ `${point(balancerExit.x + branchSpan * 0.56, backendEntry.y)} ${point(backendEntry.x, backendEntry.y)}`,
		)
	})

	elements.routeJunction.querySelectorAll<SVGCircleElement>('circle').forEach((circle) => {
		circle.setAttribute('cx', balancerExit.x.toFixed(2))
		circle.setAttribute('cy', balancerExit.y.toFixed(2))
	})

	const databaseStart = {
		x: localX(balancerRect.left + balancerRect.width / 2),
		y: localY(balancerRect.bottom),
	}
	const databaseEnd = {
		x: localX(databaseRect.left + databaseRect.width / 2),
		y: localY(databaseRect.top),
	}
	const databaseSpan = Math.max(1, databaseEnd.y - databaseStart.y)
	elements.databaseRoute.setAttribute(
		'd',
		`M ${point(databaseStart.x, databaseStart.y)} `
		+ `C ${point(databaseStart.x, databaseStart.y + databaseSpan * 0.45)} `
		+ `${point(databaseEnd.x, databaseEnd.y - databaseSpan * 0.45)} ${point(databaseEnd.x, databaseEnd.y)}`,
	)
}

const routeResizeObserver = new ResizeObserver(queueRouteGeometryUpdate)
routeResizeObserver.observe(elements.networkStage)
routeResizeObserver.observe(elements.backendNodes)
window.addEventListener('resize', queueRouteGeometryUpdate)
void document.fonts.ready.then(queueRouteGeometryUpdate)

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

function resizeDemoBackendPool(count: number): void {
  const previous = backends
  backends = Array.from({ length: count }, (_, index) => previous[index] ?? createDemoBackend(index))
  backends.forEach((backend) => {
    backend.enabled = true
    backend.alive = backend.healthy
    backend.draining = false
    backend.circuitState = backend.alive ? 'closed' : 'open'
  })
  selectedBackendCount = count
  state.roundRobinCounter = 0
}

async function updateBackendCount(count: number): Promise<void> {
  if (count < 1 || count > (isDemoMode ? demoBackendLimit : backends.length)) return

  if (isDemoMode) {
    resizeDemoBackendPool(count)
    renderState()
    addEvent('SYS', 'Размер demo-pool изменён', `${count} активных backend-серверов`)
    showToast(`Активных backend: ${count}`)
    return
  }

  if (!runtimeMutationsEnabled) return
  elements.backendCountSelect.disabled = true
  try {
    const response = await fetch('/api/dashboard/backends', {
      method: 'POST',
      headers: managementMutationHeaders,
      body: JSON.stringify({ count }),
    })
    if (!response.ok) {
      const body = (await response.json().catch(() => null)) as { error?: string } | null
      throw new Error(body?.error || `HTTP ${response.status}`)
    }
    selectedBackendCount = count
    await refreshStatus()
    addEvent('SYS', 'Активный pool изменён', `${count} из ${backends.length} backend-серверов`)
    showToast(`Активных backend: ${count}`)
  } catch {
    showToast('Не удалось изменить количество backend-серверов', 'error')
    await refreshStatus()
  } finally {
    elements.backendCountSelect.disabled = !backendConnected || !runtimeMutationsEnabled
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
      backend.inflight = 0
      backend.draining = false
      backend.slowStartPercent = 100
      backend.circuitState = 'closed'
    })
    renderState()
    showToast('Демо-состояние сброшено')
    return
  }

  try {
    const requests = [
      fetch('/api/dashboard/limit', {
        method: 'POST',
        headers: managementMutationHeaders,
        body: JSON.stringify({ capacity: state.capacity }),
      }),
      fetch('/api/dashboard/backends', {
        method: 'POST',
        headers: managementMutationHeaders,
        body: JSON.stringify({ count: selectedBackendCount }),
      }),
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

elements.backendCountSelect.addEventListener('change', (event) => {
  const select = event.currentTarget as HTMLSelectElement
  void updateBackendCount(Number(select.value))
})

query<HTMLSelectElement>('#capacity-select').addEventListener('change', async (event) => {
  const select = event.currentTarget as HTMLSelectElement
  const capacity = Number(select.value)

  if (isDemoMode) {
    state.capacity = capacity
    state.tokens = capacity
    elements.settingCapacity.value = String(capacity)
    renderState()
    addEvent('SYS', 'Демо bucket сброшен', `${capacity} tokens · refill 1 token/sec`)
    showToast(`Демо bucket: ${capacity} токенов`)
    return
  }

  try {
    const configResponse = await fetch('/api/dashboard/config', {
      method: 'PATCH',
      headers: managementMutationHeaders,
      body: JSON.stringify({ rate_limit: { capacity } }),
    })
    if (!configResponse.ok) throw new Error('Runtime config API failed')
    const response = await fetch('/api/dashboard/limit', {
      method: 'POST',
      headers: managementMutationHeaders,
      body: JSON.stringify({ capacity }),
    })
    if (!response.ok) throw new Error('Limit API failed')
    const bucket = (await response.json()) as DashboardStatus['bucket']
    state.capacity = bucket.capacity
    state.tokens = bucket.tokens
    elements.settingCapacity.value = String(bucket.capacity)
    runtimeFormDirty = false
    renderState()
    addEvent('SYS', 'Token bucket сброшен', `${bucket.capacity} tokens · refill ${bucket.refill_per_second}/sec`)
    showToast(`Реальный bucket: ${bucket.capacity} токенов`)
  } catch {
    showToast('Не удалось изменить bucket', 'error')
    await refreshStatus()
    select.value = String(state.capacity)
  }
})

elements.runtimeForm.addEventListener('input', () => {
  runtimeFormDirty = true
})

elements.runtimeForm.addEventListener('submit', async (event) => {
  event.preventDefault()
  if (!elements.runtimeForm.reportValidity()) return

  const capacity = Number(elements.settingCapacity.value)
  const refillPerSecond = Number(elements.settingRefill.value)
  const failureMode = elements.settingFailure.value
  const healthInterval = elements.settingHealthInterval.value.trim()
  const healthTimeout = elements.settingHealthTimeout.value.trim()
  const failureThreshold = Number(elements.settingFailureThreshold.value)
  const successThreshold = Number(elements.settingSuccessThreshold.value)
  const slowStart = elements.settingSlowStart.value.trim()
  const slowMinimum = Number(elements.settingSlowMinimum.value)
  const retryAttempts = Number(elements.settingRetryAttempts.value)
  const retryTimeout = elements.settingRetryTimeout.value.trim()
  const retryBudget = Number(elements.settingRetryBudget.value)
  const retryBudgetRefill = Number(elements.settingRetryBudgetRefill.value)

  if (isDemoMode) {
    state.capacity = capacity
    state.tokens = Math.min(state.tokens, capacity)
    state.refillPerSecond = refillPerSecond
    healthIntervalSeconds = Math.max(1, Math.ceil(durationSeconds(healthInterval)))
    healthCountdown = healthIntervalSeconds
    elements.runtimeFailureMode.textContent = failureMode
    elements.runtimeRetry.textContent = `${retryAttempts} attempts · ${retryTimeout}`
    elements.runtimeRetryBudget.textContent = `${retryBudget} / ${retryBudget}`
    elements.healthIntervalCopy.textContent = `каждые ${healthInterval}`
    elements.healthTimeoutCopy.textContent = `${healthTimeout} timeout`
    elements.runtimeFormStatus.textContent = 'Параметры применены к модели в браузере. Go-процесс и внешнее хранилище не используются.'
    runtimeFormDirty = false
    renderState()
    addEvent('SYS', 'Demo runtime обновлён', `${capacity} tokens · retry ${retryAttempts} · budget ${retryBudget}`)
    showToast('Demo-параметры применены')
    return
  }

  elements.runtimeFormStatus.textContent = 'Применение параметров…'
  try {
    const response = await fetch('/api/dashboard/config', {
      method: 'PATCH',
      headers: managementMutationHeaders,
      body: JSON.stringify({
        rate_limit: { capacity, refill_per_second: refillPerSecond, failure_mode: failureMode },
        health_check: { interval: healthInterval, timeout: healthTimeout, failure_threshold: failureThreshold, success_threshold: successThreshold, slow_start: slowStart, slow_start_minimum_percent: slowMinimum },
        retry: { max_attempts: retryAttempts, per_try_timeout: retryTimeout, budget_capacity: retryBudget, budget_refill_per_second: retryBudgetRefill },
      }),
    })
    if (!response.ok) {
      const body = (await response.json().catch(() => null)) as { error?: string } | null
      throw new Error(body?.error || `HTTP ${response.status}`)
    }
    runtimeFormDirty = false
    await refreshStatus()
    elements.runtimeFormStatus.textContent = 'Параметры применены к работающему процессу. Изменение действует до следующего старта или SIGHUP.'
    addEvent('SYS', 'Runtime config обновлён', `${capacity} tokens · ${refillPerSecond}/sec · retry ${retryAttempts}`)
    showToast('Runtime-параметры применены')
  } catch (error) {
    const message = error instanceof Error ? error.message : 'неизвестная ошибка'
    elements.runtimeFormStatus.textContent = `Настройки отклонены: ${message}`
    showToast('Не удалось применить настройки', 'error')
    runtimeFormDirty = false
    await refreshStatus()
  }
})

elements.drainActions.addEventListener('click', (event) => {
  const button = (event.target as HTMLElement).closest<HTMLButtonElement>('[data-drain-backend]')
  if (!button) return
  void drainBackend(Number(button.dataset.drainBackend), button)
})

elements.backendNodes.addEventListener('click', (event) => {
  const button = (event.target as HTMLElement).closest<HTMLButtonElement>('[data-backend]')
  if (!button) return
  void toggleBackend(Number(button.dataset.backend), button)
})

async function drainBackend(index: number, button: HTMLButtonElement): Promise<void> {
  const backend = backends[index]
  if (!backend) return
  if (isDemoMode) {
    backend.draining = true
    backend.enabled = false
    backend.alive = false
    backend.circuitState = 'draining'
    renderBackends()
    addEvent('SYS', `Backend ${backend.number}: draining`, `${backend.inflight} активных запросов будут завершены`)
    return
  }
  if (!runtimeMutationsEnabled) return
  button.disabled = true
  try {
    const response = await fetch(`/api/dashboard/backends/${encodeURIComponent(backend.id)}/drain`, {
      method: 'POST',
      headers: managementMutationHeaders,
      body: '{}',
    })
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    addEvent('SYS', `Backend ${backend.number}: draining`, 'новые запросы больше не назначаются')
    showToast(`Backend ${backend.number}: draining`)
    await refreshStatus()
  } catch {
    showToast('Не удалось начать draining', 'error')
  } finally {
    button.disabled = false
  }
}

async function toggleBackend(index: number, element: HTMLButtonElement): Promise<void> {
  const backend = backends[index]
  if (!backend) return
  const enabled = !backend.enabled
  if (isDemoMode) {
    backend.enabled = enabled
    backend.alive = backend.healthy && enabled
    backend.draining = false
    backend.circuitState = backend.alive ? 'closed' : 'open'
    renderBackends()
    addEvent('SYS', `Backend ${backend.number}: ${enabled ? 'enabled' : 'disabled'}`, enabled ? 'возвращён в демо-pool' : 'исключён из демо-pool')
    showToast(`Backend ${backend.number}: ${enabled ? 'enabled' : 'disabled'}`)
    return
  }
  if (!runtimeMutationsEnabled) return
  element.disabled = true
  try {
    const response = await fetch(`/api/dashboard/backends/${encodeURIComponent(backend.id)}`, {
      method: 'POST',
      headers: managementMutationHeaders,
      body: JSON.stringify({ enabled }),
    })
    if (!response.ok) throw new Error('Backend state API failed')
    backend.enabled = enabled
    backend.alive = backend.healthy && enabled
    backend.draining = false
    renderBackends()
    addEvent('SYS', `Backend ${backend.number}: ${enabled ? 'enabled' : 'disabled'}`, enabled ? 'возвращён в реальный pool' : 'исключён из реального pool')
    showToast(`Backend ${backend.number}: ${enabled ? 'enabled' : 'disabled'}`)
    queueStatusRefresh()
  } catch {
    showToast('Не удалось изменить backend', 'error')
    await refreshStatus()
  }
}

query<HTMLButtonElement>('#clear-log').addEventListener('click', () => {
  state.events = []
  renderEvents()
})

query<HTMLButtonElement>('.copy-button').addEventListener('click', async () => {
  const address = isDemoMode ? 'demo://round-robin' : publicBalancerURL
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
  if (healthCountdown <= 0) {
    healthCountdown = healthIntervalSeconds
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
  state.tokens = Math.min(state.capacity, state.tokens + state.refillPerSecond / 4)
  renderState()
}, 250)

renderEvents()
renderState()
if (isDemoMode) {
  void refreshStatus(true)
} else {
  setConnectionState(false)
  void refreshStatus(true)
  window.setInterval(() => void refreshStatus(), 2000)
}
