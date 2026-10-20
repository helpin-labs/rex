# Exercise the installed Tauri/WebView2 app in a disposable CI account.
param([Parameter(Mandatory)][string]$InstallDirectory)
$ErrorActionPreference = 'Stop'
$session = 'rex-desktop-smoke-' + [Guid]::NewGuid().ToString('N')
$evidence = Join-Path $env:RUNNER_TEMP $session
New-Item -ItemType Directory $evidence | Out-Null
$cli = Join-Path $InstallDirectory 'resources/browser/agent-browser.exe'
$portListener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
$portListener.Start()
$port = $portListener.LocalEndpoint.Port
$portListener.Stop()
$previousArguments = $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS
$env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = "--remote-debugging-port=$port --remote-debugging-address=127.0.0.1"
$app = $null
$duplicate = $null
$policy = $null
$policyName = 'rex-desktop.exe'
$previousPolicy = $null
$previousPolicyKind = $null
function Invoke-Browser([string]$arguments) {
    # A CLI daemon can keep its parent's stdout handle open; wait only for the CLI.
    $output = Join-Path $evidence ('command-' + [Guid]::NewGuid().ToString('N') + '.out')
    $errors = $output + '.err'
    $process = Start-Process $cli -ArgumentList "--session $session --cdp $port $arguments" -RedirectStandardOutput $output -RedirectStandardError $errors -PassThru
    if (!$process.WaitForExit(60000)) { $process.Kill(); throw "Browser check timed out: $arguments" }
    $result = Get-Content -Raw $output -ErrorAction SilentlyContinue
    if ($process.ExitCode -ne 0) { throw "Browser check failed: $arguments $result $(Get-Content -Raw $errors)" }
    return $result
}
try {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    if ([Security.Principal.WindowsPrincipal]::new($identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        # WebView2 150+ ignores environment overrides in elevated CI; scope trusted policy to Rex.
        $policy = [Microsoft.Win32.Registry]::LocalMachine.CreateSubKey('SOFTWARE\Policies\Microsoft\Edge\WebView2\AdditionalBrowserArguments')
        $previousPolicy = $policy.GetValue($policyName, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        if ($null -ne $previousPolicy) { $previousPolicyKind = $policy.GetValueKind($policyName) }
        $policy.SetValue($policyName, "$previousPolicy $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS".Trim(), [Microsoft.Win32.RegistryValueKind]::String)
    }
    $app = Start-Process (Join-Path $InstallDirectory 'rex-desktop.exe') -RedirectStandardOutput (Join-Path $evidence 'app.out') -RedirectStandardError (Join-Path $evidence 'app.err') -PassThru
    $deadline = [DateTime]::UtcNow.AddMinutes(2)
    do {
        if ($app.HasExited) { throw "Installed desktop exited: $($app.ExitCode)" }
        try { $targets = Invoke-RestMethod "http://127.0.0.1:$port/json/list" -TimeoutSec 2 } catch { $targets = @() }
        if (@($targets | Where-Object type -eq 'page').Count -gt 0) { break }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    if (@($targets | Where-Object type -eq 'page').Count -eq 0) { throw 'Installed WebView2 did not expose a page; inspect app.err in the evidence directory' }
    Invoke-Browser 'snapshot' | Set-Content (Join-Path $evidence 'initial.txt')
    Invoke-Browser "screenshot $evidence/initial.png" | Out-Null
    Invoke-Browser 'wait --text "Welcome to Rex"' | Out-Null
    $snapshot = Invoke-Browser 'snapshot -i'
    $snapshot | Set-Content (Join-Path $evidence 'onboarding.txt')
    if ($snapshot -notmatch 'Find models') { throw "Onboarding controls missing: $snapshot" }
    Invoke-Browser 'find role button click --name "Hosted (Anthropic, OpenAI, OpenRouter)"' | Out-Null
    $snapshot = Invoke-Browser 'snapshot -i'
    if ($snapshot -notmatch 'Anthropic API key' -or $snapshot -notmatch 'OpenAI API key') { throw "Hosted onboarding did not render: $snapshot" }
    Invoke-Browser 'eval "window.__TAURI_INTERNALS__.invoke(''set_attention'', {count: 12})"' | Out-Null
    Invoke-Browser 'eval "window.__TAURI_INTERNALS__.invoke(''set_attention'', {count: 0})"' | Out-Null
    Invoke-Browser "screenshot $evidence/installed-desktop.png" | Out-Null
    $data = Join-Path $env:LOCALAPPDATA 'ai.rex.desktop'
    $connectionPath = Join-Path $data 'connection.json'
    $connectionHash = (Get-FileHash $connectionPath -Algorithm SHA256).Hash
    $connection = Get-Content -Raw $connectionPath | ConvertFrom-Json
    $runtimePID = Get-Content -Raw (Join-Path $data 'runtime.pid')
    if (-not ('RexSmokeWindow' -as [type])) {
        Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class RexSmokeWindow {
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
}
'@
    }
    $app.Refresh()
    $window = $app.MainWindowHandle
    if ($window -eq [IntPtr]::Zero) { throw 'Rex has no visible main window' }
    if (!$app.CloseMainWindow()) { throw 'Could not close the Rex window to the tray' }
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        $app.Refresh()
        if ($app.HasExited) { throw 'Closing the window exited Rex instead of hiding it' }
        if (![RexSmokeWindow]::IsWindowVisible($window)) { break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    if ([RexSmokeWindow]::IsWindowVisible($window)) { throw 'Rex window did not hide in the tray' }
    $duplicate = Start-Process (Join-Path $InstallDirectory 'rex-desktop.exe') -RedirectStandardOutput (Join-Path $evidence 'duplicate.out') -RedirectStandardError (Join-Path $evidence 'duplicate.err') -PassThru
    if (!$duplicate.WaitForExit(10000)) { throw 'Reopening Rex started a second desktop instance' }
    if ($duplicate.ExitCode -ne 0) { throw "Reopening Rex exited with code $($duplicate.ExitCode)" }
    $app.Refresh()
    if ($app.HasExited -or ![RexSmokeWindow]::IsWindowVisible($window)) { throw 'Reopening Rex did not reveal the original window' }
    if ((Get-FileHash $connectionPath -Algorithm SHA256).Hash -ne $connectionHash -or (Get-Content -Raw (Join-Path $data 'runtime.pid')) -ne $runtimePID) {
        throw 'Reopening Rex replaced the host connection or runtime'
    }
    $hosts = @(Get-Process rex-host -ErrorAction SilentlyContinue | Where-Object Path -eq (Join-Path $InstallDirectory 'rex-host.exe'))
    if ($hosts.Count -ne 1) { throw "Expected one Rex host after reopening; found $($hosts.Count)" }
    $status = Invoke-RestMethod ($connection.url + '/api/status') -Headers @{ Authorization = 'Bearer ' + $connection.token } -TimeoutSec 5
    if (!$status.runtime_ready) { throw 'Original engine was not ready after reopening' }
    Write-Host "Installed WebView2 onboarding, provider switching, attention and reopening the tray instance passed. Evidence: $evidence"
} finally {
    foreach ($name in @('rex.log', 'embedder.log')) {
        $log = Join-Path $env:LOCALAPPDATA "ai.rex.desktop/$name"
        if (Test-Path $log) { Copy-Item $log (Join-Path $evidence $name) -ErrorAction Continue }
    }
    if ($policy) {
        if ($null -eq $previousPolicy) { $policy.DeleteValue($policyName, $false) }
        else { $policy.SetValue($policyName, $previousPolicy, $previousPolicyKind) }
        $policy.Dispose()
    }
    $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = $previousArguments
    if ($duplicate -and !$duplicate.HasExited) { $duplicate.Kill($true); $duplicate.WaitForExit(10000) | Out-Null }
    if ($app -and !$app.HasExited) { $app.Kill($true); $app.WaitForExit(10000) | Out-Null }
    Get-Process agent-browser -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $cli } | Stop-Process -Force -ErrorAction SilentlyContinue
}
