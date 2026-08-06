# GoAl Backlog

## ✅ P0 — Process reliability (ЗАВЕРШЕНО)

- [x] Process Manager с state machine (running, exited, starting, stopping)
- [x] Exit classification (success, failure, killed, signaled, timeout)
- [x] Merge custom environment с `os.Environ()`
- [x] Валидация executable и working directory
- [x] Один и только один `cmd.Wait()` вызов
- [x] Concurrent Start/Stop/Status race-safe
- [x] Fake runtime тесты (16 тестов, все прошли)
- [x] Windows Job Object с `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`
- [x] Linux process group (Setpgid) с SIGTERM→SIGKILL escalation

## ✅ P1 — Security (ЗАВЕРШЕНО)

- [x] Administrator authentication (session-based, HTTP-only cookies)
- [x] CSRF protection (token в cookies + header)
- [x] Session security и login rate limiting (100 req/min)
- [x] Session store с авто-cleanup
- [x] Password store с bcrypt

## ✅ P1 — Profiles and API (ЗАВЕРШЕНО)

- [x] Profile, RuntimeEntry, ModelEntry модели в store
- [x] JSON файловое хранение (profiles.json, runtimes.json, models.json)
- [x] Build `CommandSpec` из Runtime + Model + Profile
- [x] POST `/api/v1/profiles/{id}/start` — запуск процесса
- [x] POST `/api/v1/profiles/{id}/stop` — остановка
- [x] POST `/api/v1/profiles/{id}/restart` — перезапуск
- [x] GET `/api/v1/profiles/{id}/status` — статус процесса
- [x] CRUD endpoints для profiles, runtimes, models

## ✅ P2 — Distribution (ЗАВЕРШЕНО)

- [x] Windows Service support (deploy/windows/install.ps1, uninstall.ps1)
- [x] systemd service (deploy/systemd/goal.service)
- [x] Release archives и checksums (scripts/build-all.ps1)
- [x] Version metadata (internal/version package)
- [x] `--version` flag в CLI
- [x] Build time embedded в бинарник

## ✅ P1 — Frontend (ЗАВЕРШЕНО)

- [x] Интерактивный UI для создания профилей (modal с runtime/model selects)
- [x] Визуализация статуса процессов (dashboard cards с animation)
- [x] Лог viewer с фильтрацией (stream filter + search)
- [x] Форма логина (modal с session-based auth)
- [x] Modern dark theme CSS
- [x] Escape HTML для безопасности
- [x] SSE log stream с клиентской фильтрацией

## ✅ P0 — Supervisor: multi-instance lifecycle (ЗАВЕРШЕНО)

- [x] Supervisor управляет несколькими instances (process/supervisor.go)
- [x] LogBroker — multi-instance логирование с подпиской (process/log_broker.go)
- [x] SubscribeLogs — подписка с instance_id filter, безопасная отмена
- [x] QueryAggregatedLogs — объединение логов, сортировка DESC, pagination один раз
- [x] InstanceController.Snapshot() — возврат копии (не mutable pointer)
- [x] maxConcurrent — атомарная проверка и резервирование слота
- [x] Restart без time.Sleep — использует done channel для synchronization
- [x] Recovery — stale instance marking при запуске
- [x] shutdown persistence — terminal states persist
- [x] 18+ тестов для LogBroker, QueryAggregated, Supervisor concurrency

## ✅ P1 — API improvements (ЧАСТИЧНО ЗАВЕРШЕНО)

- [x] Version endpoint (GET /api/v1/version) — возвращает version, gitCommit, buildTime
- [x] Health check endpoint (GET /api/v1/health) — базовый health check
- [x] Request logging middleware (`internal/webui/middleware/logging.go`)
  - Запись method, path, status code, duration, client IP, user agent
  - statusWriter wrapper для захвата HTTP status code
  - Встроен в цепочку middleware: logging → rate limit → CSRF
- [x] Port и host валидация (`internal/webui/validation/`)
  - ValidatePort — проверка диапазона 1-65535
  - ValidateHost — проверка IP, hostname (RFC 1123)
  - ValidateAddress — комбинация host+port
  - ParsePort — парсинг строки порта
  - Интегрировано в CreateProfile/UpdateProfile

## ✅ P1 — Port validation (ЗАВЕРШЕНО)

- [x] `internal/webui/validation/port.go` — валидация портов и хостов
- [x] `internal/webui/validation/port_test.go` — unit-тесты
- [x] Интеграция в `internal/webui/store/profile_store.go`

## TODO — Next iteration

### P1 — API improvements

- [x] Health checks для runtimes (`internal/webui/health/`)
- [x] Runtime-level health check endpoint (`GET /api/v1/runtimes/health/{id}`)
- [x] Periodic health check polling (`startHealthChecker`, 30s interval)
- [x] Structured API errors с кодами (`internal/webui/errors/`)
- [x] Activate/deactivate profile endpoints
- [x] Log filtering and pagination (server-side) — `internal/process/log_store.go`, `GET /api/v1/logs/query`
- [x] WebSocket для log stream (`internal/webui/websocket/`)
- [x] Metrics endpoint (Prometheus format) — `internal/webui/metrics/`

### P2 — Packaging

- [x] Release archives для Windows (.zip) — `scripts/build-all.ps1`
  - Автоматическое создание ZIP архива с бинарником, конфигами, README и service скриптами
  - Включает install-service.ps1 и uninstall-service.ps1
  - RELEASE.txt с инструкциями по установке
- [x] Release archives для Linux (.tar.gz) — `scripts/build-all.ps1`
  - Автоматическое создание tar.gz архива с бинарником, конфигами, README и systemd service файлом
  - Структура: goal/, etc/goal/, deploy/
  - RELEASE.txt с инструкциями по установке и systemd
- [x] GPG signatures для checksums — встроенная поддержка в build-all.ps1
  - Проверяет наличие gpg в системе
  - Создаёт .sig файл для checksums.txt
  - Graceful fallback если gpg не установлен
- [x] Self-extracting installer (SFX) — cmd/goal/msi/sfx.go
  - Создает ZIP архив с бинарником, конфигами, service скриптами
  - Включает install.bat для автоматической распаковки
  - Не требует внешних зависимостей (WiX)
  - Работает на Windows и Linux
- [x] MSI/SFX fallback в build.go — автовыбор между MSI и SFX
- [x] Поддержка -sfx флага в goal-msi
- [x] Linux packages (.deb, .rpm) — cmd/goal/linux/packager.go
  - Поддержка dpkg-deb и fpm для .deb
  - Поддержка rpmbuild и fpm для .rpm
  - Автоматическое определение доступных инструментов
  - Systemd service integration
- [x] Auto-update mechanism — internal/updater/updater.go
  - Проверка обновлений через GitHub Releases API
  - Скачивание с verification checksum SHA256
  - Установка для Windows, Linux
  - Автоматический restart сервиса после обновления
  - Fallback на package manager для .deb/.rpm
- [ ] Windows installer (.msi) — требует WiX Toolset

### P2 — Configuration

- [x] Config validation при старте (`internal/config/validate.go`, `ValidateFull`)

### P1 — Testing improvements

- [x] Устранены failing тесты в `internal/webui/handlers/`
  - Рефакторинг `instanceStoreAdapter` → `mockInstanceStore` с configurable behavior
  - Исправлен `TestInstancesHandler_List_Error` — supervisor.List() возвращает из internal map, не из store
  - Исправлен `TestInstancesHandler_StartProfile` — учтено что fake-runtime может отсутствовать
  - Исправлен `TestInstanceStoreJSON_CreateDuplicate` — Create не проверяет уникальность (overwrite)
  - Все тесты handlers проходят (go test ./internal/webui/handlers/...), gofmt и go vet чисты
- [x] Hot-reload configuration (`internal/config/reload.go`, `ReloadConfig`)
- [x] Config migration from v1 to v2 (`internal/config/config.go`, `migrateV1ToV2`)
  - Добавлена миграция версии конфиг файла
  - Добавлены поля HealthCheck для Profile и Runtime
- [x] Profile-specific health check config (`ProfileHealthCheck`)
  - `Enabled`, `Interval`, `Timeout`, `HTTPPath`, `HTTPStatus`
  - Мигрируется автоматически при загрузке старого конфига
- [x] Runtime-specific health check config (`RuntimeHealthCheck`)
  - `Type`, `Enabled`, `Interval`, `Timeout`, `Host`, `Port`, `HTTPPath`

### P2 — Monitoring

- [x] Request logging middleware (`internal/webui/middleware/logging.go`)
- [x] Metrics endpoint (Prometheus format) — `internal/webui/metrics/`
- [x] Structured JSON logger — `internal/webui/logger/`
  - `JSONLogger` — логгер с JSON-форматом вывода
  - `Level` — уровни (DEBUG, INFO, WARN, ERROR, FATAL)
  - `Option` — WithPrefix, WithFields
  - `NewChild` — дочерние логгеры с наследованием prefix и fields
  - `HTTPMiddleware` — HTTP middleware для JSON логирования
  - 12 unit-тестов (все прошли)
- [x] Health check endpoint (runtime-level) — `GET /api/v1/runtimes/health/{id}`