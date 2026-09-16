export function renderGuide(mode: 'demo' | 'live'): string {
  const currentMode =
    mode === 'demo'
      ? 'Сейчас открыт demo: все запросы и изменения выполняет симулятор в браузере.'
      : 'Сейчас открыт live: интерфейс подключён к запущенному Go-процессу.'

  return `
    <section class="guide-intro" aria-labelledby="guide-title">
      <div>
        <span class="eyebrow">Начните отсюда</span>
        <h2 id="guide-title">Что делает этот проект</h2>
        <p class="guide-lead">Это HTTP-прокси и балансировщик нагрузки. Клиент отправляет запрос на один адрес прокси, а прокси выбирает подходящий работающий сервер, передаёт ему запрос и возвращает клиенту ответ.</p>
        <p>Клиенту не нужно знать адреса всех серверов. Если один из них выключен, перегружен или проходит обслуживание, прокси может выбрать другой.</p>
        <p>Эта страница только показывает работу системы и сама ничего не ускоряет. Нагрузка обрабатывается быстрее, когда запускают несколько копий сервиса, а прокси распределяет запросы между ними.</p>
        <div class="guide-mode-now"><span class="mode-badge mode-badge--${mode}">${mode}</span><strong>${currentMode}</strong></div>
      </div>
      <aside class="guide-example" aria-label="Простой пример">
        <span>Пример</span>
        <p>Есть три одинаковых сервера интернет-магазина. Покупатель обращается к <code>shop.example</code>. Первый запрос прокси отправляет серверу №1, следующий — серверу №2, затем №3. Это и есть распределение по кругу, или round-robin.</p>
      </aside>
    </section>

    <section class="guide-section guide-basics" aria-labelledby="basics-title">
      <header class="guide-section-head">
        <span class="eyebrow">Четыре определения</span>
        <h2 id="basics-title">Минимум терминов для начала</h2>
      </header>
      <div class="guide-basics-grid">
        <article><strong>HTTP</strong><p>Общие правила, по которым клиент просит данные у сервера, а сервер отвечает. По этим правилам браузер открывает сайты, а сервисы общаются друг с другом.</p></article>
        <article><strong>Запрос и ответ</strong><p>Вы нажали «открыть страницу» — браузер отправил серверу просьбу, то есть запрос. Сервер вернул HTML или данные — это ответ.</p></article>
        <article><strong>Backend</strong><p>Обычный сервер с вашим приложением. Несколько backend-ов могут быть одинаковыми копиями одного сервиса, например интернет-магазина.</p></article>
        <article><strong>RPS и token bucket</strong><p>RPS — число запросов в секунду. Token bucket похож на коробку со 100 талонами: каждый запрос забирает один талон, а система, например, добавляет по одному талону каждую секунду.</p></article>
      </div>
    </section>

    <section class="guide-section" aria-labelledby="request-way-title">
      <header class="guide-section-head">
        <span class="eyebrow">Один запрос</span>
        <h2 id="request-way-title">Как запрос проходит через систему</h2>
        <p>Каждый шаг виден на схеме в Overview и в результате Request lab.</p>
      </header>
      <ol class="plain-flow">
        <li><span>1</span><div><strong>Клиент</strong><p>Браузер, приложение или другой сервис отправляет HTTP-запрос на адрес прокси.</p></div></li>
        <li><span>2</span><div><strong>Маршрут</strong><p>Прокси смотрит домен, путь, метод и при необходимости заголовки. Например, путь <code>/api/</code> выбирает маршрут <code>api</code>.</p></div></li>
        <li><span>3</span><div><strong>Группа серверов</strong><p>Маршрут указывает группу подходящих серверов. В конфигурации она называется cluster.</p></div></li>
        <li><span>4</span><div><strong>Конечный сервер</strong><p>Стратегия выбирает один доступный сервер — endpoint. Round-robin выбирает их по очереди.</p></div></li>
        <li><span>5</span><div><strong>Ответ</strong><p>Прокси получает ответ сервера и возвращает его клиенту. При разрешённой ошибке он может повторить запрос на другом сервере.</p></div></li>
      </ol>
    </section>

    <section class="guide-section" aria-labelledby="modes-title">
      <header class="guide-section-head">
        <span class="eyebrow">Два режима</span>
        <h2 id="modes-title">Demo и live выглядят одинаково, но делают разное</h2>
      </header>
      <div class="guide-mode-grid">
        <article class="guide-mode-card ${mode === 'demo' ? 'is-current' : ''}">
          <header><span>demo</span>${mode === 'demo' ? '<b>открыт сейчас</b>' : ''}</header>
          <h3>Безопасная интерактивная документация</h3>
          <p>Go-прокси и тестовые серверы не нужны. Поведение воспроизводит симулятор внутри браузера; он не отправляет запросы в вашу сеть.</p>
          <ul><li>Можно менять число включённых серверов.</li><li>Можно отправлять тестовый поток и получать 200, 429 или 503.</li><li>Изменения живут только в памяти вкладки, если отдельно не включено сохранение demo config.</li></ul>
        </article>
        <article class="guide-mode-card ${mode === 'live' ? 'is-current' : ''}">
          <header><span>live</span>${mode === 'live' ? '<b>открыт сейчас</b>' : ''}</header>
          <h3>Консоль настоящего локального запуска</h3>
          <p>Интерфейс читает status и config из Go management API. Запрос из Request lab реально проходит через data plane, а разрешённые изменения влияют на запущенный экземпляр.</p>
          <ul><li>Используйте live только со своим тестовым или локальным запуском.</li><li>Роль operator (оператор) управляет тестовыми запросами и конечными серверами.</li><li>Apply, Rollback, стратегия и общий ограничитель требуют роль admin (администратор).</li></ul>
        </article>
      </div>
    </section>

    <section class="guide-section" aria-labelledby="sections-title">
      <header class="guide-section-head">
        <span class="eyebrow">Навигация</span>
        <h2 id="sections-title">Что находится в каждом разделе</h2>
      </header>
      <div class="guide-section-map">
        <a href="#overview"><span>01</span><strong>Overview</strong><p>Общая статистика, путь запроса, состояние серверов и последние действия.</p></a>
        <a href="#request"><span>02</span><strong>Request lab</strong><p>Форма одного запроса, небольшой поток нагрузки, ответы и распределение по серверам.</p></a>
        <a href="#routes"><span>03</span><strong>Routes</strong><p>Правила выбора группы серверов: домен, путь, метод, приоритет, timeout и retry.</p></a>
        <a href="#clusters"><span>04</span><strong>Clusters</strong><p>Группы конечных серверов, их здоровье, включение, отключение и плавный вывод из работы.</p></a>
        <a href="#config"><span>05</span><strong>Config</strong><p>Полная конфигурация в текстовом формате JSON, проверка, применение новой версии и возврат предыдущей.</p></a>
        <a href="#guide"><span>06</span><strong>Как это работает</strong><p>Это руководство и более подробная инженерная справка.</p></a>
      </div>
    </section>

    <section class="guide-section" aria-labelledby="first-test-title">
      <header class="guide-section-head">
        <span class="eyebrow">Первый опыт</span>
        <h2 id="first-test-title">Безопасный сценарий для demo</h2>
        <p>Эти действия не обращаются к реальным внешним сервисам в demo-режиме.</p>
      </header>
      <ol class="safe-steps">
        <li><span>1</span><div><strong>Откройте Request lab.</strong><p>Оставьте <code>GET</code>, host <code>localhost</code> и путь <code>/api/</code>. Нажмите <b>Send once</b>. В ответе будут выбранные маршрут, группа и конечный сервер.</p></div></li>
        <li><span>2</span><div><strong>Посмотрите распределение.</strong><p>Нажмите <b>Start traffic</b>, подождите несколько секунд и нажмите <b>Stop traffic</b>. Диаграмма покажет, сколько запросов получил каждый сервер. <b>Burst ×20</b> отправляет 20 запросов одновременно.</p></div></li>
        <li><span>3</span><div><strong>Измените доступные серверы.</strong><p>В Clusters выберите другое число Enabled endpoints. Кнопка питания исключает сервер сразу. Кнопка <b>D</b> включает drain: новые запросы туда не идут, а уже начатые не обрываются.</p></div></li>
        <li><span>4</span><div><strong>Проверьте ограничение частоты.</strong><p>В Global token bucket задайте маленькую Capacity — запас разрешённых запросов, нажмите <b>Apply override</b> и запустите поток. Ответ 429 означает, что лимит сработал. <b>Reset my bucket</b> возвращает токены текущему тестовому клиенту.</p></div></li>
        <li><span>5</span><div><strong>Попробуйте конфигурацию.</strong><p>В Config сначала используйте <b>Validate</b>. <b>Apply revision</b> создаёт новую версию, а <b>Rollback previous</b> возвращает предыдущую. В live эти две кнопки меняют настоящий процесс и доступны только администратору.</p></div></li>
      </ol>
    </section>

    <section class="guide-section" aria-labelledby="codes-title">
      <header class="guide-section-head">
        <span class="eyebrow">Результат запроса</span>
        <h2 id="codes-title">Что означают основные HTTP-коды</h2>
      </header>
      <div class="status-code-grid">
        <article class="status-code status-code--ok"><code>200</code><div><strong>Запрос выполнен</strong><p>Маршрут найден, доступный сервер выбран и вернул успешный ответ.</p></div></article>
        <article class="status-code status-code--limit"><code>429</code><div><strong>Слишком много запросов</strong><p>В этом эксперименте так сообщает token bucket: талоны временно закончились. В live такой же код может вернуть и сам backend, поэтому всегда смотрите выбранный маршрут, конечный сервер и события рядом с ответом.</p></div></article>
        <article class="status-code status-code--unavailable"><code>503</code><div><strong>Запрос сейчас нельзя обработать</strong><p>Причиной могут быть недоступные backend-ы, общий предел одновременных запросов, перегрузка или режим fail-closed, который запрещает запрос при сбое хранилища. Иногда 503 возвращает сам backend. Status и Events помогают отличить причины.</p></div></article>
      </div>
    </section>

    <section class="guide-section" aria-labelledby="buttons-title">
      <header class="guide-section-head">
        <span class="eyebrow">Кнопки и изменения</span>
        <h2 id="buttons-title">Что именно меняется</h2>
      </header>
      <div class="guide-action-list">
        <div><strong>Send once / Start traffic / Burst</strong><p>Создают тестовые запросы. В live они проходят через настоящий прокси, поэтому используйте безопасный тестовый путь.</p></div>
        <div><strong>Power / Enabled endpoints</strong><p>Разрешают или запрещают новые запросы выбранным endpoint-ам. Запись из конфигурации не удаляется.</p></div>
        <div><strong>Drain</strong><p>Плавно выводит endpoint из работы: новые запросы не назначаются, уже выполняющиеся могут закончиться.</p></div>
        <div><strong>Strategy</strong><p>Меняет способ выбора сервера: по кругу, с весами, по минимальному числу активных запросов или по стабильному ключу.</p></div>
        <div><strong>Validate</strong><p>Проверяет структуру и ссылки конфигурации, но не применяет её. Это безопасный первый шаг.</p></div>
        <div><strong>Apply / Rollback</strong><p>Применяет новую версию или восстанавливает предыдущую. Сервер проверяет номер текущей revision, чтобы не затереть чужое изменение.</p></div>
      </div>
    </section>

    <section class="advanced-guide" aria-labelledby="advanced-title">
      <header class="guide-section-head">
        <span class="eyebrow">Инженерная справка</span>
        <h2 id="advanced-title">Термины, запуск и границы проекта</h2>
        <p>Этот блок нужен, когда основная схема уже понятна.</p>
      </header>
      <div class="guide-grid">
        <article class="guide-card"><span>01</span><h3>Listener</h3><p>Точка входа: адрес и протокол, на которых прокси принимает HTTP. Hostnames могут ограничить обслуживаемые домены.</p></article>
        <article class="guide-card"><span>02</span><h3>Route</h3><p>Правило сопоставления host, path, method и headers. Больший priority проверяется раньше. Action выбирает cluster, redirect или распределение с весами.</p></article>
        <article class="guide-card"><span>03</span><h3>Cluster</h3><p>Логическая группа upstream-серверов. Здесь задаются strategy, health-check, retry, transport, TLS и способ обнаружения адресов.</p></article>
        <article class="guide-card"><span>04</span><h3>Endpoint</h3><p>Один конкретный HTTP-сервер. Disable исключает его сразу; drain прекращает новые назначения без принудительного обрыва inflight-запросов.</p></article>
      </div>
      <section class="panel runbook">
        <header class="panel-header"><div><span class="eyebrow">Current mode</span><h2>${mode === 'demo' ? 'Статическая документация и browser simulator' : 'Локальная интеграция с management API'}</h2></div><span class="mode-badge mode-badge--${mode}">${mode}</span></header>
        <div class="runbook-steps">
          <article><b>Demo</b><code>npm run dev:demo</code><p>Не делает сетевых запросов к backend-ам. Эту сборку публикует GitHub Pages.</p></article>
          <article><b>Live frontend</b><code>npm run dev:live</code><p>Vite передаёт <code>/api/v1</code> в закрытый management listener. Credential остаётся на стороне proxy.</p></article>
          <article><b>Full stack</b><code>docker compose up --build</code><p>Запускает frontend, Go-прокси, хранилище limiter-а и тестовые backend-ы в одной локальной сети.</p></article>
        </div>
      </section>
      <section class="boundary-grid">
        <article><span>Data plane</span><p>Обрабатывает пользовательские запросы: выбирает route и endpoint, проксирует, повторяет разрешённые ошибки и защищается от перегрузки.</p></article>
        <article><span>Management plane</span><p>Показывает status, проверяет и применяет versioned config и разрешённые runtime-изменения. Его нельзя публиковать напрямую в Internet.</p></article>
        <article><span>External edge</span><p>Публичный TLS, identity оператора, WAF/DDoS-защита и DNS должны находиться перед проектом — в Ingress или cloud load balancer.</p></article>
      </section>
    </section>
  `
}
