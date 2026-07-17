# Balancer Lab SPA

Интерактивный dashboard для запущенного Go-балансировщика. Запросы из интерфейса проходят через реальный rate limit middleware, round-robin и reverse proxy; состояние backend'ов и token bucket читается из Go API.

## Запуск

Весь проект запускается одной командой из корня репозитория:

```bash
docker-compose up --build
```

В окружениях с Compose Plugin эквивалентная команда — `docker compose up --build`.

После запуска dashboard доступен на `http://localhost:3000`, а балансировщик напрямую — на `http://localhost:8080`.

Для разработки frontend отдельно:

```bash
cd frontend
npm install
npm run dev
```

Vite проксирует `/api` на Go-сервис по адресу `http://127.0.0.1:8080`. Production-сборка:

```bash
npm run build
npm run preview
```

## Что можно проверить

- фактическое распределение запросов между `backend1` и `backend2`;
- реальное исключение ноды из backend pool;
- настоящий ответ `429` при исчерпании PostgreSQL-backed token bucket;
- настоящий ответ `503`, когда из ротации исключены все backend'ы;
- восстановление токенов с частотой из `config/config.yaml`;
- TCP health-state, burst и непрерывный автотрафик.

Dashboard использует demo API `/api/dashboard/*`. Выбор профиля bucket сбрасывает реальную запись текущего клиента до 8, 12 или 100 токенов. Переключатель backend'а меняет его участие в round-robin, не останавливая Docker-контейнер; фактическая TCP-доступность продолжает обновляться health-check'ом независимо.
