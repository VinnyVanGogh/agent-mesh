#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Stops and unregisters the staypointd Windows service.
#>

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ServiceName = "staypointd"

$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if (-not $svc) {
    Write-Host "Service '$ServiceName' is not installed." -ForegroundColor Yellow
    exit 0
}

if ($svc.Status -eq "Running") {
    Write-Host "Stopping service '$ServiceName'..." -ForegroundColor Cyan
    Stop-Service -Name $ServiceName -Force
    $svc.WaitForStatus("Stopped", [TimeSpan]::FromSeconds(15))
    Write-Host "Service stopped." -ForegroundColor Green
}

Write-Host "Removing service '$ServiceName'..." -ForegroundColor Cyan
# Use sc.exe for compatibility with all PowerShell versions.
sc.exe delete $ServiceName | Out-Null
Write-Host "Service removed." -ForegroundColor Green

# Remove the event log source if it was registered.
$logSource = $ServiceName
if ([System.Diagnostics.EventLog]::SourceExists($logSource)) {
    [System.Diagnostics.EventLog]::DeleteEventSource($logSource)
    Write-Host "Event log source removed." -ForegroundColor Green
}

Write-Host "Uninstall complete." -ForegroundColor Green
