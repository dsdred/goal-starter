# GoAl

GoAl — лёгкий кроссплатформенный менеджер для локальных AI-рантаймов, моделей и профилей запуска. Один бинарник для Windows и Linux.

## Статус

**v0.9 — Multi-Instance Supervisor, Domain Models, Launch Resolver, Structured API Errors.**

> Этот репозиторий — архитектурный стартер, не готовая к продакшену система.

## Поддерживаемые платформы

- Windows amd64
- Linux amd64
- Планируется: arm64

## Быстрый старт на Windows

```powershell
.\scripts\bootstrap-windows.ps1
Copy-Item goal.example.json goal.json
$env:GOAL_CONFIG = (Resolve-Path .\goal.json)
go run .\cmd\goal
```

## Сборка

### Полная кросс-компиляция (Windows + Linux)

```powershell
.\scripts\build-all.ps1
```

Результат:
- `bin/goal-windows-amd64.exe` — бинарник для Windows
- `bin/goal-linux-amd64` — бинарник для Linux
- `bin/checksums.txt` — SHA256 контрольные суммы

### Ручная кросс-компиляция

```powershell
# Windows
$env:GOOS='windows'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; go build -o bin/goal-windows-amd64.exe ./cmd/goal

# Linux
$env:GOOS='linux'; $env:GOARCH='amd64'; go build -o bin/goal-linux-amd64 ./cmd/goal

Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

## Обязательные проверки

```powershell
gofmt -w .
go test ./...
go test -race ./...  # требует CGO_ENABLED=1 и gcc
go vet ./...
go build ./cmd/goal
```

## Конфигурация

Скопируйте и отредактируйте пример:

```powershell
Copy-Item goal.example.json goal.json
```

Файл `goal.json` исключён из git (содержит секреты и пути пользователя).

### Миграция конфигурации

GoAl автоматически мигрирует конфигурацию при запуске. Поддерживаются шаги:
- `1 -> 2`: Применение значений по умолчанию для отсутствующих полей (healthCheck для профилей и рантаймов)

Статус миграции: `GET /api/v1/migration/status`

### Горячая перезагрузка конфигурации

Конфигурация автоматически перезагружается при изменении файла.

## API endpoints

### Аутентификация

| Method | Path | Описание |
|--------|------|----------|
| POST | `/api/v1/auth/login` | Войти (HTTP-only cookies) |
| POST | `/api/v1/auth/logout` | Выйти |
| GET | `/api/v1/auth/session` | Проверить сессию |

### Управление процессом

| Method | Path | Описание |
|--------|------|----------|
| GET | `/` | Веб-панель управления |
| GET | `/api/v1/status` | Статус процесса |
| GET | `/api/v1/health` | Health check (заглушка) |
| GET | `/api/v1/version` | Версия и metadata |
| GET | `/api/v1/metrics` | Метрики приложения |
| GET | `/api/v1/logs/stream` | SSE поток логов |
| GET | `/api/v1/logs/query` | Поиск и пагинация логов |
| GET | `/api/v1/migration/status` | Статус миграции конфигурации |

### Управление экземплярами (Instances)

Профиль — это шаблон запуска. Экземпляр (Instance) — запущенный процесс.

| Method | Path | Описание |
|--------|------|----------|
| GET | `/api/v1/instances` | Список всех экземпляров |
| GET | `/api/v1/instances/{id}` | Статус экземпляра |
| POST | `/api/v1/instances/{id}/stop` | Остановить экземпляр |
| POST | `/api/v1/instances/{id}/restart` | Перезапустить экземпляр |

### Профили (CRUD)

Профиль — шаблон запуска, не процесс.

| Method | Path | Описание |
|--------|------|----------|
| GET | `/api/v1/profiles` | Список профилей |
| GET | `/api/v1/profiles/{id}` | Получить профиль |
| POST | `/api/v1/profiles` | Создать профиль |
| PUT | `/api/v1/profiles/{id}` | Обновить профиль |
| DELETE | `/api/v1/profiles/{id}` | Удалить профиль |
| POST | `/api/v1/profiles/{id}/resolve` | Preview команды запуска |
| POST | `/api/v1/profiles/{id}/start` | Запустить процесс по профилю |
| POST | `/api/v1/profiles/{id}/stop` | Остановить все процессы профиля |
| POST | `/api/v1/profiles/{id}/restart` | Перезапустить процессы профиля |
| GET | `/api/v1/profiles/{id}/status` | Статус процессов по профилю |
| POST | `/api/v1/profiles/{id}/activate` | Активировать профиль |
| POST | `/api/v1/profiles/{id}/deactivate` | Деактивировать профиль |

### Рантаймы

| Method | Path | Описание |
|--------|------|----------|
| GET | `/api/v1/runtimes` | Список рантаймов |
| GET | `/api/v1/runtimes/{id}` | Получить рантайм |
| POST | `/api/v1/runtimes` | Создать рантайм |
| PUT | `/api/v1/runtimes/{id}` | Обновить рантайм |
| DELETE | `/api/v1/runtimes/{id}` | Удалить рантайм |
| POST | `/api/v1/runtimes/{id}/start` | Запустить процесс рантайма |
| POST | `/api/v1/runtimes/{id}/stop` | Остановить процесс рантайма |
| POST | `/api/v1/runtimes/{id}/restart` | Перезапустить процесс рантайма |
| GET | `/api/v1/runtimes/health` | Проверка здоровья всех рантаймов |
| GET | `/api/v1/runtimes/health/{id}` | Проверка здоровья конкретного рантайма |

### Модели

| Method | Path | Описание |
|--------|------|----------|
| GET | `/api/v1/models` | Список моделей |
| GET | `/api/v1/models/{id}` | Получить модель |
| POST | `/api/v1/models` | Создать модель |
| PUT | `/api/v1/models/{id}` | Обновить модель |
| DELETE | `/api/v1/models/{id}` | Удалить модель |

### SSE / WebSocket

| Method | Path | Описание |
|--------|------|----------|
| GET | `/api/v1/logs/stream` | SSE поток логов |
| GET | `/api/v1/logs/query` | Поиск и пагинация логов |
| GET | `/ws` | WebSocket поток логов (WIP) |

### Structured API errors

Все ошибки возвращаются в структурированном JSON формате:

```json
{
  "error": {
    "error_code": "invalid_port",
    "error": "invalid port: out of range",
    "details": []
  }
}
```

Доступные коды ошибок:
- `bad_request` — невалидный запрос
- `unauthorized` — неавторизован
- `forbidden` — запрещено (CSRF failure)
- `not_found` — ресурс не найден
- `conflict` — конфликт (процесс уже запущен)
- `invalid_port` — невалидный порт
- `invalid_host` — невалидный хост
- `invalid_address` — невалидный адрес
- `invalid_profile` — невалидный профиль
- `invalid_runtime` — невалидный рантайм
- `invalid_model` — невалидная модель
- `internal_server_error` — внутренняя ошибка

## Безопасность

- **Аутентификация** — HTTP-only cookies, session-based
- **CSRF защита** — CSRF token для всех unsafe методов
- **Rate limiting** — 100 запросов в минуту на IP

## Архитектура

### Управление процессами

GoAl управляет одним процессом за раз через `process.Manager`. Каждый `exec.Cmd` имеет ровно одного владельца, вызывающего `Wait()`. Process lifecycle управляется через `platform.ProcessControl` интерфейс:

- **Windows**: Job Object с kill-on-close
- **Linux**: Process group (SIGTERM/SIGKILL)

Среды процессов сливаются с окружением родительского процесса (переменные профиля переопределяют системные).

### Логирование

Все логи процессов сохраняются в `LogStore` (кольцевой буфер до 10000 записей). Поддерживается:
- SSE стриминг в реальном времени (`/api/v1/logs/stream`)
- Фильтрация по stream, search, time range
- Пагинация (page/page_size)

### Хранение данных

Профили, рантаймы и модели хранятся в JSON-файлах в `dataDir`:
- `data/profiles.json`
- `data/runtimes.json`
- `data/models.json`

### Health Checks

Периодическая проверка здоровья рантаймов (каждые 30 секунд). Поддерживаются TCP и HTTP health check'и.

## Структура репозитория

| Путь | Назначение |
|------|------------|
| `cmd/goal/` | Точка входа приложения |
| `cmd/goal-msi/` | MSI installer builder |
| `internal/config/` | Парсинг конфигурации, валидация, hot-reload, миграции |
| `internal/process/` | Управление жизненным циклом процессов, log store |
| `internal/platform/` | OS-специфичная обработка процессов |
| `internal/version/` | Версия и metadata |
| `internal/webui/` | HTTP-сервер и шаблоны |
| `internal/webui/errors/` | Structured API errors |
| `internal/webui/security/` | Аутентификация, CSRF, sessions |
| `internal/webui/validation/` | Валидация портов, хостов, адресов |
| `internal/webui/middleware/` | Logging middleware |
| `internal/webui/store/` | Файловый store (profiles, runtimes, models) |
| `internal/webui/health/` | Health check рантаймов |
| `internal/webui/metrics/` | Прикладные метрики |
| `internal/webui/websocket/` | WebSocket поток логов |
| `internal/webui/logger/` | HTTP логгер |
| `testdata/fake-runtime/` | Фейковый рантайм для интеграционных тестов |
| `deploy/` | Systemd-сервисы, поддержка Windows-сервиса |
| `scripts/` | Скрипты сборки и настройки |
| `web/`, `webui/` | Статические файлы и шаблоны (дубли для совместимости) |

## .gitignore

Из репозитория исключены:

- `bin/` — артефакты сборки
- `data/` — данные рантайма
- `goal.json` — конфигурация пользователя
- `*.log`, `*.tmp`, `*.bak` — временные файлы
- `*.exe` — скомпилированные бинарники (кроме корневого `goal.exe`)
- `.env*` — секреты окружения

## Перед началом разработки

Ознакомьтесь с `AGENTS.md`, `BACKLOG.md`, `ROADMAP.md` и `SUBAGENT_MASTER_PROMPT.md`.