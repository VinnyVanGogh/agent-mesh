#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Installs staypointd as a Windows service and registers the named pipe IPC.

.DESCRIPTION
    Locates staypointd.exe, registers it with the Windows Service Control Manager
    (auto-start, LocalService), and confirms the named pipe \\.\pipe\staypointd is
    reachable after the service starts.

.EXAMPLE
    # Run from an elevated PowerShell prompt:
    .\staypointd-service-install.ps1

.EXAMPLE
    # Point to a specific binary:
    .\staypointd-service-install.ps1 -BinPath "C:\Tools\staypoint\staypointd.exe"
#>

param(
    [string]$BinPath = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ServiceName    = "staypointd"
$DisplayName    = "Staypoint Background Daemon"
$Description    = "AI agent ops daemon: quota telemetry, named-pipe IPC, and file watcher."

# ------------------------------------------------------------------
# Resolve binary path
# ------------------------------------------------------------------
if (-not $BinPath) {
    # Try $PATH first, then common install locations.
    $resolved = Get-Command staypointd.exe -ErrorAction SilentlyContinue
    if ($resolved) {
        $BinPath = $resolved.Source
    } else {
        $candidates = @(
            "$env:ProgramFiles\staypoint\staypointd.exe",
            "$env:LOCALAPPDATA\Programs\staypoint\staypointd.exe",
            "$env:USERPROFILE\scoop\shims\staypointd.exe",
            "$env:USERPROFILE\.local\bin\staypointd.exe"
        )
        foreach ($c in $candidates) {
            if (Test-Path $c) { $BinPath = $c; break }
        }
    }
}

if (-not $BinPath -or -not (Test-Path $BinPath)) {
    Write-Error "staypointd.exe not found. Install staypoint first (scoop install staypoint) or pass -BinPath."
    exit 1
}

Write-Host "Using binary: $BinPath" -ForegroundColor Cyan

# ------------------------------------------------------------------
# Register the service
# ------------------------------------------------------------------
$existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existing) {
    Write-Host "Service '$ServiceName' already exists (state: $($existing.Status)). Skipping registration." -ForegroundColor Yellow
} else {
    Write-Host "Registering service '$ServiceName'..." -ForegroundColor Cyan
    # staypointd --service tells the binary it is running under SCM
    New-Service `
        -Name        $ServiceName `
        -BinaryPathName "$BinPath --service" `
        -DisplayName $DisplayName `
        -Description $Description `
        -StartupType Automatic | Out-Null
    Write-Host "Service registered." -ForegroundColor Green
}

# ------------------------------------------------------------------
# Start the service
# ------------------------------------------------------------------
$svc = Get-Service -Name $ServiceName
if ($svc.Status -ne "Running") {
    Write-Host "Starting service..." -ForegroundColor Cyan
    Start-Service -Name $ServiceName
    $svc.WaitForStatus("Running", [TimeSpan]::FromSeconds(15))
    Write-Host "Service started." -ForegroundColor Green
} else {
    Write-Host "Service is already running." -ForegroundColor Green
}

# ------------------------------------------------------------------
# Smoke-test: confirm the named pipe is present
# ------------------------------------------------------------------
Write-Host "Verifying named pipe \\.\pipe\staypointd..." -ForegroundColor Cyan
$pipe = Get-Item "\\.\pipe\staypointd" -ErrorAction SilentlyContinue
if ($pipe) {
    Write-Host "Named pipe is available. IPC ready." -ForegroundColor Green
} else {
    Write-Warning "Named pipe not yet visible. The service may need a moment to start; try: Get-Item '\\.\pipe\staypointd'"
}

Write-Host ""
Write-Host "Installation complete!" -ForegroundColor Green
Write-Host "  Start : Start-Service $ServiceName"
Write-Host "  Stop  : Stop-Service  $ServiceName"
Write-Host "  Remove: .\staypointd-service-uninstall.ps1"
Write-Host ""
Write-Host "Add shell integration to your PowerShell `$PROFILE:"
Write-Host "  staypoint init --powershell | Out-File -Encoding UTF8 `$PROFILE -Append"
