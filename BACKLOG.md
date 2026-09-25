# GoAl Backlog

> **Historical engineering record.** This file tracks completed features and the v0.10 backlog. For the current product state, see [ROADMAP.md](ROADMAP.md).

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
- [x] Metrics endpoint — `GET /api/v1/metrics` (JSON system state: instance counts + server settings, requireAuth; `handlers/system.go`). Примечание (forensic 2026-09-16): пометка "Prometheus format" была неверна — dead v0.8 Prometheus-прототип `internal/webui/metrics/` (wired только в v0.8 `6759dbe`, снят в v0.8.1 `a042c11`, 0 импортеров) удалён как dead code вместе с orphan `ServeMetrics` (server.go); Prometheus-compatible monitoring остаётся open в ROADMAP (требует отдельного design/ADR и Owner-решений по exposure policy и implementation approach)
- [ ] `DELETE /api/v1/models/{id}` на несуществующую модель возвращает 500 вместо 404 (bounded API-correctness defect, обнаружен во время ADR 007 entity-audit реализации, 2026-09-16; НЕ исправлен)
   - Доказательная цепочка: `JSONRepository.DeleteModel` (internal/storage/repository.go:965) возвращает plain `fmt.Errorf("model not found: %s", id)` — не `APIError` — поэтому в `ModelsHandler.Delete` (internal/webui/handlers/models.go:214-228) `errors.As` не срабатывает, mapping `CodeNotFound → 404` (models.go:218-223) для этого пути мёртвый, и handler падает в `writeError(w, 500, err.Error())`. Sibling-эндпоинты намеренно возвращают 404 на отсутствующую модель: `Activate`/`Deactivate` (models.go:309/329), `RuntimesHandler.Delete` (runtimes.go:257-258)
   - ADR 007 тест (audit_entity_test.go:151-154) сознательно утверждает только "rejection" (not 404) — тест на 404 не ждать, пока handler не вернёт 404
   - Будущая задача: вернуть `APIError` `CodeNotFound` из repository/service + регрессионный тест, ожидающий 404; schema API не меняется, только behavior hardening

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
- [x] Hot-reload configuration (`internal/config/reload.go`, `ReloadConfig`) — superseded by [ADR 009](docs/adr/009-hot-reload-wiring.md): the `ReloadConfig` type was removed (dead production code; its `Save()` violated the durable-write contract); hot-reload is now the explicit `POST /api/v1/admin/reload` endpoint
- [x] Config migration from v1 to v2 (`internal/config/config.go`, `migrateV1ToV2`)
  - Добавлена миграция версии конфиг файла
  - Добавлены поля HealthCheck для Profile и Runtime
- [x] Profile-specific health check config (`ProfileHealthCheck`)
  - `Enabled`, `Interval`, `Timeout`, `HTTPPath`, `HTTPStatus`
  - Мигрируется автоматически при загрузке старого конфига
- [x] Runtime-specific health check config (`RuntimeHealthCheck`)
  - `Type`, `Enabled`, `Interval`, `Timeout`, `Host`, `Port`, `HTTPPath`
- [ ] `internal/webui/handlers` -race suite структурно тяжёлый (test-performance debt; mitigation применён 2026-09-16, корневая тяжесть остаётся)
  - Evidence (CI `go test -race ./...`, step wall ≈ handlers package): run 129 (919e3a0, pre-ADR-007-ext) 574s PASS; run 130 (3b8519e) 443s PASS; run 131 (5782e47) >600s FAIL ×2 (default per-package `-timeout` 10m0s) — сьюта систематически на границе 600s-бюджета ещё до ADR 007 entity extension
  - Mitigation (2026-09-16): (1) dedup cost-12 bcrypt generates в audit harness — 33 full-stack env × 2 generates (~230ms each) заменены одним precomputed хэшем + `SetHash` (isolation и login-verify сохранены); handlers non-race 63s → 46s локально; (2) CI race step: явный `-timeout 20m` (measured 443-600s+ observations + >2× headroom)
  - Остаток: 33 full-stack env + repo JSON save на мутацию + audit fsync на событие — если сьюта продолжит расти или раннеры будут медленнее, требуется структурное решение (env-reuse/parallelization) и повторное обоснование 20m-бюджета
- [ ] Browser suite `core.cjs` check 24.1 flake в restart-окне (test-harness hardening debt; НЕ production defect; НЕ исправлен)
  - Симптом (наблюдено один раз в CI): `24.1 Auth phase: no unexpected console errors` — 3 × `net::ERR_CONNECTION_REFUSED`. Evidence: ADR 007 SHA `3b8519e`, CI run `35055731936` (first pass FAIL; same-SHA rerun 7/7 PASS); локальные попытки воспроизведения 3/3 PASS (57/57); repository-content correction не требовалась
  - Механизм: Phase B `server.stop()` (core.cjs:346) выполняется, пока auth-OFF страница (page) открыта; `watchPage` (harness.cjs:166-174) без unwatch пишет console-ошибки обеих страниц (page L62 + page2 L359) в общий `suite.consoleErrors`; запросы в окне stop→server2-accept дают connection-refused, а whitelist 24.1 (core.cjs:387-388) фильтрует только 401-уведомления
  - Будущая задача: forensic/design детерминированной синхронизации. Направления расследования (не принятые fix): остановить/unwatch polling auth-OFF страницы до `server.stop()`; явная синхронизация restart-окна; узкая классификация ожидаемых refused/aborted ошибок только внутри намеренного restart-окна. Глобальный whitelist `ERR_CONNECTION_REFUSED` — НЕ допускается

### P2 — Monitoring

- [x] Request logging middleware (`internal/webui/middleware/logging.go`)
- [x] Metrics endpoint — `GET /api/v1/metrics` (JSON system state, requireAuth). (2026-09-16: dead Prometheus-прототип `internal/webui/metrics/` удалён как dead code — детали в P1 API-пункте; Prometheus monitoring open в ROADMAP)
- [x] Structured JSON logger — `internal/webui/logger/`
  - `JSONLogger` — логгер с JSON-форматом вывода
  - `Level` — уровни (DEBUG, INFO, WARN, ERROR, FATAL)
  - `Option` — WithPrefix, WithFields
  - `NewChild` — дочерние логгеры с наследованием prefix и fields
  - `HTTPMiddleware` — HTTP middleware для JSON логирования
  - 12 unit-тестов (все прошли)
- [x] Health check endpoint (runtime-level) — `GET /api/v1/runtimes/health/{id}`

## ⚠️ Current tracked debt — ADR 017 lifecycle remediation (recorded by D4, 2026-09-24)

> Tracked here, deliberately **not solved** by D4. Slice evidence, BF/RB statuses and the "program not complete" statement live in [ROADMAP.md](ROADMAP.md) (P0 → ADR 017 corrective slices + remediation register). Everything below is genuinely remaining debt or an open decision — not a duplicate of a resolved finding, and not fixed by writing it down.
>
> **On the `F-*` numbering (recorded by D6.1):** there is **no F-3** in this repository and none was ever removed — `git log --all -S"F-3"` finds no such token in any tracked file or commit message on any ref, and F-1 / F-2 / F-4 were all introduced together by D4. The series therefore originates from an external review register that the repository cannot reconstruct; the gap is not a lost finding, and **no F-3 defect is being asserted or invented here.**

- [ ] **Terminal-controller metadata retention (BF-07 aspect — hygiene-with-cost).** Once a process terminates, its `InstanceController` stays registered in the Supervisor — together with its preallocated `LogStore` (`internal/process/log_store.go`, capacity from `NewLogStore`) — until explicit cleanup (`POST /api/v1/instances/cleanup`) or the GoAl process ends. No automatic TTL / LRU / cap / background eviction exists. This is **metadata retention, not** an established process/goroutine/slot leak: the concurrency slot is released during terminal finalization. Any future bounded-retention design needs its own task and an eviction rule that can never select a controller whose record still backs a documented restart capability.
- [ ] **Durable historical `InstanceID` restart (open product/architecture decision — not a defect).** The current contract is **process-scoped** (documented in `docs/API.md`): restart of a terminal `InstanceID` works while its controller remains registered in the running GoAl process and its history record has not been explicitly cleaned; it is not guaranteed across a GoAl process restart, and cleanup removes the capability. Cross-restart durability would require reconstructing restart-capable controllers from history records — a separate product/architecture decision behind an ADR, not a correction.
- [ ] **F-1 — whole-set cleanup reconciliation needs re-review if a second production record-deletion path appears.** `Supervisor.ForgetCleanedControllers` (`internal/process/supervisor.go`) reconciles the registry against the repository's live record set. That is safe under the invariant recorded in ADR 017 §"Cleanup and Registry Reconciliation Invariant": `JSONRepository.DeleteTerminalInstances` (called only from `InstanceService.DeleteTerminalInstances`) is currently the only production path that removes a `LaunchInstance` record while the Supervisor is alive — the per-ID deleters `DeleteInstance` / `Delete` / `DeleteLaunchInstance` have zero production callers. Introducing another deletion path (per-instance delete, cascade, import overwrite, a retention job) invalidates the "a safe terminal controller without a durable record is attributable to explicit cleanup" inference and requires re-reviewing the whole-set design.
- [ ] **F-2 — missing deterministic retry regression (the implementation is already convergent).** `InstanceService.CleanupInstances` runs `Supervisor.ForgetCleanedControllers` **after a successful `DeleteTerminalInstances`, regardless of the delete count**, and the reconciliation derives its candidates from "registered controller whose id is absent from `store.List()`" rather than from that count — so a retry whose deletion removed zero rows still converges. **There is no `deleted == 0` short-circuit** (the earlier wording of this entry claimed one, and it was wrong). What is missing is a single deterministic regression pin for the chain: delete succeeds → reconciliation `List` fails → the request reports a reconciliation failure → retry deletes zero → reconciliation runs again → the safe terminal controller is removed. Each half is pinned separately today (`TestD3Cleanup_StoreReadFailureKeepsRegistry`, `TestD3Cleanup_RepositoryFailureKeepsRecordAndController`, and the `deleted == 0` repeat assertion in `TestD3Cleanup_SuccessRemovesRecordAndController`); only the cross-attempt chain is unpinned. Coverage debt, not a defect.
- [ ] **D5-02 chain-coverage debt — three invariants proven only by composition.** No single end-to-end test exists for: (a) `KillOrphan → durable stale → AdmitAndStart` on the same ModelID succeeds (`finishKill`'s `stale` write and the `stale`-does-not-block-fence predicate are each pinned, but not in one run); (b) `POST /api/v1/models/{id}/start → 409 orphan` through the real handler (the fence and the error mapping are pinned separately, and the mapping test drives the same `writeLifecycleError` the handler uses); (c) an orphan-specific model autostart skip (autostart's duplicate guard and failure-continues are pinned; `autostartModels` has no orphan-specific branch to test — the deny comes from `orphanFence`). See ADR 017 §"Concurrency / Regression Test Inventory" for the per-row mapping. Non-blocking coverage debt; must not block Manual Owner Acceptance.
- [ ] **F-4 — D3 forensic observations, recorded as observations and NOT confirmed defects.** (a) The starting-persist failure path removes the controller from the registry by `InstanceID` rather than by controller identity; (b) a pre-existing persistence write happens while the controller/instance lock is held. Current `InstanceID` values are process-unique (model ID + nanosecond timestamp + atomic sequence), so neither has demonstrated harm. Each needs its own targeted forensic before being classified as a defect.
- [ ] **D2 deferred A — `waitFileRewrite` observation window.** `internal/application/restart_reresolve_test.go` detects the durable rewrite by mtime on a 50 ms poll and does not fail on timeout, so a truncate/write inside the filesystem timestamp granularity can be missed. Test-harness hardening debt, not a production defect.
- [ ] **D2 deferred B — mandatory race evidence can be masked.** In `.github/workflows/ci.yml` the Linux job runs `go test ./...` **before** `CGO_ENABLED=1 go test -race …`, so an ordinary-test failure ends the job before the required race evidence is collected. CI-shape debt (surfaced while adjudicating the D2 same-SHA rerun).
- [ ] **D2 deferred C — `ErrInstanceNotFound` textual rendering changed by sentinel wrapping.** D2 made the not-found case carry the sentinel (`fmt.Errorf("%w: %s", ErrInstanceNotFound, id)`), which changed the rendered message text at the affected sites. No production consumer of the old rendering was found; residual risk only for consumers outside the repository that match on text. Wire status/`code`/`error` are unchanged.
- [ ] **BF-02d — latent `ListInstances` error swallowing in pipeline helper paths.** `PipelineService.hasActiveOwnedInstances` (`internal/application/pipeline_service.go`) returns `false` when the store read fails, so a structural pipeline edit or delete that is gated on "no active owned instances" can pass on a transient read error. Distinct from BF-02 (success attribution — RESOLVED by D2); deferred out of the D2 scope on purpose.
- [ ] **`Supervisor.RemoveTerminal` is callerless in production.** Reachable only from tests; the production terminal path is `wait()` finalization plus explicit cleanup's registry reconciliation. A future task should remove it or give it a real consumer — it must not be assumed to be the mechanism that evicts terminal controllers, because today it is not.
- [ ] **`docs/ARCHITECTURE.md` / `docs/ARCHITECTURE_RU.md` ADR summary tables stop at ADR 014.** The ADR 016 and ADR 017 rows promised by ADR 017's affected-documentation list are missing, and ADR 018 (Portable Configuration backup/restore semantics, 2026-09-26) adds a third absent row. Documentation debt only; no behavior implication.

## ⚠️ Portable Configuration — open debt pending ADR 018 (recorded 2026-09-26)

> Decisions, semantics and the five-slice plan live in [ADR 018](docs/adr/018-portable-configuration-restore-semantics.md) and in [ROADMAP.md](ROADMAP.md) (P1 → "Portable Configuration & Path Variables"); the ADR 018 implementation slices are deliberately **not** duplicated below. Everything here is genuinely remaining debt found while designing that ADR.

- [ ] **Import can persist a Pipeline with zero model entries (class C technical debt — derived from repository bytes, not yet reproduced at runtime).** Bundle validation inspects pipeline entries only when they exist (`internal/application/portable/portable.go:206-223`), the import planner never checks the entry-list length (`internal/storage/import_plan.go:188-218`), and `ImportGraph` appends the converted entry as-is (`internal/storage/repository.go:1139-1142`) — so a bundle pipeline with `"models": []` is written as a real pipeline, the one shape the interactive API refuses to create (`internal/application/pipeline_service.go:131`). A pipeline in that state then reaches the start/list/attribution paths without ever having passed the pipeline validator. Closes with ADR 018 Slice 1/2 as a BLOCKED classification (`pipeline_entries_empty`); no bundle-format change. Needs its own regression test when implemented.
