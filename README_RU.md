# GoAl

GoAl — лёгкий однофайловый менеджер для локальных AI-рантаймов, моделей и профилей запуска. Один бинарник для Windows amd64 и Linux amd64.

**Последняя стабильная версия: v1.0.0** — [GitHub Releases](https://github.com/dsdred/goal-starter/releases/tag/v1.0.0)

## Ключевые возможности

- **CRUD рантаймов** — настройка Ollama, llama.cpp, vLLM или собственных inference-серверов
- **Управление моделями** — GGUF-файлы, аргументы командной строки, переменные окружения
- **Профили запуска** — комбинация Runtime + Model + аргументы в переиспользуемые шаблоны
- **Мультиинстансный суперайзер** — параллельный запуск нескольких процессов с ограничением конкурентности
- **Live-логи** — SSE-стриминг с фильтрацией по инстансам и пагинацией
- **Исторические логи** — поиск, фильтрация по времени и стримам
- **Preview / Resolve** — посмотреть собранную команду перед запуском
- **Встроенный Web UI** — дашборд с авторизацией и CSRF-защитой
- **Атомарное JSON-хранилище** — tmp + rename + backup recovery, без внешней БД
- **Консервативное восстановление** — обнаружение stale-инстансов при перезапуске

## Быстрый старт

```powershell
.\scripts\bootstrap-windows.ps1
Copy-Item goal.example.json goal.json
$env:GOAL_CONFIG = (Resolve-Path .\goal.json)
go run .\cmd\goal
```

Затем откройте **http://127.0.0.1:9090** в браузере.

## Минимальная конфигурация

```json
{
  "version": 2,
  "listenAddress": "127.0.0.1",
  "webPort": 9090,
  "dataDir": "./data"
}
```

Рантаймы, модели или профили не обязательны. Всё настраивается через Web UI.

## Платформы

| Платформа | Архитектура | Статус |
|-----------|-------------|--------|
| Windows   | amd64       | Production |
| Linux     | amd64       | Production |
| Linux     | arm64       | Planned |

## Безопасность

- HTTP-only session cookies, bcrypt-хеширование паролей
- CSRF-защита для всех unsafe методов
- Bind по умолчанию: `127.0.0.1`
- `authEnabled=false` отклоняется для non-loopback адресов
- **Предупреждение публичного режима:** если авторизация отключена и GoAl привязан к `0.0.0.0`, все API-эндпоинты доступны без credentials.
> Подробнее: [SECURITY.md](docs/SECURITY.md)

## Известные ограничения

- Нет PID-reattach после перезапуска GoAl (инстансы помечаются как `stale`)
- SSE — авторитарный транспорт live-логов; WebSocket реализован, но не подключён
- Результаты TCP HealthChecker хранятся внутри, не вынесены в отдельный API
> Полный список: [LIMITATIONS.md](docs/LIMITATIONS.md)

## Сборка из исходников

```powershell
.\scripts\build-all.ps1
# Результат: bin/goal-windows-amd64.exe, bin/goal-linux-amd64, bin/checksums.txt
```

```powershell
go test ./...
go test -race ./...   # требует CGO_ENABLED=1 и gcc
go vet ./...
```

## Документация

- [Руководство пользователя](docs/USER_GUIDE_RU.md) — скачивание, конфигурация, запуск, обзор Web UI
- [Справочник конфигурации](docs/CONFIGURATION.md) — все опции goal.json
- [API Reference](docs/API.md) — все production эндпоинты
- [Архитектура](docs/ARCHITECTURE_RU.md) — жизненный цикл процессов, хранилище, решения
- [Разработка](docs/DEVELOPMENT.md) — сборка, тестирование, релиз
