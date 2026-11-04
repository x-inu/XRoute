<#
.SYNOPSIS
  xroute one-line installer for Windows: downloads the latest release
  .exe from GitHub, installs it, and prints next steps.

.DESCRIPTION
  Run in PowerShell (no admin needed):

    irm https://raw.githubusercontent.com/x-inu/XRoute/main/install.ps1 | iex

  Optional overrides (set before piping, or pass when saved locally):

    $env:VERSION = "v1.9.2"   # default: latest
    $env:BINDIR  = "C:\Tools" # default: $env:LOCALAPPDATA\xroute
#>
[CmdletBinding()]
param(
  [string]$Version = $env:VERSION,
  [string]$InstallDir = $env:BINDIR
)

$ErrorActionPreference = 'Stop'

$Repo = 'x-inu/XRoute'
if ([string]::IsNullOrWhiteSpace($Version)) { $Version = 'latest' }
if ([string]::IsNullOrWhiteSpace($InstallDir)) {
  $InstallDir = Join-Path $env:LOCALAPPDATA 'xroute'
}

function Write-Info($msg) { Write-Host "[xroute] $msg" -ForegroundColor Green }

# Windows release asset is amd64-only; refuse ARM with a clear message.
$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -ne 'AMD64') {
  throw "Unsupported architecture: $arch (Windows asset is amd64-only; see https://github.com/$Repo/releases/latest)"
}

$Asset = 'xroute-windows-amd64.exe'
if ($Version -eq 'latest') {
  $Url = "https://github.com/$Repo/releases/latest/download/$Asset"
} else {
  $Url = "https://github.com/$Repo/releases/download/$Version/$Asset"
}

if (Get-Process -Name 'xroute' -ErrorAction SilentlyContinue) {
  throw 'xroute is currently running — stop it first (Ctrl+C in its terminal), then re-run the installer.'
}

if (-not (Test-Path $InstallDir)) {
  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}
$Dest = Join-Path $InstallDir 'xroute.exe'
$Tmp = Join-Path $env:TEMP 'xroute-download.exe'

Write-Info "downloading $Url ..."
Invoke-WebRequest -Uri $Url -OutFile $Tmp -UseBasicParsing
Move-Item -Path $Tmp -Destination $Dest -Force

# Add install dir to the *user* PATH so `xroute` works in new terminals.
$currentUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$newPath = $null
if ([string]::IsNullOrWhiteSpace($currentUserPath)) {
  $newPath = $InstallDir
} elseif ((($currentUserPath -split ';')) -notcontains $InstallDir) {
  $newPath = $currentUserPath.TrimEnd(';') + ';' + $InstallDir
}
if ($newPath) {
  [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
  Write-Info "added $InstallDir to your user PATH (restart the terminal to use it)"
}
