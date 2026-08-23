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
8. [Runtime](#рантайны)
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

После запуска GoAl доступен по адресу: **http://127.0.0.1:8088**

> **Примечание:** Если порт 8088 занят, измените `webPort` в `goal.json`.

---

## Конфигурация

Файл `goal.json` лежит в той же директории, что и бинарник. Он **исключён из git** (содержит секреты и пользовательские пути).

> **Legacy-формат:** `goal.json` использует схему v5 для обратной совместимости. Записи `profiles` при старте становятся **Model** (определениями запуска) GoAl 2.0. Legacy-записи `models` (с `path`) складываются в launch args соответствующей модели. Новые модели, созданные через API или Web UI, используют упрощённый формат GoAl 2.0.

### Полная конфигурация

```json
{
  "version": 2,
  "listenAddress": "127.0.0.1",
  "webPort": 8088,
  "dataDir": "./data",
  "adminUser": "admin",
  "adminPasswordHash": "",
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
| `webPort` | Порт HTTP-сервера | `8088` | Нет |
| `dataDir` | Каталог для хранения данных | `./data` | Нет |
| `adminUser` | Имя администратора | `admin` | Нет |
| `adminPasswordHash` | Bcrypt-хеш пароля администратора (обязателен при `authEnabled=true`; обычно задаётся через настройки Web UI — plaintext не сохраняется) | `""` | Условное |
| `authEnabled` | Включить авторизацию | `false` | Нет |
| `runtimes` | Список AI-рантайнов | `[]` | Нет |
| `models` | Список моделей | `[]` | Нет |
| `profiles` | Список профилей запуска | `[]` | Нет |

### Настройка Runtime

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

**Поля Runtime:**

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
| `active` | Активен ли Runtime |

### Настройка модели (legacy-формат `goal.json`)

В `goal.json` модели используют legacy-формат v5. При старте `SeedFromConfig` складывает
`path` и `arguments` в launch args GoAl 2.0 Model, созданной из `profiles`.

**Пример: llama.cpp с GGUF-моделью:**

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

**Вариант B: через path (простой GGUF-файл, складывается в args при старте):**

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

При старте `path` превращается в `-m E:/models/llama3/model.gguf` в launch args модели.

**Поля модели (legacy `goal.json`):**

| Поле | Описание |
|------|----------|
| `id` | Уникальный идентификатор |
| `name` | Отображаемое имя |
| `runtimeId` | ID рантайна, где будет запущена |
| `path` | Путь к GGUF-файлу (складывается в args как `-m <путь>` при старте) |
| `arguments` | Массив аргументов командной строки (добавляются к model args) |
| `environment` | Переменные окружения процесса |

### Настройка профиля запуска (legacy-формат `goal.json`)

Записи `profiles` в `goal.json` при старте становятся **Model** GoAl 2.0.
Если `modelId` ссылается на legacy-модель, её `path` и `arguments` складываются
в launch args результирующей модели.

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

**Поля профиля (legacy `goal.json` — становится Model при старте):**

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

После запуска GoAl доступен по адресу: **http://127.0.0.1:8088**

### Возможности веб-интерфейса:

- **Панель управления** — обзор всех экземпляров и их статусов
- **Управление экземплярами** — запуск, остановка, перезапуск
- **CRUD рантайнов** — настройка AI-рантайнов
- **CRUD моделей** — настройка определений запуска (Runtime + аргументы + окружение)
- **История экземпляров** — персистентные записи завершённых экземпляров (переживают перезапуск)
- **Мониторинг здоровья** — проверка доступности рантайнов
- **Метрики** — встроенные метрики приложения
- **Тема** — System / Dark / Light (нижняя панель сайдбара, сохраняется в localStorage)
- **Язык** — Русский / English (нижняя панель сайдбара, сохраняется в localStorage)

### Тема и язык

Веб-интерфейс поддерживает две темы (тёмная, светлая) и режим System,
следующий за системной настройкой ОС. Интерфейс доступен на русском и
английском языках. Оба выбора сохраняются в браузере и восстанавливаются
при следующем визите.

Словари переводов находятся в:
- `internal/webui/static/i18n/ru.json`
- `internal/webui/static/i18n/en.json`

Для добавления нового языка создайте JSON-файл с тем же набором ключей
в директории `i18n/` и добавьте элемент `<option>` в `<select>` языка
в `index.html`.

### Авторизация

Если `authEnabled` включён:

1. Перейдите на `http://127.0.0.1:8088`
2. Нажмите **Login**
3. Введите `adminUser` и ваш пароль
4. После входа сессия хранится в HTTP-only cookie

---

## API

### Базовый URL

Все API вызовы начинаются с: `http://127.0.0.1:8088`

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
| GET | `/api/v1/history` | Завершённые экземпляры (персистентные, переживают перезапуск) |
| POST | `/api/v1/instances/{id}/stop` | Остановить |
| POST | `/api/v1/instances/{id}/restart` | Перезапустить |

### Модели

Модели — настроенные определения запуска, объединяющие Runtime с аргументами запуска
и окружением. Все параметры запуска (`--host`, `--port`, `-m` и др.) задаются
через аргументы.

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

### Runtime

| Метод | Путь | Описание |
|-------|------|----------|
| GET | `/api/v1/runtimes` | Список Runtime |
| GET | `/api/v1/runtimes/{id}` | Получить Runtime |
| POST | `/api/v1/runtimes` | Создать Runtime |
| PUT | `/api/v1/runtimes/{id}` | Обновить Runtime |
| DELETE | `/api/v1/runtimes/{id}` | Удалить Runtime |
| GET | `/api/v1/runtimes/health` | Здоровье всех Runtime |
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

- **Model** — настроенное определение запуска (ссылка на Runtime + аргументы + окружение)
- **Instance** — запущенный процесс (runtime entity)

Одна модель может создавать множество экземпляров. Остановка экземпляра не удаляет модель. Перезапуск переиспользует тот же экземпляр: старый процесс останавливается, новый процесс запускается под тем же ID экземпляра.

### Управление через CLI

```bash
# Список всех экземпляров
curl http://127.0.0.1:8088/api/v1/instances

# Статус конкретного экземпляра
curl http://127.0.0.1:8088/api/v1/instances/INSTANCE_ID

# Остановить экземпляр
curl -X POST http://127.0.0.1:8088/api/v1/instances/INSTANCE_ID/stop

# Перезапустить экземпляр
curl -X POST http://127.0.0.1:8088/api/v1/instances/INSTANCE_ID/restart
```

### Управление через веб-интерфейс

1. Откройте http://127.0.0.1:8088
2. Нажмите на нужный экземпляр
3. Используйте кнопки **Stop** / **Restart**

---

## Модели

**Модель** — настроенное определение запуска: ссылка на Runtime, аргументы запуска
и окружение. Все параметры запуска (`--host`, `--port`, `-m`, `--mmproj` и др.)
задаются через аргументы. Физические файлы моделей (GGUF, MMProj) не являются
отдельными сущностями — это обычные аргументы запуска.

### Создание модели

**Через веб-интерфейс:**

1. Перейдите в раздел **Мои модели**
2. Нажмите **+ Добавить модель**
3. Заполните поля:
   - Имя модели
   - Выберите Runtime
   - Укажите аргументы запуска (опционально)
   - Укажите переменные окружения (опционально)
4. Нажмите **Save**

**Через API:**

```bash
curl -X POST http://127.0.0.1:8088/api/v1/models \
  -H "Content-Type: application/json" \
  -d '{
    "id": "my-model",
    "name": "Моя модель",
    "runtime_id": "ollama",
    "active": true
  }'
```

### Пример: llama.cpp с Qwen GGUF

Типичная конфигурация модели llama.cpp:

```bash
curl -X POST http://127.0.0.1:8088/api/v1/models \
  -H "Content-Type: application/json" \
  -d '{
    "id": "qwen-35b",
    "name": "Qwen 3.6 35B",
    "runtime_id": "llama-cpp",
    "args": ["-m", "E:\\models\\qwen\\Qwen.gguf", "--mmproj", "E:\\models\\qwen\\mmproj.gguf", "-ngl", "99", "-c", "131072", "--host", "127.0.0.1", "--port", "8085"],
    "active": true
  }'
```

Резолюция команды:
`llama-server -m E:\models\qwen\Qwen.gguf --mmproj E:\models\qwen\mmproj.gguf -ngl 99 -c 131072 --host 127.0.0.1 --port 8085`

### Preview команды запуска

```bash
curl -X POST http://127.0.0.1:8088/api/v1/models/my-model/resolve \
  -H "Content-Type: application/json"
```

Возвращает полную команду, которая будет выполнена.

---

## Рантайны

### Создание Runtime

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
curl -X POST http://127.0.0.1:8088/api/v1/runtimes \
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
curl http://127.0.0.1:8088/api/v1/runtimes/health

# Здоровье конкретного Runtime
curl http://127.0.0.1:8088/api/v1/runtimes/health/ollama
```

---

## Логи

### Просмотр логов экземпляра

**Через API:**

```bash
# Логи конкретного экземпляра
curl http://127.0.0.1:8088/api/v1/instances/INSTANCE_ID/logs

# SSE поток логов
curl http://127.0.0.1:8088/api/v1/logs/stream
```

### Фильтрация логов

```bash
# С фильтром по instance_id
curl "http://127.0.0.1:8088/api/v1/logs?instance_id=INSTANCE_ID"
```

### Пагинация

```bash
# Страница 2, по 50 записей на странице
curl "http://127.0.0.1:8088/api/v1/logs?page=2&page_size=50"
```

---

## Безопасность

### Текущие настройки безопасности

| Параметр | Значение |
|----------|---------|
| Аутентификация | HTTP-only cookies, session-based |
| CSRF защита | Да, для всех unsafe методов |
| Rate limiting | **Не реализован** (известное ограничение) |
| Limit request body | http.MaxBytesReader |
| Bind по умолчанию | 127.0.0.1 (localhost) |

### Настройка для внешней сети

Чтобы сделать GoAl доступным по сети:

1. Откройте `goal.json`
2. Измените `listenAddress` на `"0.0.0.0"`
3. Включите авторизацию: `"authEnabled": true`
4. Перезапустите GoAl
5. Задайте пароль через **Settings → Server** в Web UI (сохраняется как `adminPasswordHash`)

```json
{
  "listenAddress": "0.0.0.0",
  "webPort": 8088,
  "authEnabled": true,
  "adminUser": "admin",
  "adminPasswordHash": "$2a$12$..."
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

## Миграция (v5 → v6)

При обновлении с GoAl v1.x файл `goal_repo.json` автоматически мигрируется при первом запуске:

| v5 (старое) | v6 (новое) |
|-------------|------------|
| Записи `profiles` | Становятся `models` (определения запуска) |
| Записи `models` (физические GGUF) | Складываются в launch args модели (например, `-m <путь>`) |
| `profile_id` в инстансах | Переименовывается в `model_id` |
| История инстансов | Сохраняется без изменений |

Резолюция команды запуска идентична до и после миграции. Действия пользователя не требуются.

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

Задайте новый пароль через **Settings → Server** в Web UI (вступает в силу немедленно, сохраняется как `adminPasswordHash`). Либо запишите валидный bcrypt-хеш в `adminPasswordHash` в `goal.json` и перезапустите GoAl.

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