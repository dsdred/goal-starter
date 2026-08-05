$ErrorActionPreference = 'Stop'

Write-Host 'Checking Git...'
if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
    throw 'Git is not installed or not available in PATH.'
}

Write-Host 'Checking Go...'
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go is not installed or not available in PATH. Install Go, reopen the terminal, and rerun this script.'
}

if (-not (Test-Path .git)) {
    git init
    git add .
    git commit -m 'Initial GoAl starter'
}

gofmt -w .
go test ./...
go vet ./...
go build -o bin/goal.exe ./cmd/goal

Write-Host 'GoAl bootstrap completed.'
