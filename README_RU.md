# GoAl

[![XERJ Corpus Hub contributor](https://hub.xerj.org/contributors/dsdred/badge.svg)](https://hub.xerj.org/contributors/dsdred)

Запись в корпусе: [dsdred--goal-starter](https://hub.xerj.org/corpus/dsdred--goal-starter)

GoAl — лёгкий однофайловый менеджер для локальных AI-рантаймов и моделей. Один бинарник для Windows amd64 и Linux amd64.

**Архитектура:** Runtime ← Model → Instance. Runtime — исполнительный движок (executable, workingDir, environment). Model — настроенное определение запуска (runtime + args + environment). Instance — конкретный процесс (PID, state, logs). Физические GGUF/MMProj-файлы — обычные аргументы запуска, не отдельные сущности.

**Релизы:** [GitHub Releases](https://github.com/dsdred/goal-starter/releases)

## Ключевые возможности

- **CRUD рантаймов** — настройка Ollama, llama.cpp, vLLM или собственных inference-серверов
- **CRUD моделей** — настроенные определения запуска (runtime + args + environment)
- **Жизненный цикл экземпляров** — start, stop, restart; мультиинстансный суперайзер
- **Пайплайны** — упорядоченные группы моделей с групповым жизненным циклом и per-entry Args-переопределением; одна модель может входить в пайплайн несколько раз
- **Preview / Resolve** — посмотреть собранную команду перед запуском
- **Live-логи** — SSE-стриминг с фильтрацией по инстансам и пагинацией
- **Исторические логи** — поиск, фильтрация по времени и стримам
- **Встроенный Web UI** — дашборд с авторизацией и CSRF-защитой
- **Атомарное JSON-хранилище** — tmp + rename + backup recovery, без внешней БД
- **Консервативное восстановление** — обнаружение stale/orphan-инстансов при перезапуске

## Видео

- [GoAl первый запуск](https://www.youtube.com/watch?v=FtXztKYLU0Y)
- [Запуск Yue2-3B на локальной видеокарте](https://youtu.be/dwHAaJy7dPw)
- [Запуск в GoAl одной модели Yue2 на двух портах в два потока при помощи пайплайнов](https://www.youtube.com/watch?v=SnbJbUcMPm4)

## Проверенная ASR-конфигурация (по данным владельца)

- **Движок:** NeMo-Speech.cpp
- **Модель:** `NVIDIA-Nemotron-3.5-Transcribe-30B-A4B-AD-Q2_0_M.gguf`
- **Запуск:** через GoAl
- **Проверенные языки:** русский и английский
- **Наблюдения владельца:** распознавание речи работает успешно, субъективное качество распознавания очень хорошее, выполнение быстрое

Это практический тест, о котором сообщил владелец, а не стандартизированный бенчмарк. Данные об оборудовании, пропускной способности, задержках, real-time factor, метрики WER/CER и потреблении памяти не заявляются, совместимость не гарантируется.

## Быстрый старт

```powershell
.\scripts\bootstrap-windows.ps1
Copy-Item goal.example.json goal.json
$env:GOAL_CONFIG = (Resolve-Path .\goal.json)
go run .\cmd\goal
```

Затем откройте **http://127.0.0.1:8088** в браузере.

## Минимальная конфигурация

```json
{
  "version": 2,
  "listenAddress": "127.0.0.1",
  "webPort": 8088,
  "dataDir": "./data"
}
```

Рантаймы, модели или профили не обязательны. Всё настраивается через Web UI.

## Платформы

| Платформа | Архитектура | Статус |
|-----------|-------------|--------|
| Windows   | amd64       | Production |
| Linux     | amd64       | Production |
| Linux    | arm64       | Planned |

## Примечание о Windows SmartScreen

GoAl Windows-релизы на данный момент **не подписаны**. Publisher может отображаться как "Unknown Publisher", и Windows SmartScreen или Microsoft Defender могут показать предупреждение при первом запуске скачанного релиза. Это ожидаемое ограничение текущего способа распространения, а не ошибка GoAl. Если вы скачали релиз с официальной страницы [GitHub Releases](https://github.com/dsdred/goal-starter/releases) и проверили SHA-256 по `checksums.txt` из этого релиза, вы можете выбрать запуск приложения (например, через "Подробнее" → "Запуск в любом случае"). Code signing — возможное будущее улучшение.

## Безопасность

- HTTP-only session cookies, bcrypt-хеширование паролей
- CSRF-защита для всех unsafe методов
- Bind по умолчанию: `127.0.0.1`
- `authEnabled=false` на non-loopback адресе разрешён с prominent WARN (startup не блокируется)
- **Предупреждение публичного режима:** если авторизация отключена и GoAl привязан к `0.0.0.0`, все API-эндпоинты доступны без credentials.
> Подробнее: [SECURITY.md](docs/SECURITY.md)

## Известные ограничения

- Нет PID-reattach после перезапуска GoAl (инстансы помечаются как `stale` или `orphan`)
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
