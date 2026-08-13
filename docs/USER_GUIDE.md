# GoAl — User Guide

GoAl is a lightweight cross-platform manager for local AI runtimes, models, and launch profiles. One binary for Windows and Linux.

---

## Table of Contents

1. [Installation](#installation)
2. [Quick Start](#quick-start)
3. [Configuration](#configuration)
4. [Web Interface](#web-interface)
5. [API](#api)
6. [Instance Management](#instance-management)
7. [Launch Profiles](#launch-profiles)
8. [Runtimes](#runtimes)
9. [Models](#models)
10. [Logs](#logs)
11. [Runtime Health](#runtime-health)
12. [Security](#security)
13. [Install as Service (Linux systemd)](#install-as-service-linux-systemd)
14. [Install as Service (Windows)](#install-as-service-windows)
15. [FAQ](#faq)

---

## Installation

### Windows

**Option A — Download a released binary:**

1. Download `goal-VERSION-windows-amd64.zip` from GitHub Releases
2. Extract to any directory (e.g., `C:\goal-starter\`)
3. Open PowerShell and navigate to that folder

**Option B — Build from source:**

```powershell
git clone https://github.com/dsdred/goal-starter.git
cd goal-starter
.\scripts\build-all.ps1
```

The binary will appear at `bin\goal-windows-amd64.exe`.

### Linux

**Option A — Download a released binary:**

1. Download `goal-VERSION-linux-amd64.tar.gz` from GitHub Releases
2. Extract: `tar -xzf goal-VERSION-linux-amd64.tar.gz`

**Option B — Build from source:**

```bash
git clone https://github.com/dsdred/goal-starter.git
cd goal-starter
GOOS=linux GOARCH=amd64 go build -o bin/goal-linux-amd64 ./cmd/goal
```

---

## Quick Start

```powershell
# 1. Navigate to the binary directory
cd C:\goal-starter   # Windows
cd /opt/goal         # Linux

# 2. Create a configuration file
cp goal.example.json goal.json

# 3. Edit goal.json (see below)

# 4. Start GoAl
.\goal.exe           # Windows
sudo ./goal          # Linux
```

After starting, GoAl is available at: **http://127.0.0.1:9090**

> **Note:** If port 9090 is in use, change `webPort` in `goal.json`.

---

## Configuration

The `goal.json` file is located in the same directory as the binary. It is **excluded from git** (contains secrets and custom paths).

### Full Configuration

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

### Configuration Fields

| Field | Description | Default | Required |
|-------|-------------|---------|----------|
| `version` | Configuration schema version | 2 | No |
| `listenAddress` | HTTP server listen address | `127.0.0.1` | No |
| `webPort` | HTTP server port | `9090` | No |
| `dataDir` | Directory for storing data | `./data` | No |
| `adminUser` | Administrator username | `admin` | No |
| `adminPassword` | Administrator password (empty = no auth) | `""` | No |
| `authEnabled` | Enable authentication | `false` | No |
| `runtimes` | List of AI runtimes | `[]` | No |
| `models` | List of models | `[]` | No |
| `profiles` | List of launch profiles | `[]` | No |

### Runtime Configuration

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

**Runtime Fields:**

| Field | Description |
|-------|-------------|
| `id` | Unique identifier (lowercase letters, digits, hyphens) |
| `name` | Display name |
| `type` | Runtime type: `ollama`, `vllm`, `llama.cpp`, `custom` |
| `executable` | Path to the executable file |
| `workingDir` | Working directory for the process |
| `args` | Command-line arguments |
| `environment` | Process environment variables |
| `healthCheck` | Health check configuration |
| `active` | Whether the runtime is enabled |

### Model Configuration

Models can be configured in two ways: via `arguments` (inline args for the server) or via `path` (direct GGUF file path).

**Option A: via arguments (for llama.cpp server and similar):**

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

**Option B: via path (simple GGUF file):**

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

**Model Fields:**

| Field | Description |
|-------|-------------|
| `id` | Unique identifier |
| `name` | Display name |
| `runtimeId` | ID of the runtime where the model will run |
| `path` | Path to GGUF file (alternative to arguments) |
| `arguments` | Command-line arguments array (alternative to path) |
| `environment` | Process environment variables |

### Launch Profile Configuration

```json
{
  "profiles": [
    {
      "id": "chat-profile",
      "name": "Chat with Llama 3",
      "runtimeId": "ollama",
      "modelId": "llama3",
      "args": ["--port", "8080"],
      "environment": {"OMP_NUM_THREADS": "4"},
      "active": true
    }
  ]
}
```

**Profile Fields:**

| Field | Description |
|-------|-------------|
| `id` | Unique identifier |
| `name` | Display name |
| `runtimeId` | Runtime ID |
| `modelId` | Model ID (optional) |
| `args` | Additional command-line arguments |
| `environment` | Process environment variables |
| `active` | Whether the profile is enabled |

### Hot Configuration Reload

- `logLevel` — can be changed without restart
- `healthCheck.interval` — can be changed without restart
- `listenAddress`, `webPort`, `dataDir` — **require restart**

---

## Web Interface

After starting, GoAl is available at: **http://127.0.0.1:9090**

### Web Interface Features:

- **Dashboard** — overview of all instances and their statuses
- **Instance Management** — start, stop, restart
- **Profile CRUD** — create, edit, delete profiles
- **Runtime CRUD** — configure AI runtimes
- **Model CRUD** — configure models
- **Health Monitoring** — check runtime availability
- **Metrics** — built-in application metrics

### Authentication

If `authEnabled` is set:

1. Go to `http://127.0.0.1:9090`
2. Click **Login**
3. Enter `adminUser` and `adminPassword` from your configuration
4. After login, the session is stored in an HTTP-only cookie

---

## API

### Base URL

All API calls start with: `http://127.0.0.1:9090`

### Authentication

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/auth/login` | Login (HTTP-only cookies) |
| POST | `/api/v1/auth/logout` | Logout |
| GET | `/api/v1/auth/session` | Check session |

### Instance Management

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/instances` | List all instances |
| GET | `/api/v1/instances/{id}` | Instance status |
| POST | `/api/v1/instances/{id}/stop` | Stop instance |
| POST | `/api/v1/instances/{id}/restart` | Restart instance |

### Profiles

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/profiles` | List profiles |
| GET | `/api/v1/profiles/{id}` | Get profile |
| POST | `/api/v1/profiles` | Create profile |
| PUT | `/api/v1/profiles/{id}` | Update profile |
| DELETE | `/api/v1/profiles/{id}` | Delete profile |
| POST | `/api/v1/profiles/{id}/start` | Start by profile |
| POST | `/api/v1/profiles/{id}/stop` | Stop all processes of profile |
| POST | `/api/v1/profiles/{id}/restart` | Restart profile processes |
| GET | `/api/v1/profiles/{id}/status` | Processes status |
| POST | `/api/v1/profiles/{id}/activate` | Activate |
| POST | `/api/v1/profiles/{id}/deactivate` | Deactivate |

Profile environment values are write-only. The API and Web UI show only their
keys. Editing other profile fields preserves the stored environment when the
`environment` field is omitted. Send an explicit replacement map to change the
environment, or `{}` to remove all profile environment entries.

### Runtimes

Runtime environment values are write-only through the HTTP API. Runtime reads
and mutation responses show sorted variable names in `environment_keys`, never
their values. Editing other runtime fields preserves the stored environment
when `environment` is omitted. Send `{}` to clear it or an explicit map to
replace it. Values remain stored locally in `goal_repo.json` for process launch;
this file is not an encrypted secret vault.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/runtimes` | List runtimes |
| GET | `/api/v1/runtimes/{id}` | Get runtime |
| POST | `/api/v1/runtimes` | Create runtime |
| PUT | `/api/v1/runtimes/{id}` | Update runtime |
| DELETE | `/api/v1/runtimes/{id}` | Delete runtime |
| GET | `/api/v1/runtimes/health` | Health of all runtimes |
| GET | `/api/v1/runtimes/health/{id}` | Health of specific runtime |

### Models

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/models` | List models |
| GET | `/api/v1/models/{id}` | Get model |
| POST | `/api/v1/models` | Create model |
| PUT | `/api/v1/models/{id}` | Update model |
| DELETE | `/api/v1/models/{id}` | Delete model |

### Health Check and Version

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/health` | Health check |
| GET | `/api/v1/version` | Application version |
| GET | `/api/v1/metrics` | Application metrics |

> Migration runs automatically at startup — no status endpoint.

---

## Instance Management

### What is an Instance?

- **Profile** — launch template (configuration)
- **Instance** — running process (runtime entity)

One profile can create multiple instances. Stopping an instance does not delete the profile. Restart creates a new instance.

### CLI Management

```bash
# List all instances
curl http://127.0.0.1:9090/api/v1/instances

# Status of specific instance
curl http://127.0.0.1:9090/api/v1/instances/INSTANCE_ID

# Stop instance
curl -X POST http://127.0.0.1:9090/api/v1/instances/INSTANCE_ID/stop

# Restart instance
curl -X POST http://127.0.0.1:9090/api/v1/instances/INSTANCE_ID/restart
```

### Web Interface Management

1. Open http://127.0.0.1:9090
2. Click on the desired instance
3. Use **Stop** / **Restart** buttons

---

## Launch Profiles

### Creating a Profile

**Via Web Interface:**

1. Go to **Profiles** section
2. Click **Create Profile**
3. Fill in the fields:
   - Profile name
   - Select runtime
   - Select model (optional)
   - Specify arguments (optional)
   - Specify environment variables (optional)
4. Click **Save**

**Via API:**

```bash
curl -X POST http://127.0.0.1:9090/api/v1/profiles \
  -H "Content-Type: application/json" \
  -d '{
    "id": "my-profile",
    "name": "My Profile",
    "runtimeId": "ollama",
    "modelId": "llama3",
    "active": true
  }'
```

### Launch Command Preview

```bash
curl -X POST http://127.0.0.1:9090/api/v1/profiles/my-profile/resolve \
  -H "Content-Type: application/json"
```

Returns the full command that will be executed.

---

## Runtimes

### Creating a Runtime

**Via Web Interface:**

1. Go to **Runtimes** section
2. Click **Create Runtime**
3. Fill in the fields:
   - Runtime name
   - Type: `ollama`, `vllm`, `llama.cpp`, `custom`
   - Executable path
   - Working directory
   - Command-line arguments
   - Health Check (HTTP or TCP)
4. Click **Save**

**Via API:**

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

GoAl automatically checks runtime health every 30 seconds. Two types are supported:

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

### Checking Health

```bash
# Health of all runtimes
curl http://127.0.0.1:9090/api/v1/runtimes/health

# Health of specific runtime
curl http://127.0.0.1:9090/api/v1/runtimes/health/ollama
```

---

## Models

### Creating a Model

```bash
curl -X POST http://127.0.0.1:9090/api/v1/models \
  -H "Content-Type: application/json" \
  -d '{
    "id": "llama3",
    "name": "Llama 3",
    "runtimeId": "ollama",
    "model": "llama3:8b",
    "active": true
  }'
```

---

## Logs

### Viewing Instance Logs

**Via API:**

```bash
# Logs of specific instance
curl http://127.0.0.1:9090/api/v1/instances/INSTANCE_ID/logs

# SSE log stream
curl http://127.0.0.1:9090/api/v1/logs/stream
```

### Filtering Logs

```bash
# With instance_id filter
curl "http://127.0.0.1:9090/api/v1/logs?instance_id=INSTANCE_ID"
```

### Pagination

```bash
# Page 2, 50 records per page
curl "http://127.0.0.1:9090/api/v1/logs?page=2&page_size=50"
```

---

## Security

### Current Security Settings

| Parameter | Value |
|-----------|-------|
| Authentication | HTTP-only cookies, session-based |
| CSRF Protection | Yes, for all unsafe methods |
| Rate Limiting | 100 requests/min per IP |
| Login Rate Limit | 5 attempts / 5 minutes |
| Limit Request Body | http.MaxBytesReader |
| Bind Address | 127.0.0.1 (localhost) by default |

### Configuring for Network Access

To make GoAl accessible from the network:

1. Open `goal.json`
2. Change `listenAddress` to `"0.0.0.0"`
3. Enable authentication: `"authEnabled": true`
4. Set a password: `"adminPassword": "your_password"`
5. Restart GoAl

```json
{
  "listenAddress": "0.0.0.0",
  "webPort": 9090,
  "authEnabled": true,
  "adminPassword": "secure_password_here"
}
```

---

## Install as Service (Linux systemd)

### Manual Installation

```bash
# 1. Copy the binary
sudo cp goal /opt/goal/goal

# 2. Create configuration
sudo mkdir -p /etc/goal
sudo cp goal.example.json /etc/goal/goal.json
sudo nano /etc/goal/goal.json  # edit

# 3. Install systemd service
sudo cp deploy/systemd/goal.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable goal
sudo systemctl start goal

# 4. Check status
sudo systemctl status goal
```

### Service Logs

```bash
sudo journalctl -u goal -f
```

### Updating the Service

```bash
sudo systemctl stop goal
# replace the binary
sudo cp goal /opt/goal/goal
sudo systemctl start goal
```

---

## Install as Service (Windows)

### Via PowerShell

```powershell
# 1. Copy binary to C:\goal-starter
# 2. Create goal.json
Copy-Item goal.example.json C:\goal-starter\goal.json
notepad C:\goal-starter\goal.json

# 3. Install as Windows Service
cd C:\goal-starter
.\deploy\windows\install-service.ps1

# 4. Check the service
Get-Service goal
```

### Uninstalling the Service

```powershell
.\deploy\windows\uninstall-service.ps1
```

---

## FAQ

### Where is data stored?

Data is stored in `dataDir` from configuration (default `./data`). Repository file: `goal_repo.json`.

### How to change the port?

Change `webPort` in `goal.json` and restart GoAl.

### How to change the address?

Change `listenAddress` in `goal.json` and restart GoAl.

### What does "stale" status mean?

The process was restarted by the OS, but GoAl cannot restore its state. You need to restart it manually.

### How to reset the administrator password?

1. Open `goal.json`
2. Change `"adminPassword": "new_password"`
3. Restart GoAl

### Where are GoAl's own logs?

Logs are output to stdout/stderr. For systemd: `journalctl -u goal`. For Windows — Event Log.

### How to cross-compile?

```powershell
# Windows -> Linux
$env:GOOS='linux'; $env:GOARCH='amd64'; go build -o goal-linux ./cmd/goal
```

### How to verify binary integrity?

```bash
# Check SHA256 from checksums.txt
Get-FileHash bin/goal-windows-amd64.exe -Algorithm SHA256
```

### How to update GoAl?

1. Download the new version
2. Stop the current process
3. Replace the binary
4. Restart

The `goal.json` configuration and data in `dataDir` remain unchanged.

### How to run multiple instances of GoAl?

Each instance must have its own `goal.json` with different `webPort` and `dataDir`:

```powershell
$env:GOAL_CONFIG = "C:\goal-instance-1\goal.json"
.\goal.exe

$env:GOAL_CONFIG = "C:\goal-instance-2\goal.json"
.\goal.exe
