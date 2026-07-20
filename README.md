# HTTP-балансировщик нагрузки на Go

Простой HTTP-балансировщик нагрузки, написанный на Go. Проект был сделан в рамках тестового задания для Cloud.ru и затем оформлен как pet-project.  
В основе — обратный прокси, round-robin-стратегия выбора backend'ов, периодический health-check и rate limiting с хранением состояния в PostgreSQL.

Для ознакомления создана интерактивная документация, доступная по ссылке (в проекте её можно использовать текже как полноценную клиентскую часть, взаимодействующую непосредственно с реализацией балансировщика) - https://j0es1ick.github.io/load_balancer/

## Возможности

- распределение HTTP-запросов между несколькими backend-серверами;
- стратегия балансировки `round-robin`;
- автоматическая проверка доступности backend'ов;
- исключение недоступных backend'ов из маршрутизации;
- rate limiting по IP-адресу клиента;
- хранение счётчиков rate limit в PostgreSQL;
- очистка устаревших токен-бакетов по расписанию;
- запуск через Docker Compose;
- graceful shutdown по `SIGINT` и `SIGTERM`.

## Как это работает

1. Клиент отправляет запрос на балансировщик.
2. Middleware rate limit определяет ключ клиента по IP-адресу.
3. Проверяется token bucket для этого IP:
   - если лимит исчерпан, возвращается `429 Too Many Requests`;
   - если лимит доступен, запрос идёт дальше.
4. Балансировщик выбирает следующий живой backend по round-robin.
5. Запрос проксируется на выбранный backend через `httputil.ReverseProxy`.
6. Параллельно работает health-check, который периодически проверяет backend'ы по TCP.
7. Если backend становится недоступен, он помечается как `dead` и временно исключается из ротации.

## Архитектура проекта

```text
cmd/balancer/main.go         — точка входа приложения
internal/config              — загрузка и хранение конфигурации
internal/server              — HTTP-сервер и middleware
internal/balancer            — балансировщик, backend pool, стратегии, health-check
internal/ratelimit           — token bucket rate limiter и PostgreSQL storage
config/config.yaml           — пример конфигурации
test/backend1, test/backend2  — простые backend'ы для локального запуска
```

## Структура запросов и ответы

Балансировщик обрабатывает все запросы на `/` и проксирует их на один из backend'ов.

### Основные ответы

- `200 OK` — запрос успешно проксирован на backend;
- `429 Too Many Requests` — превышен лимит запросов;
- `502 Bad Gateway` — ошибка при проксировании на backend;
- `503 Service Unavailable` — нет доступных backend'ов;
- `500 Internal Server Error` — ошибка rate limiter'а или внутренний сбой сервера.

## Требования

Для запуска понадобятся:

- Go `1.24.x` или совместимая версия;
- Docker и Docker Compose;
- PostgreSQL (если запускать не через compose).

## Конфигурация

Конфигурация читается из файла, путь к которому задаётся через переменную окружения `CONFIG_PATH`.

Пример:

```yaml
server:
  port: "8080"

database:
  host: "postgres"
  port: "5432"
  user: "postgres"
  password: "12345"
  name: "balancer"
  sslmode: "disable"
  connect_timeout: "5s"

backends:
  - "http://backend1:80"
  - "http://backend2:80"

rate_limit:
  default_capacity: 100
  default_rate: "1s"
```

### Поля конфигурации

#### `server.port`

Порт, на котором поднимается HTTP-сервер балансировщика.

#### `database`

Параметры подключения к PostgreSQL.

- `host` — хост базы данных;
- `port` — порт PostgreSQL;
- `user` — пользователь;
- `password` — пароль;
- `name` — имя базы;
- `sslmode` — режим SSL для подключения;
- `connect_timeout` — таймаут подключения.

#### `backends`

Список backend-адресов, между которыми распределяются запросы.

#### `rate_limit.default_capacity`

Максимальное число токенов в bucket'е для нового клиента.

#### `rate_limit.default_rate`

Интервал, через который восстанавливается 1 токен.

Пример интерпретации: если `default_capacity = 100` и `default_rate = "1s"`, то у клиента есть 100 запросов в запасе, а затем токены будут восстанавливаться по одному раз в секунду.

## Rate limiting

Rate limit реализован по схеме token bucket.

### Что это означает

- каждому клиенту соответствует отдельный bucket;
- ключ bucket'а — IP-адрес клиента;
- при каждом запросе из bucket'а забирается 1 токен;
- если токенов нет — запрос блокируется;
- bucket автоматически пополняется со временем.

### Где хранятся данные

Состояние rate limit хранится в PostgreSQL в таблице `ratelimit`.

Структура таблицы создаётся автоматически при старте приложения:

```sql
CREATE TABLE IF NOT EXISTS ratelimit (
    key TEXT PRIMARY KEY,
    capacity INTEGER NOT NULL,
    tokens INTEGER NOT NULL,
    rate TEXT NOT NULL,
    last_refill TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
```

### Очистка устаревших записей

Фоновый worker раз в 6 часов удаляет записи, у которых `last_refill` старше 24 часов. Это помогает не накапливать бесконечное число записей для редко используемых IP.

## Балансировка нагрузки

Используется стратегия `round-robin`.

### Поведение

- backend'ы перебираются по очереди;
- если backend помечен как недоступный, он пропускается;
- если не осталось ни одного живого backend'а, возвращается `503`.

### Health-check

Health-check выполняется каждые 5 секунд и проверяет доступность backend'ов через TCP-подключение с таймаутом 2 секунды.

Важно: проверка на доступность не делает HTTP-запрос `GET /`, а проверяет именно TCP-доступность хоста и порта. Это означает, что backend может отвечать на TCP, но при этом иметь проблемы на уровне HTTP-приложения.

## Локальный запуск через Docker Compose

### 1. Проверить конфигурацию

В `config/config.yaml` должны быть указаны:

- адрес PostgreSQL;
- список backend'ов;
- параметры rate limit;
- порт сервера.

Для compose-запуска в этом проекте используется:

- `postgres` как host базы;
- `backend1` и `backend2` как backend-имена внутри сети Docker;
- `CONFIG_PATH=/config/config.yaml`.

### 2. Запустить проект

```bash
docker-compose up --build
```

### 3. Проверить работу

```bash
curl http://localhost:8080/
```

Ответы будут приходить от разных backend'ов по очереди.

## Пример ручного тестирования

### Проверка round-robin

Сделайте несколько запросов подряд:

```bash
for i in {1..6}; do curl -s http://localhost:8080/; echo; done
```

В ответе должен меняться backend.

### Проверка rate limit

Если лимит небольшой, можно быстро получить `429 Too Many Requests`:

```bash
ab -n 1000 -c 50 http://localhost:8080/
```

или через `curl` в цикле.

## Запуск без Docker

Если нужно поднять проект вручную:

1. Запустите PostgreSQL.
2. Укажите корректный `CONFIG_PATH`.
3. Проверьте список backend'ов в конфиге.
4. Запустите приложение:

```bash
go run ./cmd/balancer
```

## Пример переменной окружения

```bash
export CONFIG_PATH=./config/config.yaml
go run ./cmd/balancer
```

## Точки входа в код

### `cmd/balancer/main.go`

Собирает все компоненты вместе:

- загружает конфигурацию;
- создаёт pool backend'ов;
- запускает health-check;
- подключается к PostgreSQL;
- создаёт limiter;
- поднимает HTTP-сервер;
- обрабатывает сигналы завершения.

### `internal/server`

Собирает HTTP middleware-цепочку:

- rate limit middleware;
- handler балансировщика.

### `internal/balancer`

Содержит:

- `Backend` — описание одного backend'а;
- `BackendPool` — набор backend'ов;
- `RoundRobinStrategy` — выбор следующего backend'а;
- `HealthChecker` — проверка доступности backend'ов;
- `LoadBalancer` — HTTP handler, который проксирует запрос.

### `internal/ratelimit`

Содержит:

- `TokenBucket` — логика токен-бакета;
- `TokenBucketLimiter` — обёртка для проверки лимита;
- `Database` — хранилище на PostgreSQL;
- middleware для защиты HTTP-цепочки.

## Тестирование

В проекте есть unit-тесты для:

- балансировщика;
- стратегии round-robin;
- health-check;
- rate limiter'а;
- PostgreSQL storage слоя.

Запуск:

```bash
go test ./...
```

## Известные ограничения

- Перезагрузка конфигурации по `SIGHUP` реализована, но текущие компоненты после старта продолжают использовать уже загруженные зависимости. То есть изменение `config.yaml` во время работы не меняет поведение запущенного процесса без дополнительной логики повторной инициализации.
- Health-check проверяет доступность backend'а по TCP, а не HTTP-ответ.
- Rate limiting привязан к IP-адресу клиента. Если несколько пользователей выходят в интернет через один NAT, они могут делить один и тот же лимит.
- Ошибка при парсинге backend URL приводит к аварийному завершению при старте, потому что `url.Parse` обрабатывается через `log.Fatal`.
- Для простоты не реализованы:
  - sticky sessions;
  - weighted round-robin;
  - retries;
  - circuit breaker;
  - metrics и tracing;
  - административный API.

## Что можно улучшить дальше (возможно будет реализовано, когда появится время)

- добавить поддержку нескольких стратегий балансировки;
- сделать HTTP-based health-check;
- вынести rate limit в отдельную БД/Redis-реализацию;
- добавить метрики Prometheus;
- добавить structured logging;
- сделать динамическую перезагрузку конфигурации на практике;
- добавить `/healthz` и `/metrics`.

## Локальная демонстрация

В репозитории уже лежат два простых backend'а в `test/backend1` и `test/backend2`. Они используются как наглядный пример распределения запросов.

### Веб-dashboard

SPA поддерживает два режима через Vite `.env`:

- `live` — полноценная интеграция с Go-балансировщиком: фактический backend для каждого запроса, реальный token bucket, TCP health-state и управление нодами;
- `demo` — автономная интерактивная документация для GitHub Pages, которая воспроизводит те же сценарии прямо в браузере без сервера и базы данных.

Обычный локальный запуск по-прежнему поднимает `live`-режим:

```bash
docker-compose up --build
```

Также поддерживается синтаксис Compose Plugin: `docker compose up --build`.

После запуска откройте `http://localhost:3000`. Сам балансировщик по-прежнему доступен на `http://localhost:8080`.

Для автономного режима:

```bash
cd frontend
npm ci
npm run dev:demo
```

Деплой GitHub Pages выполняет workflow `.github/workflows/pages.yml`. Подробные команды и переменные окружения описаны в `frontend/README.md`.
