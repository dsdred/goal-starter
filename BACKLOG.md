# GoAl Backlog

> **Historical engineering record.** This file tracks completed features and the v0.10 backlog. For the current product state, see [ROADMAP.md](../ROADMAP.md).

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

- [x] Supervisor управляет несколькими instances
- [x] LogBroker — multi-instance логирование с подпиской
- [x] SubscribeLogs — подписка с instance_id filter, безопасная отмена (idempotent cancel)
- [x] QueryAggregatedLogs — объединение логов, сортировка DESC, pagination один раз
- [x] InstanceController.Snapshot() — возврат копии (не mutable pointer)
- [x] maxConcurrent — атомарная CAS reservation
- [x] Restart без time.Sleep — использует done channel
- [x] Recovery — stale instance marking при запуске
- [x] shutdown persistence — terminal states persist
- [x] 16+ тестов для LogBroker/LogStore (race-safe)

**Evidence:** `internal/process/supervisor.go`, `internal/process/log_store.go`, `internal/process/log_store_test.go`

## ✅ P0 — Stabilization iteration (ЗАВЕРШЕНО, v0.9)

### Исправления конкурентности:
- [x] LogBroker: cancel idempotent, publish guard, shutdown-safe
- [x] QueryLogs: instance_id filter ПОСЛЕ агрегации, global pagination, deterministic sort
- [x] maxConcurrent: CAS reservation через atomic.Int64, race-free
- [x] Snapshot model: все публичные методы возвращают копии

### Persistence:
- [x] Нет игнорирования ошибок repository
- [x] JSON repository: atomic write, backup recovery, fsync

### Архитектура:
- [x] Все endpoints используют Supervisor
- [x] InstanceStore — узкий persistence-specific интерфейс
- [x] Application services связывают domain + Supervisor + repository

### Recovery:
- [x] Policy: restorable/stale/orphaned
- [x] Terminal states persist reliably

### Документация:
- [x] README.md и README_RU.md синхронизированы
- [x] Recovery policy documented
- [x] Known limitations v0.9 documented
- [x] Security defaults documented

**Evidence:**
- `internal/process/log_store.go` (cancel idempotent, publish guard)
- `internal/process/supervisor.go` (CAS reservation, QueryLogs aggregation)
- `docs/STABILIZATION_PLAN.md` (полный план и прогресс)

## ⏭ TODO — Next iteration (v0.10)

### P0 — Production readiness
- [ ] SQLite storage (с сохранением single-binary)
- [ ] Full reattach к произвольному PID
- [ ] Hot-reload wired into main startup
- [ ] Audit logging (полноценный, не только metrics)
- [ ] Login rate limit fully implemented
- [ ] fsync after rename на всех платформах
- [ ] Transactional backup перед каждой записью

### P1 — Reliability
- [ ] Comprehensive integration tests
- [ ] Chaos testing for Supervisor recovery
- [ ] Schema migration tests
- [ ] Concurrent write protection tests
- [ ] Windows/Linux-specific lifecycle tests

### P2 — Packaging
- [ ] .deb/.rpm packages через CI pipeline
- [ ] GPG signatures для всех артефактов
- [ ] ARM64 builds и tests
- [ ] Windows MSI installer (через WiX)
- [ ] Release automation через GitHub Actions

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
- [ ] Linux packages (.deb, .rpm) — **ОТКЛОНЕНО как завершённое (forensic 2026-08-27, вердикт REJECT)**
   - Запись ложно отмечена `[x]` в `48ccbfe` (unrelated test-fix commit); `cmd/goal/linux/packager.go` никогда не коммитился (untracked scratch, пусто в `git log --all`), не импортируется ни `cmd/goal`, ни `cmd/goal-msi`, не используется ни в одном build-скрипте или CI, тестов нет
   - Доказанные дефекты (код никогда не выполнялся): расхождение fmt-аргументов RPM-спека с шаблоном → `Name: <version>`, `Version: <arch>`, `BuildArch: <name>`, `systemctl disable <version>.service`; deb `Architecture: x86_64` при cross-сборке (Debian ожидает `amd64`); fpm-режим ссылается на postinst/prerm-скрипты, которые никогда не пишутся на диск
   - Направление остаётся открытым: ROADMAP «Later» (MSI/.deb/.rpm installers as demand matures); реализация — отдельная задача с тестами и CI-интеграцией
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
- [x] Hot-reload configuration (`internal/config/reload.go`, `ReloadConfig`) — superseded by [ADR 009](../docs/adr/009-hot-reload-wiring.md): the `ReloadConfig` type was removed (dead production code; its `Save()` violated the durable-write contract); hot-reload is now the explicit `POST /api/v1/admin/reload` endpoint
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