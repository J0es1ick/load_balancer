# Proxy Console

TypeScript/Vite SPA для `proxy/v1`. Один интерфейс работает через два адаптера:

- `demo` — статическая документация и детерминированный browser simulator, без сетевых запросов;
- `live` — консоль запущенного Go proxy через защищённый management API `/api/v1`.

Разделы интерфейса соответствуют runtime-модели: Overview, Request lab, Routes, Clusters, Config и Guide. Dashboard не является источником production-конфигурации: при `runtime_mutations_enabled=false` изменяющие controls блокируются.

## Команды

```bash
npm ci
npm run dev:demo
npm run dev:live
npm run build:demo
npm run build:live
npm run test:simulator
```

GitHub Pages собирает `demo` с `VITE_BASE_PATH=/<repository>/`. Этот build не содержит management token и не обращается к приватному API.

Для локального Vite live-server создайте игнорируемый `.env.live.local`:

```dotenv
BALANCER_ADMIN_TOKEN_FILE=/absolute/path/to/local/admin-token
# либо BALANCER_ADMIN_TOKEN=local-development-token
```

Vite проксирует `/api` на `VITE_API_PROXY_TARGET`. В контейнере frontend nginx добавляет bearer token server-side, поэтому credential не попадает в JavaScript.

## Adapter contract

Live adapter использует:

| Method | Path | Назначение |
| --- | --- | --- |
| `GET` | `/api/v1/status` | runtime snapshot и counters |
| `GET` | `/api/v1/config` | текущий `GatewayConfig` |
| `POST` | `/api/v1/config/validate` | проверка `{config}` без apply |
| `PUT` | `/api/v1/config` | атомарный apply `{config, expected_revision}` |
| `POST` | `/api/v1/config/rollback` | rollback с optimistic revision |
| `PATCH` | `/api/v1/clusters/{cluster}/endpoints/{id}` | enable/disable/drain endpoint |
| `POST` | `/api/v1/request` | синтетический запрос через data plane |
| `PATCH` | `/api/v1/rate-limit` | admin-only runtime override global token bucket |
| `POST` | `/api/v1/rate-limit/reset` | admin-only reset bucket текущего verified client |

Изменяющие запросы отправляют `Content-Type: application/json` и `X-Balancer-CSRF: 1`.

Console следует ролям из `status.principal` и не подменяет серверную авторизацию:

| Роль | Доступные действия |
| --- | --- |
| `viewer` | чтение status/config, локальное редактирование и pure validation |
| `operator` | всё viewer + Request lab и lifecycle endpoint-ов |
| `admin` | всё operator + apply/rollback конфигурации и смена стратегии |

`runtime_mutations_enabled=false` дополнительно блокирует runtime-операции для любой роли. Runtime rate-limit override относится только к текущему instance, не меняет `GatewayConfig` и не переживает restart; в multi-replica deployment его нужно применять через automation ко всем экземплярам. Сервер обязан повторно проверять роль каждого запроса: disabled control в браузере — только UX, не граница безопасности.

Demo adapter реализует тот же контракт в памяти: host/path/method/header matching, priority, redirects, route rate limit, round-robin и другие заявленные стратегии, endpoint lifecycle, validation, optimistic revisions и rollback. Хранение demo config в `localStorage` включается пользователем в разделе Config и по умолчанию выключено.

## Browser rendering regression

После `npm run dev:demo` откройте `http://localhost:5173/tests/rendering.html` и нажмите **Run rendering tests**. Fixture использует настоящие `ProxyConsole` и `DemoAdapter`: запускает 25 RPS и проверяет, что incremental render не заменяет DOM controls, не сбрасывает раскрытый `details`, focus/selection/scroll редактора, не теряет endpoint click и действительно останавливает traffic counters. Адаптер запускается без Storage, поэтому fixture не читает и не изменяет сохранённую demo-конфигурацию пользователя. Эта Vite test route не входит в production build entrypoint.

## Environment

| Переменная | Использование |
| --- | --- |
| `VITE_APP_MODE=demo\|live` | Выбор adapter-а во время сборки |
| `VITE_BASE_PATH` | Base URL для GitHub Pages или корня домена |
| `VITE_API_PROXY_TARGET` | Target только для Vite dev proxy |
| `BALANCER_ADMIN_TOKEN[_FILE]` | Server-only token локального dev proxy; `_FILE` перечитывается для каждого запроса |
| `VITE_PUBLIC_URL` | Публичный адрес data plane, передаваемый Docker build |

## Frontend nginx → management mTLS

Без дополнительной настройки nginx обращается к `http://balancer:9090`. Для защищённого upstream включите:

```dotenv
BALANCER_MANAGEMENT_TLS_ENABLED=true
BALANCER_MANAGEMENT_TLS_CA_FILE=/var/run/secrets/balancer-management/ca.crt
BALANCER_MANAGEMENT_TLS_CERT_FILE=/var/run/secrets/balancer-management/tls.crt
BALANCER_MANAGEMENT_TLS_KEY_FILE=/var/run/secrets/balancer-management/tls.key
BALANCER_MANAGEMENT_TLS_SERVER_NAME=balancer
```

Entrypoint проверяет hostname, безопасные пути и доступность всех файлов, затем включает HTTPS, client certificate, CA verification, SNI и TLS 1.2/1.3. При неполной конфигурации контейнер завершается, а не откатывается к незашифрованному соединению.

## Структура

```text
src/
  adapters/     demo/live adapters и simulator tests
  config/       стартовый proxy/v1 snapshot
  styles/       base, layout, components, responsive
  ui/           shell, HTML helpers, topology geometry
  views/        отдельный модуль каждого раздела
  app.ts        состояние и orchestration
  types.ts      JSON contract management API
```
