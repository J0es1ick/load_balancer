# Interactive documentation SPA

Один TypeScript/Vite интерфейс собирается в двух режимах.

## Demo — GitHub Pages

`demo` не обращается к API. Round-robin, token bucket, выбираемый пул из 1–8 backend-ов, runtime-настройки, retry budget и ответы `429`/`503` воспроизводятся локально в браузере. Это не мониторинг запущенного сервера, а автономная интерактивная документация.

```bash
npm ci
npm run dev:demo
npm run build:demo
npm run preview:demo
```

При push в `master` workflow собирает этот режим с `VITE_BASE_PATH=/${repository-name}/` и публикует `dist` через GitHub Pages. В репозитории один раз выберите **Settings → Pages → Source → GitHub Actions**.

## Live — локальная интеграция

`live` читает защищённый `/api/dashboard/status`, динамически строит список backend-ов, отправляет тестовые запросы через настоящий limiter/proxy и показывает circuit/inflight/slow-start state. Селектор «Активных backend» вызывает `POST /api/dashboard/backends` и включает первые N нод реального локального пула. Compose содержит 8 nginx upstream-ов, из которых по умолчанию включены первые два. При разрешённых runtime mutations интерфейс также включает, исключает и drain'ит отдельные backend-ы и применяет настройки.

Рекомендуемый запуск всего проекта:

```bash
./scripts/init-local.sh
docker compose up --build
```

Откройте `http://127.0.0.1:3000`. Nginx внутри frontend-контейнера проксирует `/api/` на management listener и добавляет bearer token сервер-сервер; credential не попадает в JavaScript.

Изменяющие запросы SPA отправляют `Content-Type: application/json` и `X-Balancer-CSRF: 1`. Management API отклоняет cross-site и простые form-запросы, поэтому сторонний сайт не может воспользоваться токеном, который nginx добавляет автоматически.

Адрес data plane, который интерфейс показывает и копирует в live-режиме, задаётся build-переменной `VITE_PUBLIC_URL`. Корневой Compose передаёт её автоматически из `.env`, созданного `scripts/init-local.*`.

Для отдельного Vite dev-server:

```bash
# frontend/.env.live.local — файл игнорируется Git
VITE_MANAGEMENT_TOKEN=то-же-значение-что-BALANCER_ADMIN_TOKEN

npm run dev:live
```

Vite отправляет `/api` на `VITE_API_PROXY_TARGET`, по умолчанию `http://127.0.0.1:9090`.

## Environment

| Переменная | Использование |
| --- | --- |
| `VITE_APP_MODE=demo\|live` | Выбор автономной модели или Go API |
| `VITE_BASE_PATH` | `/` локально, `/<repo>/` на project Pages |
| `VITE_API_PROXY_TARGET` | Только dev proxy в live mode |
| `VITE_MANAGEMENT_TOKEN` | Только локальный dev proxy; запрещено задавать в Pages build |

`npm run dev` и `npm run build` остаются алиасами live-режима.

## Что меняет Runtime form

В live mode `PATCH /api/dashboard/config` обновляет capacity/refill/failure mode, health interval/timeout/thresholds/slow start и retry attempts/timeout/budget. Изменения находятся в памяти процесса до рестарта либо следующего `SIGHUP`. Форма не меняет YAML и не управляет secrets, listeners, global overload semaphore или storage connections.

В Kubernetes-шаблоне frontend развёртывается как внутренний ClusterIP без public Ingress, а `management.runtime_mutations` выключен. UI автоматически блокирует изменяющие controls и показывает ID закреплённой реплики. Persistent production-настройки выполняются через versioned ConfigMap и rolling deployment; панель остаётся диагностическим интерфейсом.

В demo mode поля меняют только браузерную модель. Поэтому опубликованный сайт остаётся полностью статическим и безопасным.
