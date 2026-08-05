# Uninstall GoAl Windows Service
# Usage: Run as Administrator: .\deploy\windows\uninstall-service.ps1

$ErrorActionPreference = "Stop"

$SERVICE_NAME = "GoAl"

Write-Host "=== GoAl Windows Service Uninstaller ===" -ForegroundColor Cyan
Write-Host ""

# Check if running as admin
$admin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) {
    Write-Host "[!] This script must be run as Administrator" -ForegroundColor Red
    exit 1
}

# Check if service exists
$service = Get-Service -Name $SERVICE_NAME -ErrorAction SilentlyContinue
if ($service) {
    if ($service.Status -ne 'Stopped') {
        Write-Host "[+] Stopping service '$SERVICE_NAME'..." -ForegroundColor Yellow
        Stop-Service -Name $SERVICE_NAME -Force
    }
    Write-Host "[+] Removing service '$SERVICE_NAME'..." -ForegroundColor Yellow
    sc.exe delete $SERVICE_NAME
    if ($LASTEXITCODE -eq 0) {
        Write-Host "[+] Service removed successfully" -ForegroundColor Green
    } else {
        Write-Host "[-] Failed to remove service" -ForegroundColor Red
        exit 1
    }
} else {
    Write-Host "[!] Service '$SERVICE_NAME' not found" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "[+] Uninstall complete" -ForegroundColor Green