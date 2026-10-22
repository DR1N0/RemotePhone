#Requires -RunAsAdministrator
<#
.SYNOPSIS
  One-time Windows Firewall allow-rules for mako's host programs.

.DESCRIPTION
  Windows shows the "Windows Security Alert" firewall popup every time a new
  executable starts listening on a non-loopback interface. `go run` makes a
  fresh temp executable on every invocation, so a probe launched that way
  prompts every single time. This script adds inbound allow-rules for the
  fixed binaries mako actually runs, so the popup stops appearing for them.

  Run it once from an elevated PowerShell:
      powershell -ExecutionPolicy Bypass -File scripts\windows\firewall-allow.ps1

  Add -DisableNotifications to also turn off the firewall's "blocked app"
  notification for programs not covered by a rule. That is a machine-wide
  setting; omit it if you want to keep the popup for unknown programs.
#>
param(
    [switch]$DisableNotifications
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$sdkRoot = if ($env:ANDROID_SDK_ROOT) { $env:ANDROID_SDK_ROOT }
           elseif ($env:ANDROID_HOME) { $env:ANDROID_HOME }
           else { Join-Path $env:LOCALAPPDATA "Android\Sdk" }

$programs = @(
    (Join-Path $repoRoot ".bin\mako-controlplane.exe"),
    (Join-Path $repoRoot ".bin\mako-worker.exe"),
    (Join-Path $repoRoot ".bin\mako-probe.exe"),
    (Join-Path $sdkRoot "emulator\emulator.exe"),
    (Join-Path $sdkRoot "emulator\qemu\windows-x86_64\qemu-system-x86_64.exe"),
    (Join-Path $sdkRoot "emulator\qemu\windows-x86_64\qemu-system-x86_64-headless.exe")
)

foreach ($program in $programs) {
    if (-not (Test-Path $program)) {
        Write-Host "skip (not built/found): $program"
        continue
    }
    $displayName = "mako allow $([IO.Path]::GetFileName($program))"
    Get-NetFirewallRule -DisplayName $displayName -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule
    New-NetFirewallRule -DisplayName $displayName -Direction Inbound -Action Allow `
        -Program $program -Profile Any -Enabled True | Out-Null
    Write-Host "allowed: $program"
}

if ($DisableNotifications) {
    Set-NetFirewallProfile -Profile Domain,Private,Public -NotifyOnListen False
    Write-Host "disabled Windows Firewall listen notifications"
}
