# Архитектура

GoAl — однофайловое приложение, составленное из отдельных слоёв с чётким разделением ответственности. Этот документ описывает текущую архитектуру GoAl 2.0.

## Доменная модель (v7)

GoAl 2.0 использует упрощённый домен из трёх сущностей:

- **Runtime** — конфигурация движка выполнения (исполняемый файл, рабочая директория, окружение)
- **Model** — настроенное определение запуска (ссылка на runtime, аргументы запуска, окружение, автозапуск)
- **Instance** — конкретная история запуска (неизменяемая запись о запуске процесса)

Все параметры запуска (`--host`, `--port`, `-m`, `--mmproj` и др.) задаются
через аргументы Model. Отдельных полей host/port на Model нет.

Физические файлы моделей (GGUF, MMProj) НЕ являются отдельными доменными сущностями. Это обычные
аргументы запуска (например, `-m <путь>`, `--mmproj <путь>`).

Связь: Runtime ← Model → Instance

## Точка композиции

`cmd/goal/main.go` собирает все компоненты:

```
main
├── config.Load()        → конфигурация
├── JSONRepository       → персистентность
├── Application services → бизнес-логика
├── Supervisor           → жизненный цикл процессов
├── HTTP handlers        → REST API
├── Embedded Web UI      → дашборд
└── Signal handler       → graceful shutdown
```

## Слои и ответственность

| Слой | Пакет | Ответственность |
|------|-------|-----------------|
| Конфигурация | `internal/config/` | Парсинг, валидация, миграция, сохранение `goal.json`. |
| Персистентность | `internal/storage/` | `JSONRepository` — однофайловое хранилище с атомарными записями. |
| Домен | `internal/domain/` | Типы, конвертация DTO между storage и application слоями. |
| Application | `internal/application/` | Бизнес-логика: ModelService, RuntimeService, InstanceService. |
| Жизненный цикл | `internal/process/` | Supervisor, Manager, LogBroker, LogStore, SlotReservation. |
| ОС | `internal/platform/` | Windows Job Object, Linux process groups. |
| HTTP / UI | `internal/webui/` | Роуты, хэндлеры, embedded шаблоны, статика, безопасность. |
| Безопасность | `internal/webui/security/` | Сессии, CSRF, password store (bcrypt). |
| Ошибки | `internal/webui/errors/` | Структурированные API коды ошибок и классификация. |
| Валидация | `internal/webui/validation/` | Валидация порта, хоста, адреса. |
| Метрики | `internal/webui/metrics/` | Метрики приложения в формате Prometheus. |
| Логгер | `internal/webui/logger/` | Структурированный JSON HTTP логгер. |
| Health | `internal/webui/health/` | TCP/HTTP health checker для рантаймов. |

## Поток данных

```
Пользователь (Web UI / API)
    │
    ▼
HTTP handlers (internal/webui/handlers/)
    │
    ▼
Application services (internal/application/)
    │
    ├──► JSONRepository (internal/storage/) — сохранение сущностей
    │
    └──► Supervisor (internal/process/) — управление процессами
           │
           ├──► Manager (на инстанс)
           │      └──► platform.Prepare() — OS-specific настройка
           │
           ├──► LogBroker — мультиинстансный лог-стриминг
           └──► LogStore — ring buffer на инстанс (10000 записей)
```

## Авторитарное хранилище

`goal_repo.json` — единственный источник истины для всех сущностей после первого запуска.

**Последовательность надёжной (durable) записи** — `fsutil.WriteFileDurable` (`internal/fsutil/`), используется для `goal_repo.json` (`JSONRepository.saveLocked` и `SaveUnified`) и `goal.json` (`config.Save`):
1. Сериализация во временный файл в той же директории
2. `fsync` временного файла (`File.Sync()`: `fsync(2)` на POSIX, `FlushFileBuffers` на Windows)
3. Проверка записанных байт повторным чтением временного файла
4. Сохранение предыдущего файла как `.bak` (та же durable-последовательность; перед каждой записью)
5. `os.Rename()` (атомарно на обеих платформах: `rename(2)` на POSIX, `MoveFileExW` с `MOVEFILE_REPLACE_EXISTING` на Windows)
6. POSIX: `fsync` родительской директории (обязательно; ошибка sync = ошибка записи)

**Платформенные гарантии:**
- **POSIX (Linux):** после шага 6 данные файла и rename (метаданные директории) закрепились на стабильном хранилище. Потеря питания в любой момент оставляет либо полное предыдущее, либо полное новое состояние.
- **Windows:** данные файла надёжны после шага 2; rename надёжен в момент успешного возврата `MoveFileExW`, если том — журналный NTFS (транзакция rename коммитится в журнал NTFS до возврата API). Поддерживаемая API-модель Windows не имеет directory flush; на томах без журналирования (FAT/exFAT, NTFS с отключённым журналом) гарантия надёжности rename не действует.

Каждый шаг возвращает ошибки: запись считается успешной только если она закреплена, а при ошибке на пути назначения не остаётся частично записанного файла.

**Согласованность в памяти:** изменяющие операции `JSONRepository` сначала изменяют состояние в памяти, затем персистят. При ошибке персистентности операция откатывает состояние до записи (rollback) и возвращает ошибку — память и диск не расходятся из-за ошибки сохранения.

Единый JSON-репозиторий в `internal/storage/` (`JSONRepository`) — единственный слой персистентности для всех сущностей.

## Журнал аудита безопасности

`internal/webui/audit/` реализует устойчивый журнал аудита безопасности (ADR 007): append-only JSON Lines файл `<dataDir>/goal_audit.jsonl` (mode `0600`). Каждое событие — одна `O_APPEND`-запись полной строки с последующим `fsync`; событие считается записанным только после возврата `fsync`. Ротация в `goal_audit.jsonl.1` выполняется перед дозаписью, пересекающей 10 MiB; хранится не более 3 поколений. Единый защищённый mutex `AuditLogger` обеспечивает совпадение порядка в файле с порядком возникновения событий.

Защита секретов обеспечена построением: логгер принимает только типизированный `AuditEvent`, создаваемый именованными точками вызова в хэндлерах (входы в систему, выход, сохранение настроек, действия жизненного цикла инстансов); общего пути «логировать этот запрос» нет. Семантика сбоев — fail-open: ошибка записи никогда не ломает и не откатывает бизнес-операцию, она порождает структурированный `slog.Error` (только имя события и исходная I/O ошибка, без данных события), и логгер не переходит в застывшее ошибочное состояние — каждое следующее событие независимо пытается новую запись. Файл — источник истины и читается при каждом запросе `GET /api/v1/admin/audit` (без кэша в памяти); отсутствующий файл (свежая установка) даёт пустой список, оборванная последняя строка пропускается.

`webui.App` владеет логгером (создаётся из `dataDir` в `NewApp`, закрывается при завершении в `main`); `RouteRegistry` внедряет его в auth/system/instance хэндлеры через `WithAuditLogger`.

См. [ADR 007](adr/007-audit-logging.md) и [SECURITY.md — Audit trail](SECURITY.md#audit-trail).

## Seed-on_once

`goal.json` импортируется в `goal_repo.json` при первом запуске через `storage.SeedFromConfig()`. Последующие запуски добавляют только новые сущности (по ID); существующие сущности в репозитории не перезаписываются.

См. [ADR 004](adr/004-config-vs-repository-ownership.md).

## Hot-reload конфигурации

Hot-reload реализован в `internal/config/reload.go` (`ReloadConfig`, `Watch`) но пока не подключён в main. Конфигурация загружается один раз при старте через `config.Load()`.

См. [ADR 004](adr/004-config-vs-repository-ownership.md) и [CONFIGURATION.md](CONFIGURATION.md#hot-reload).

## Веб-интерфейс

Web UI обслуживается из embedded filesystem (`templateFS`, `staticFS` объявлены в `internal/webui/server.go`). Дашборд рендерит `templates/index.html` из `templateFS`. Статические файлы обслуживаются из `staticFS` по `/static/`.

См. [ADR 003](adr/003-webui-embedded-fs.md).

## Направления зависимостей

```
handlers → application services → domain adapters → storage interfaces
                                                        ↓
                                                 JSONRepository
handlers → Supervisor → Manager → platform.Prepare()
handlers → LogBroker ← LogStore (per-instance)
config ← main (загружается один раз при старте)
```

## Правила владения процессами

- Каждый `exec.Cmd` имеет ровно одного goroutine-владельца, вызывающего `cmd.Wait()`.
- HTTP handlers не управляют `exec.Cmd` напрямую; они делегируют `Supervisor`.
- Аргументы процессов передаются как `[]string` — без shell-инвокации.
- Переменные окружения модели сливаются с окружением родительского процесса (переменные модели переопределяют системные).

## ADR сводка

| ADR | Тема | Статус |
|-----|------|--------|
| 0001 | Продукт и архитектура (Go, single binary, SSE логи) | Accepted |
| 0002 | Мультиинстансный Supervisor и Profile → Instance модель | Accepted |
| 0003 | Web UI через embedded FS | Proposed |
| 0004 | Config vs Repository (seed-once) | Proposed |
| 0005 | Recovery: identity-verified orphan detection и reconciliation | Accepted |
| 0006 | Безопасное хранение креденшелов (bcrypt `adminPasswordHash`) | Accepted |
| 0007 | Полное аудит-логирование (структурированные security-события, `goal_audit.jsonl`) | Accepted |
| 0008 | Recovery: завершение процесса-сироты (деструктивное завершение с повторной проверкой идентичности) | Accepted |
