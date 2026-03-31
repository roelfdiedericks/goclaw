# GoClaw Windows Installer
# Usage: irm goclaw.org/install-windows.ps1 | iex
#    or: powershell -ExecutionPolicy Bypass -File install-windows.ps1
#
# This script installs GoClaw on Windows via WSL2 (Windows Subsystem for Linux).

param(
    [switch]$Help
)

$ErrorActionPreference = "Stop"

function Write-Step { param($msg) Write-Host "`n==> " -NoNewline -ForegroundColor Blue; Write-Host $msg }
function Write-Success { param($msg) Write-Host "==> " -NoNewline -ForegroundColor Green; Write-Host $msg }
function Write-Warn { param($msg) Write-Host "Warning: " -NoNewline -ForegroundColor Yellow; Write-Host $msg }
function Write-Err { param($msg) Write-Host "Error: " -NoNewline -ForegroundColor Red; Write-Host $msg }

if ($Help) {
    Write-Host @"
GoClaw Windows Installer

This script installs GoClaw on Windows via WSL2 (Windows Subsystem for Linux).

Requirements:
  - Windows 10 version 2004+ or Windows 11
  - Administrator privileges (for WSL installation if not already enabled)

What it does:
  1. Enables WSL2 if not already enabled (may require restart)
  2. Installs Debian Linux distribution
  3. Runs the GoClaw Linux installer inside WSL
  4. Creates a desktop shortcut

Usage:
  irm goclaw.org/install-windows.ps1 | iex

Options:
  -Help    Show this help message

"@
    exit 0
}

Write-Host "`nGoClaw Windows Installer`n" -ForegroundColor Cyan

# Check Windows version
$osVersion = [Environment]::OSVersion.Version
if ($osVersion.Build -lt 19041) {
    Write-Err "Windows 10 version 2004 (build 19041) or later required."
    Write-Host "Your version: $($osVersion.ToString())"
    exit 1
}
Write-Step "Windows version OK (build $($osVersion.Build))"

# Check if running as admin (needed for WSL enable)
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

# Check WSL status
$wslFeature = $null
$vmFeature = $null
try {
    $wslFeature = Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Windows-Subsystem-Linux -ErrorAction SilentlyContinue
    $vmFeature = Get-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform -ErrorAction SilentlyContinue
} catch {
    # May fail on non-admin, that's OK - we'll check via wsl command
}

# Try to detect if WSL is working by running wsl --status
$wslWorking = $false
try {
    $null = wsl --status 2>$null
    if ($LASTEXITCODE -eq 0) {
        $wslWorking = $true
    }
} catch {
    $wslWorking = $false
}

if (-not $wslWorking) {
    if ($wslFeature -and $wslFeature.State -ne 'Enabled') {
        Write-Step "WSL2 not enabled, enabling..."
        
        if (-not $isAdmin) {
            Write-Err "Administrator privileges required to enable WSL."
            Write-Host "`nPlease right-click PowerShell and select 'Run as Administrator', then run this script again."
            exit 1
        }
        
        try {
            Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Windows-Subsystem-Linux -NoRestart -ErrorAction Stop | Out-Null
            Enable-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform -NoRestart -ErrorAction Stop | Out-Null
        } catch {
            Write-Err "Failed to enable WSL: $_"
            exit 1
        }
        
        Write-Warn "A restart is required to complete WSL installation."
        Write-Host "`nAfter restart, run this script again to continue.`n"
        
        $restart = Read-Host "Restart now? (y/N)"
        if ($restart -eq 'y' -or $restart -eq 'Y') {
            Restart-Computer
        }
        exit 0
    }
}

Write-Step "WSL2 available"

# Set WSL2 as default version
try {
    wsl --set-default-version 2 2>$null | Out-Null
} catch {
    # Ignore errors, WSL2 might already be default
}

# Check if Debian is installed
Write-Step "Checking for Debian distribution..."
$distros = @()
try {
    $distroOutput = wsl --list --quiet 2>$null
    if ($distroOutput) {
        $distros = $distroOutput -split "`n" | Where-Object { $_ -match '\S' } | ForEach-Object { $_.Trim() }
    }
} catch {
    # Ignore
}

if ($distros -notcontains "Debian") {
    Write-Step "Installing Debian (this may take a few minutes)..."
    
    try {
        wsl --install -d Debian --no-launch
    } catch {
        Write-Err "Failed to install Debian: $_"
        Write-Host "You may need to run: wsl --install -d Debian"
        exit 1
    }
    
    # Wait for installation to complete
    Start-Sleep -Seconds 3
    
    # Initialize Debian (creates default user)
    Write-Step "Initializing Debian..."
    Write-Host "You may be asked to create a UNIX username and password."
    Write-Host "This is for the Linux environment, not your Windows account.`n"
    
    try {
        wsl -d Debian -- echo "Debian initialized successfully"
    } catch {
        Write-Warn "Debian initialization may require manual setup on first run"
    }
}

Write-Success "Debian installed"

# Run GoClaw installer inside WSL
Write-Step "Installing GoClaw in WSL..."
try {
    wsl -d Debian -- sh -c 'curl -fsSL https://goclaw.org/install.sh | sh'
} catch {
    Write-Err "Failed to install GoClaw in WSL: $_"
    Write-Host "You can try manually: wsl -d Debian"
    Write-Host "Then run: curl -fsSL https://goclaw.org/install.sh | sh"
    exit 1
}

# Create desktop shortcut
Write-Step "Creating desktop shortcut..."
try {
    $desktopPath = [Environment]::GetFolderPath("Desktop")
    $shortcutPath = Join-Path $desktopPath "GoClaw.lnk"

    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $shell.CreateShortcut($shortcutPath)
    $shortcut.TargetPath = "wsl.exe"
    $shortcut.Arguments = "-d Debian -- ~/.goclaw/bin/goclaw gateway"
    $shortcut.WorkingDirectory = "%USERPROFILE%"
    $shortcut.Description = "GoClaw AI Agent Gateway"
    $shortcut.Save()
    
    Write-Success "Desktop shortcut created: GoClaw.lnk"
} catch {
    Write-Warn "Could not create desktop shortcut: $_"
}

# Done
Write-Host "`n" -NoNewline
Write-Success "Installation complete!"
Write-Host @"

To start GoClaw:
  - Double-click the "GoClaw" shortcut on your desktop, or
  - Open PowerShell and run: wsl -d Debian -- ~/.goclaw/bin/goclaw gateway

First time setup:
  - Open WSL: wsl -d Debian
  - Run: goclaw onboard

Documentation: https://goclaw.org/docs

"@
