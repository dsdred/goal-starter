# Архитектура

GoAl — однофайловое приложение, составленное из отдельных слоёв с чётким разделением ответственности. Этот документ описывает production V1 архитектуру.

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
| Application | `internal/application/` | Бизнес-логика: ProfileService, InstanceService, RuntimeService, ModelService. |
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

**Последовательность атомарной записи:**
1. Сериализация во временный файл в той же директории
2. `fsync` через `File.Sync()`
3. Сохранение предыдущего корректного файла как `.bak`
4. `os.Rename()` (атомарно на Windows и Linux)
5. Sync родительской директории

**Legacy хранилища:** `internal/webui/store/` содержит файловые stores (profiles, runtimes, models), но не используется для production персистентности. Авторитарное хранилище — `internal/storage/JSONRepository`.

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
- Переменные окружения профиля сливаются с окружением родительского процесса (переменные профиля переопределяют системные).

## ADR сводка

| ADR | Тема | Статус |
|-----|------|--------|
| 0001 | Продукт и архитектура (Go, single binary, SSE логи) | Accepted |
| 0002 | Мультиинстансный Supervisor и Profile → Instance модель | Accepted |
| 0003 | Web UI через embedded FS | Proposed |
| 0004 | Config vs Repository (seed-once) | Proposed |
