# Install GoAl as a Windows Service
# Usage: Run as Administrator: .\deploy\windows\install-service.ps1

$ErrorActionPreference = "Stop"

$SERVICE_NAME = "GoAl"
$BINARY_PATH = "C:\Program Files\GoAl\goal.exe"
$CONFIG_PATH = "C:\Program Files\GoAl\goal.json"

Write-Host "=== GoAl Windows Service Installer ===" -ForegroundColor Cyan
Write-Host ""

# Check if running as admin
$admin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) {
    Write-Host "[!] This script must be run as Administrator" -ForegroundColor Red
    exit 1
}

# Create installation directory
$INSTALL_DIR = Split-Path -Path $BINARY_PATH -Parent
if (-not (Test-Path $INSTALL_DIR)) {
    New-Item -ItemType Directory -Path $INSTALL_DIR | Out-Null
    Write-Host "[+] Created installation directory: $INSTALL_DIR" -ForegroundColor Green
}

# Check if goal.json exists
if (-not (Test-Path $CONFIG_PATH)) {
    Write-Host "[!] Warning: $CONFIG_PATH not found" -ForegroundColor Yellow
    Write-Host "    Please copy goal.json to this location before starting the service" -ForegroundColor Yellow
}

# Install the service using sc.exe
Write-Host "[+] Installing Windows service '$SERVICE_NAME'..." -ForegroundColor Yellow
sc.exe binname=$SERVICE_NAME start=auto obj=LocalSystem binPath=$BINARY_PATH DisplayName="GoAl - Local AI Runtime Manager"

if ($LASTEXITCODE -ne 0) {
    Write-Host "[-] Failed to install service" -ForegroundColor Red
    exit 1
}

Write-Host "[+] Service installed successfully" -ForegroundColor Green
Write-Host ""
Write-Host "To start the service, run:" -ForegroundColor Cyan
Write-Host "  Start-Service $SERVICE_NAME" -ForegroundColor White
Write-Host ""
Write-Host "To uninstall the service, run:" -ForegroundColor Cyan
Write-Host "  .\uninstall-service.ps1" -ForegroundColor White
Write-Host ""