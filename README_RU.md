# GoAl

GoAl — лёгкий кроссплатформенный менеджер для локальных AI-рантаймов, моделей и профилей запуска. Один бинарник для Windows и Linux.

## Статус

**v0.9 — Architecture Consolidation (supervisor, instance model, application services).**

> Этот репозиторий — архитектурный стартер. Безопасность и надёжность в процессе доработки.

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

Горячая перезагрузка определена в `internal/config` (`ReloadConfig`, `Watch`) но **пока не подключена** в main. Конфигурация читается один раз при запуске через `config.Load()`.

Поддерживаются безопасные live-обновления для:
- `logLevel` — изменение уровня логирования без перезапуска
- `healthCheck.interval` — изменение частоты health check

Поля, требующие перезапуск:
- `listenAddress`, `webPort`, `dataDir`

Статус: **WIP** — planned: reload coordinator и restart-required reporting.

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
| GET | `/api/v1/health` | Health check |
| GET | `/api/v1/metrics` | Метрики приложения |
| GET | `/api/v1/migration/status` | Статус миграции конфигурации |

> `/api/v1/status` удалён. Используйте `GET /api/v1/instances` и `GET /api/v1/instances/{id}`.

### Управление экземплярами (Instances)

Профиль — это шаблон запуска. Экземпляр (Instance) — запущенный процесс.

| Method | Path | Описание |
|--------|------|----------|
| GET | `/api/v1/instances` | Список всех экземпляров (из Supervisor) |
| GET | `/api/v1/instances/{id}` | Статус экземпляра |
| POST | `/api/v1/instances/{id}/stop` | Остановить экземпляр (auth + CSRF) |
| POST | `/api/v1/instances/{id}/restart` | Перезапустить экземпляр (auth + CSRF) |

### Профили (CRUD)

Профиль — шаблон запуска, не процесс.

| Method | Path | Описание |
|--------|------|----------|
| GET | `/api/v1/profiles` | Список профилей |
| GET | `/api/v1/profiles/{id}` | Получить профиль |
| POST | `/api/v1/profiles` | Создать профиль |
| PUT | `/api/v1/profiles/` | Обновить профиль |
| DELETE | `/api/v1/profiles/` | Удалить профиль |
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
| PUT | `/api/v1/runtimes/` | Обновить рантайм |
| DELETE | `/api/v1/runtimes/` | Удалить рантайм |
| POST | `/api/v1/runtimes/{id}/start` | Запустить процесс рантайма |
| POST | `/api/v1/runtimes/{id}/stop` | Остановить процесс рантайма |
| POST | `/api/v1/runtimes/{id}/restart` | Перезапустить процесс рантайма |
| POST | `/api/v1/runtimes/{id}/action/{action}` | action: start, stop, restart (legacy) |
| GET | `/api/v1/runtimes/health` | Проверка здоровья всех рантаймов |
| GET | `/api/v1/runtimes/health/{id}` | Проверка здоровья конкретного рантайма |

### Модели

| Method | Path | Описание |
|--------|------|----------|
| GET | `/api/v1/models` | Список моделей |
| GET | `/api/v1/models/{id}` | Получить модель |
| POST | `/api/v1/models` | Создать модель |
| PUT | `/api/v1/models/` | Обновить модель |
| DELETE | `/api/v1/models/` | Удалить модель |

### Логи

| Method | Path | Описание |
|--------|------|----------|
| GET | `/api/v1/logs/stream` | SSE поток логов (заглушка) |
| GET | `/api/v1/logs` | QueryLogs с instance_id filter (заглушка) |
| GET | `/api/v1/instances/{id}/logs` | Логи конкретного instance (заглушка) |
| GET | `/api/v1/instances/{id}/logs/stream` | SSE логов instance (заглушка) |
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
- **CSRF защита** — CSRF token для всех unsafe методов (GET, HEAD, OPTIONS, DELETE защищены)
- **Rate limiting** — 100 запросов в минуту на IP
- **Login rate limit** — 5 попыток / 5 минут
- **Request body size limit** — http.MaxBytesReader
- **Default bind** — 127.0.0.1 (все интерфейсы требуют явного config)
- **Secret env vars** — `AdminPassword` очищается при сохранении
- **External bind** — требует изменения `listenAddress` в конфиге
- **Runtime path validation** — executable и working directory проверяются against allowed roots

## Архитектура

### Модель Profile → Instance

**Profile** — шаблон запуска (конфигурация).
**Instance** — запущенный процесс (runtime entity).

```
Profile (static)
  ├─ runtime_id → Runtime
  ├─ model_id → Model (optional)
  ├─ args, environment, active
  └─ ...

Instance (dynamic, создан при start)
  ├─ profile_id → Profile
  ├─ pid, state, exit_code
  ├─ started_at, stopped_at
  └─ ...
```

Разделение означает:
- Профили независимы от жизненного цикла процессов
- Несколько экземпляров могут делить один профиль
- Остановка экземпляра не удаляет профиль
- Restart создаёт новый экземпляр с новым ID

### Управление процессами

GoAl использует multi-instance `Supervisor` который управляет несколькими `process.Manager` — по одному на каждый экземпляр запуска. Каждый `exec.Cmd` имеет ровно одного владельца, вызывающего `Wait()`. Process lifecycle управляется через `platform.Prepare`:

- **Windows**: Job Object с kill-on-close
- **Linux**: Process group (SIGTERM/SIGKILL)

Среды процессов сливаются с окружением родительского процесса (переменные профиля переопределяют системные).

SysProcAttr убран из `CommandSpec` — платформенная настройка выполняется через `platform.Prepare`.

### Recovery Policy

- **Restorable**: PID существует, identity совпадает, pipes owned → restore state `running`
- **Stale**: PID существует, но identity mismatch или pipes lost → state `stale`
- **Orphaned**: PID reused another process → state `orphaned`
- **Terminal states**: persist reliably via `RemoveTerminal()`

При запуске Supervisor:
1. Загружает все `LaunchInstanceEntry` из repository
2. Проверяет, какие экземпляры были running
3. Проверяет, жив ли PID
4. Если жив: обновляет state на `running`, подписывает на logs
5. Если мёртв: маркирует как `exited` с `recovered` exit class

### Хранение данных

**Единое JSON-хранилище** (`goal_repo.json`) — single-file storage для runtimes, моделей, профилей и экземпляров.

Schema version: `4`. Atomic writes через `tmp + rename` + backup recovery.

```
goal_repo.json           — active repository
goal_repo.json.tmp       — temporary write file
goal_repo.json.bak       — backup of last known good state
```

**Atomic write sequence:**
1. Сериализация во временный файл в том же каталоге
2. `fsync` временного файла (через `File.Sync()`)
3. Сохранение предыдущего корректного файла как `.bak`
4. `os.Rename` временного файла (atomic на Windows и Linux)
5. Sync родительского каталога

**Ограничения (текущие):**
- fsync не гарантируется на всех платформах (OS handles flushing)
- Corrupted файл восстанавливается из `.bak` автоматически
- Concurrent write protection — mutex на уровне repository
- Schema version validation + migration tests

**Планируемые улучшения:**
- fsync после rename на всех платформах
- Рассмотреть SQLite для v1.0 (всё ещё single-binary)

### Логирование

Логи процессов хранятся per-instance через ring buffer `process.LogStore` (до 10000 записей на экземпляр). Доступ через:
- SSE стриминг в реальном времени (`GET /api/v1/logs/stream`) — multi-instance LogBroker
- Фильтрация по stream, search, time range, instance_id
- Пагинация (page/page_size) — агрегируется после объединения логов всех instances
- `GET /api/v1/logs` — QueryLogs с instance_id filter
- `GET /api/v1/instances/{id}/logs` — Логи конкретного instance

LogBroker (`process.LogBroker`) обеспечивает:
- Подписку на логи всех запущенных экземпляров
- Фильтрацию по instance_id
- Безопасную идемпотентную отмену подписки
- Drop oldest политику для медленных подписчиков
- Корректное завершение при shutdown

Legacy `/api/v1/status` удалён. Переход на `GET /api/v1/instances` и `GET /api/v1/instances/{id}`.

### Health Checks

Периодическая проверка здоровья рантаймов (каждые 30 секунд). Поддерживаются TCP и HTTP health check. Определения строятся на основе Profile host/port полей.

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

## Известные ограничения v0.9

- Hot-reload: не подключён в main startup, WIP
- SQLite storage: не реализован (только JSON)
- ARM64: не протестирован
- Auto-update: только через GitHub Releases
- Full reattach к произвольному PID: не реализован (только stale detection)
- Audit logging: WIP (доступны только metrics)
- Login rate limit: placeholder (не полностью реализован)
- Logs API: `/api/v1/logs`, `/api/v1/logs/stream` — заглушки (не реализованы)

## Перед началом разработки

Ознакомьтесь с `AGENTS.md`, `BACKLOG.md`, `ROADMAP.md` и `SUBAGENT_MASTER_PROMPT.md`.