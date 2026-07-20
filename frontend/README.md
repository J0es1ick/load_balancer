# Balancer Lab SPA

Один интерфейс работает в двух режимах, которые выбираются Vite environment-файлом.

## `demo` — интерактивная документация

Автономный браузерный режим: воспроизводит round-robin, token bucket, ответы `429`/`503`, health-анимацию, отключение backend'ов и метрики без Go API, PostgreSQL и Docker. Он предназначен для GitHub Pages.

```bash
cd frontend
npm ci
npm run dev:demo
```

Production-сборка:

```bash
npm run build:demo
npm run preview
```

Параметры лежат в `.env.demo`. Значение `VITE_BASE_PATH=/cloud_test_assignment/` соответствует текущему имени репозитория. При переименовании репозитория workflow сам подставит актуальный путь.

## `live` — интеграция с Go-балансировщиком

Запросы проходят через реальный rate limit middleware, round-robin и reverse proxy. Состояние backend'ов и token bucket читается из Go API.

Весь проект запускается из корня репозитория:

```bash
docker compose up --build
```

Dashboard будет доступен на `http://localhost:3000`, балансировщик — на `http://localhost:8080`.

Для отдельной разработки фронта сначала запустите Go-часть, затем:

```bash
cd frontend
npm ci
npm run dev:live
```

Vite проксирует `/api` на адрес из `VITE_API_PROXY_TARGET` в `.env.live`. Production-сборка live-режима:

```bash
npm run build:live
```

Команды `npm run dev` и `npm run build` оставлены алиасами для `live`, поэтому прежний локальный сценарий не меняется.

## Переменные окружения

| Переменная | Назначение |
| --- | --- |
| `VITE_APP_MODE=demo\|live` | Выбирает автономную модель или настоящий API |
| `VITE_BASE_PATH` | Базовый URL Vite; `/` локально, `/<repo>/` на GitHub Pages |
| `VITE_API_PROXY_TARGET` | Адрес Go API для dev-прокси live-режима |

## GitHub Pages

Workflow `.github/workflows/pages.yml` при push в `master`:

1. устанавливает зависимости из `frontend/package-lock.json`;
2. запускает `npm run build:demo`;
3. подставляет `/${{ github.event.repository.name }}/` как base path;
4. публикует `frontend/dist` через GitHub Pages.

В репозитории один раз выберите **Settings → Pages → Source → GitHub Actions**. После успешного workflow сайт будет доступен по адресу `https://<owner>.github.io/<repository>/`.

## Что можно проверить в live-режиме

- фактическое распределение запросов между `backend1` и `backend2`;
- реальное исключение ноды из backend pool;
- настоящий ответ `429` при исчерпании PostgreSQL-backed token bucket;
- настоящий ответ `503`, когда из ротации исключены все backend'ы;
- восстановление токенов с частотой из `config/config.yaml`;
- TCP health-state, burst и непрерывный автотрафик.

Dashboard использует служебный API `/api/dashboard/*`. Выбор профиля bucket сбрасывает реальную запись текущего клиента до 8, 12 или 100 токенов. Переключатель backend'а меняет его участие в round-robin, не останавливая Docker-контейнер.
