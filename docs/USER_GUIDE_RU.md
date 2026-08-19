# GoAl — Руководство пользователя

GoAl — лёгкий кроссплатформенный менеджер для локальных AI-рантайнов и моделей. Один бинарник для Windows и Linux.

---

## Содержание

1. [Установка](#установка)
2. [Быстрый старт](#быстрый-старт)
3. [Конфигурация](#конфигурация)
4. [Веб-интерфейс](#веб-интерфейс)
5. [API](#api)
6. [Управление экземплярами](#управление-экземплярами)
7. [Модели](#модели)
8. [Рантаймы](#рантайны)
9. [Логи](#логи)
10. [Здоровье рантайнов](#здоровье-рантайнов)
11. [Безопасность](#безопасность)
12. [Установка как сервис (Linux systemd)](#установка-как-сервис-linux-systemd)
13. [Установка как сервис (Windows)](#установка-как-сервис-windows)
14. [FAQ](#faq)

---

## Установка

### Windows

**Вариант A — скачать готовый релиз:**

1. Скачайте архив `goal-VERSION-windows-amd64.zip` с GitHub Releases
2. Распакуйте в любую директорию (например `C:\goal-starter\`)
3. Перейдите в папку через PowerShell

> **SmartScreen:** Выпущенный бинарник не подписан. Windows может показать предупреждение SmartScreen ("Unknown Publisher") при первом запуске. Если вы скачали с официальной страницы [GitHub Releases](https://github.com/dsdred/goal-starter/releases) и проверили SHA-256 по `checksums.txt`, нажмите "Подробнее" → "Запуск в любом случае".

**Вариант B — собрать из исходников:**

```powershell
git clone https://github.com/dsdred/goal-starter.git
cd goal-starter
.\scripts\build-all.ps1
```

Бинарник появится в `bin\goal-windows-amd64.exe`.

### Linux

**Вариант A — скачать готовый релиз:**

1. Скачайте архив `goal-VERSION-linux-amd64.tar.gz` с GitHub Releases
2. Распакуйте: `tar -xzf goal-VERSION-linux-amd64.tar.gz`

**Вариант B — собрать из исходников:**

```bash
git clone https://github.com/dsdred/goal-starter.git
cd goal-starter
GOOS=linux GOARCH=amd64 go build -o bin/goal-linux-amd64 ./cmd/goal
```

---

## Быстрый старт

```powershell
# 1. Перейдите в папку с бинарником
cd C:\goal-starter   # Windows
cd /opt/goal         # Linux

# 2. Создайте конфигурационный файл
cp goal.example.json goal.json

# 3. Отредактируйте goal.json (см. ниже)

# 4. Запустите
.\goal.exe           # Windows
sudo ./goal          # Linux
```

После запуска GoAl доступен по адресу: **http://127.0.0.1:9090**

> **Примечание:** Если порт 9090 занят, измените `webPort` в `goal.json`.

---

## Конфигурация

Файл `goal.json` лежит в той же директории, что и бинарник. Он **исключён из git** (содержит секреты и пользовательские пути).

### Полная конфигурация

```json
{
  "version": 2,
  "listenAddress": "127.0.0.1",
  "webPort": 9090,
  "dataDir": "./data",
  "adminUser": "admin",
  "adminPassword": "",
  "authEnabled": false,
  "runtimes": [],
  "models": [],
  "profiles": []
}
```

### Поля конфигурации

| Поле | Описание | По умолчанию | Обязательно |
|------|----------|-------------|-------------|
| `version` | Версия схемы конфигурации | 2 | Нет |
| `listenAddress` | Адрес для HTTP-сервера | `127.0.0.1` | Нет |
| `webPort` | Порт HTTP-сервера | `9090` | Нет |
| `dataDir` | Каталог для хранения данных | `./data` | Нет |
| `adminUser` | Имя администратора | `admin` | Нет |
| `adminPassword` | Пароль администратора (пустой = без авторизации) | `""` | Нет |
| `authEnabled` | Включить авторизацию | `false` | Нет |
| `runtimes` | Список AI-рантайнов | `[]` | Нет |
| `models` | Список моделей | `[]` | Нет |
| `profiles` | Список профилей запуска | `[]` | Нет |

### Настройка рантайма

```json
{
  "runtimes": [
    {
      "id": "ollama",
      "name": "Ollama",
      "type": "ollama",
      "executable": "ollama",
      "workingDir": "C:\\Program Files\\Ollama",
      "args": ["serve"],
      "environment": {},
      "healthCheck": {
        "type": "http",
        "url": "http://127.0.0.1:11434"
      },
      "active": true
    }
  ]
}
```

**Поля рантайма:**

| Поле | Описание |
|------|----------|
| `id` | Уникальный идентификатор (латиница, цифры, дефис) |
| `name` | Отображаемое имя |
| `type` | Тип рантайна: `ollama`, `vllm`, `llama.cpp`, `custom` |
| `executable` | Путь к исполняемому файлу |
| `workingDir` | Рабочая директория процесса |
| `args` | Аргументы командной строки |
| `environment` | Переменные окружения процесса |
| `healthCheck` | Настройка проверки здоровья |
| `active` | Активен ли рантайм |

### Настройка модели

Модели можно настроить двумя способами: через `arguments` (inline-аргументы для сервера) или через `path` (прямой путь к GGUF-файлу).

**Вариант A: через arguments (для llama.cpp server и подобных):**

```json
{
  "models": [
    {
      "id": "qwen35b",
      "name": "Qwen 3.6 35B",
      "runtimeId": "llama-cpp",
      "arguments": [
        "-m", "E:/models/qwen/model.gguf",
        "--mmproj", "E:/models/qwen/mmproj.gguf",
        "--jinja",
        "-c", "200000",
        "--port", "8085",
        "--host", "0.0.0.0"
      ],
      "environment": {}
    }
  ]
}
```

**Вариант B: через path (простой GGUF-файл):**

```json
{
  "models": [
    {
      "id": "llama3",
      "name": "Llama 3",
      "runtimeId": "ollama",
      "path": "E:/models/llama3/model.gguf"
    }
  ]
}
```

**Поля модели:**

| Поле | Описание |
|------|----------|
| `id` | Уникальный идентификатор |
| `name` | Отображаемое имя |
| `runtimeId` | ID рантайна, где будет запущена |
| `path` | Путь к GGUF-файлу (альтернатива arguments) |
| `arguments` | Массив аргументов командной строки (альтернатива path) |
| `environment` | Переменные окружения процесса |

### Настройка профиля запуска

```json
{
  "profiles": [
    {
      "id": "chat-profile",
      "name": "Чат с Llama 3",
      "runtimeId": "ollama",
      "modelId": "llama3",
      "args": ["--port", "8080"],
      "environment": {"OMP_NUM_THREADS": "4"},
      "active": true
    }
  ]
}
```

**Поля профиля:**

| Поле | Описание |
|------|----------|
| `id` | Уникальный идентификатор |
| `name` | Отображаемое имя |
| `runtimeId` | ID рантайна |
| `modelId` | ID модели (опционально) |
| `args` | Дополнительные аргументы командной строки |
| `environment` | Переменные окружения процесса |
| `active` | Активен ли профиль |

### Горячая перезагрузка конфигурации

- `logLevel` — можно изменить без перезапуска
- `healthCheck.interval` — можно изменить без перезапуска
- `listenAddress`, `webPort`, `dataDir` — **требуют перезапуска**

---

## Веб-интерфейс

После запуска GoAl доступен по адресу: **http://127.0.0.1:9090**

### Возможности веб-интерфейса:

- **Панель управления** — обзор всех экземпляров и их статусов
- **Управление экземплярами** — запуск, остановка, перезапуск
- **CRUD рантайнов** — настройка AI-рантайнов
- **CRUD моделей** — настройка определений запуска (рантайм + аргументы + окружение)
- **Мониторинг здоровья** — проверка доступности рантайнов
- **Метрики** — встроенные метрики приложения

### Авторизация

Если `authEnabled` включён:

1. Перейдите на `http://127.0.0.1:9090`
2. Нажмите **Login**
3. Введите `adminUser` и `adminPassword` из конфигурации
4. После входа сессия хранится в HTTP-only cookie

---

## API

### Базовый URL

Все API вызовы начинаются с: `http://127.0.0.1:9090`

### Аутентификация

| Метод | Путь | Описание |
|-------|------|----------|
| POST | `/api/v1/auth/login` | Войти (HTTP-only cookies) |
| POST | `/api/v1/auth/logout` | Выйти |
| GET | `/api/v1/auth/session` | Проверить сессию |

### Управление экземплярами

| Метод | Путь | Описание |
|-------|------|----------|
| GET | `/api/v1/instances` | Список всех экземпляров |
| GET | `/api/v1/instances/{id}` | Статус экземпляра |
| POST | `/api/v1/instances/{id}/stop` | Остановить |
| POST | `/api/v1/instances/{id}/restart` | Перезапустить |

### Модели

Модели — настроенные определения запуска, объединяющие рантайм с аргументами запуска,
host, портом и окружением.

| Метод | Путь | Описание |
|-------|------|----------|
| GET | `/api/v1/models` | Список моделей |
| GET | `/api/v1/models/{id}` | Получить модель |
| POST | `/api/v1/models` | Создать модель |
| PUT | `/api/v1/models/{id}` | Обновить модель |
| DELETE | `/api/v1/models/{id}` | Удалить модель |
| POST | `/api/v1/models/{id}/start` | Запустить экземпляр |
| POST | `/api/v1/models/{id}/stop` | Остановить активные экземпляры |
| POST | `/api/v1/models/{id}/restart` | Перезапустить |
| GET | `/api/v1/models/{id}/status` | Статус экземпляров |
| POST | `/api/v1/models/{id}/activate` | Включить автозапуск |
| POST | `/api/v1/models/{id}/deactivate` | Выключить автозапуск |
| POST | `/api/v1/models/{id}/resolve` | Предпросмотр команды запуска |

Значения переменных окружения модели write-only. API и веб-интерфейс показывают
только их имена. Редактирование других полей модели сохраняет сохранённое окружение,
если поле `environment` опущено. Для изменения окружения отправьте явную карту
значений, или `{}` для удаления всех переменных окружения модели.

### Рантаймы

| Метод | Путь | Описание |
|-------|------|----------|
| GET | `/api/v1/runtimes` | Список рантаймов |
| GET | `/api/v1/runtimes/{id}` | Получить рантайм |
| POST | `/api/v1/runtimes` | Создать рантайм |
| PUT | `/api/v1/runtimes/{id}` | Обновить рантайм |
| DELETE | `/api/v1/runtimes/{id}` | Удалить рантайм |
| GET | `/api/v1/runtimes/health` | Здоровье всех рантаймов |
| GET | `/api/v1/runtimes/health/{id}` | Здоровье конкретного |

### Health Check и версия

| Метод | Путь | Описание |
|-------|------|----------|
| GET | `/api/v1/health` | Health check |
| GET | `/api/v1/version` | Версия приложения |
| GET | `/api/v1/metrics` | Метрики приложения |

> Миграция запускается автоматически при старте — отдельного эндпоинта статуса нет.

---

## Управление экземплярами

### Что такое Instance?

- **Model** — настроенное определение запуска (ссылка на рантайм + аргументы + окружение)
- **Instance** — запущенный процесс (runtime entity)

Одна модель может создавать множество экземпляров. Остановка экземпляра не удаляет модель. Перезапуск создаёт новый экземпляр.

### Управление через CLI

```bash
# Список всех экземпляров
curl http://127.0.0.1:9090/api/v1/instances

# Статус конкретного экземпляра
curl http://127.0.0.1:9090/api/v1/instances/INSTANCE_ID

# Остановить экземпляр
curl -X POST http://127.0.0.1:9090/api/v1/instances/INSTANCE_ID/stop

# Перезапустить экземпляр
curl -X POST http://127.0.0.1:9090/api/v1/instances/INSTANCE_ID/restart
```

### Управление через веб-интерфейс

1. Откройте http://127.0.0.1:9090
2. Нажмите на нужный экземпляр
3. Используйте кнопки **Stop** / **Restart**

---

## Модели

**Модель** — настроенное определение запуска: ссылка на рантайм, аргументы запуска,
host/порт и окружение. Физические файлы моделей (GGUF, MMProj) не являются отдельными
сущностями — это обычные аргументы запуска (например, `-m <путь>`, `--mmproj <путь>`).

### Создание модели

**Через веб-интерфейс:**

1. Перейдите в раздел **Мои модели**
2. Нажмите **+ Добавить модель**
3. Заполните поля:
   - Имя модели
   - Выберите рантайм
   - Укажите аргументы запуска (опционально)
   - Укажите переменные окружения (опционально)
4. Нажмите **Save**

**Через API:**

```bash
curl -X POST http://127.0.0.1:9090/api/v1/models \
  -H "Content-Type: application/json" \
  -d '{
    "id": "my-model",
    "name": "Моя модель",
    "runtimeId": "ollama",
    "active": true
  }'
```

### Preview команды запуска

```bash
curl -X POST http://127.0.0.1:9090/api/v1/models/my-model/resolve \
  -H "Content-Type: application/json"
```

Возвращает полную команду, которая будет выполнена.

---

## Рантайны

### Создание рантайма

**Через веб-интерфейс:**

1. Перейдите в раздел **Runtimes**
2. Нажмите **Create Runtime**
3. Заполните поля:
   - Имя рантайна
   - Тип: `ollama`, `vllm`, `llama.cpp`, `custom`
   - Путь к исполняемому файлу
   - Рабочая директория
   - Аргументы командной строки
   - Health Check (HTTP или TCP)
4. Нажмите **Save**

**Через API:**

```bash
curl -X POST http://127.0.0.1:9090/api/v1/runtimes \
  -H "Content-Type: application/json" \
  -d '{
    "id": "my-ollama",
    "name": "Ollama",
    "type": "ollama",
    "executable": "C:\\Program Files\\Ollama\\ollama.exe",
    "args": ["serve"],
    "healthCheck": {
      "type": "http",
      "url": "http://127.0.0.1:11434"
    }
  }'
```

### Health Check

GoAl автоматически проверяет здоровье рантайнов каждые 30 секунд. Поддерживаются два типа:

**HTTP Health Check:**

```json
{
  "type": "http",
  "url": "http://127.0.0.1:11434"
}
```

**TCP Health Check:**

```json
{
  "type": "tcp",
  "address": "127.0.0.1:11434"
}
```

### Проверка здоровья

```bash
# Здоровье всех рантайнов
curl http://127.0.0.1:9090/api/v1/runtimes/health

# Здоровье конкретного рантайма
curl http://127.0.0.1:9090/api/v1/runtimes/health/ollama
```

---

## Логи

### Просмотр логов экземпляра

**Через API:**

```bash
# Логи конкретного экземпляра
curl http://127.0.0.1:9090/api/v1/instances/INSTANCE_ID/logs

# SSE поток логов
curl http://127.0.0.1:9090/api/v1/logs/stream
```

### Фильтрация логов

```bash
# С фильтром по instance_id
curl "http://127.0.0.1:9090/api/v1/logs?instance_id=INSTANCE_ID"
```

### Пагинация

```bash
# Страница 2, по 50 записей на странице
curl "http://127.0.0.1:9090/api/v1/logs?page=2&page_size=50"
```

---

## Безопасность

### Текущие настройки безопасности

| Параметр | Значение |
|----------|---------|
| Аутентификация | HTTP-only cookies, session-based |
| CSRF защита | Да, для всех unsafe методов |
| Rate limiting | 100 запросов/мин на IP |
| Login rate limit | 5 попыток / 5 минут |
| Limit request body | http.MaxBytesReader |
| Bind по умолчанию | 127.0.0.1 (localhost) |

### Настройка для внешней сети

Чтобы сделать GoAl доступным по сети:

1. Откройте `goal.json`
2. Измените `listenAddress` на `"0.0.0.0"`
3. Включите авторизацию: `"authEnabled": true`
4. Задайте пароль: `"adminPassword": "your_password"`
5. Перезапустите GoAl

```json
{
  "listenAddress": "0.0.0.0",
  "webPort": 9090,
  "authEnabled": true,
  "adminPassword": "secure_password_here"
}
```

---

## Установка как сервис (Linux systemd)

### Ручная установка

```bash
# 1. Скопируйте бинарник
sudo cp goal /opt/goal/goal

# 2. Создайте конфигурацию
sudo mkdir -p /etc/goal
sudo cp goal.example.json /etc/goal/goal.json
sudo nano /etc/goal/goal.json  # отредактируйте

# 3. Установите systemd сервис
sudo cp deploy/systemd/goal.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable goal
sudo systemctl start goal

# 4. Проверьте статус
sudo systemctl status goal
```

### Логи сервиса

```bash
sudo journalctl -u goal -f
```

### Обновление сервиса

```bash
sudo systemctl stop goal
# замените бинарник
sudo cp goal /opt/goal/goal
sudo systemctl start goal
```

---

## Установка как сервис (Windows)

### Через PowerShell

```powershell
# 1. Скопируйте бинарник в C:\goal-starter
# 2. Создайте goal.json
Copy-Item goal.example.json C:\goal-starter\goal.json
notepad C:\goal-starter\goal.json

# 3. Установите как Windows Service
cd C:\goal-starter
.\deploy\windows\install-service.ps1

# 4. Проверьте сервис
Get-Service goal
```

### Удаление сервиса

```powershell
.\deploy\windows\uninstall-service.ps1
```

---

## FAQ

### Где хранятся данные?

Данные хранятся в `dataDir` из конфигурации (по умолчанию `./data`). Файл репозитория: `goal_repo.json`.

### Как сменить порт?

Измените `webPort` в `goal.json` и перезапустите GoAl.

### Как сменить адрес?

Измените `listenAddress` в `goal.json` и перезапустите GoAl.

### Что означает статус "stale"?

Процесс был перезапущен ОС, но GoAl не может восстановить состояние. Нужно перезапустить вручную.

### Как сбросить пароль администратора?

1. Откройте `goal.json`
2. Измените `"adminPassword": "new_password"`
3. Перезапустите GoAl

### Где логи самого GoAl?

Логи выводятся в stdout/stderr. Для systemd: `journalctl -u goal`. Для Windows — Event Log.

### Как кросс-компилировать?

```powershell
# Windows -> Linux
$env:GOOS='linux'; $env:GOARCH='amd64'; go build -o goal-linux ./cmd/goal
```

### Как проверить целостность бинарника?

```bash
# Проверьте SHA256 из checksums.txt
Get-FileHash bin/goal-windows-amd64.exe -Algorithm SHA256
```

### Как обновить GoAl?

1. Скачайте новую версию
2. Остановите текущий процесс
3. Замените бинарник
4. Перезапустите

Конфигурация `goal.json` и данные в `dataDir` не меняются.

### Как запустить несколько экземпляров GoAl?

Каждый экземпляр должен иметь свой `goal.json` с разными `webPort` и `dataDir`:

```powershell
$env:GOAL_CONFIG = "C:\goal-instance-1\goal.json"
.\goal.exe

$env:GOAL_CONFIG = "C:\goal-instance-2\goal.json"
.\goal.exe