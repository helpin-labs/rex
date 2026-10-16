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
function Invoke-Browser([string]$arguments) {
    # A CLI daemon can keep its parent's stdout handle open; wait only for the CLI.
    $output = Join-Path $evidence 'command.out'
    $errors = Join-Path $evidence 'command.err'
    $process = Start-Process $cli -ArgumentList "--session $session --cdp $port $arguments" -RedirectStandardOutput $output -RedirectStandardError $errors -PassThru
    if (!$process.WaitForExit(60000)) { $process.Kill(); throw "Browser check timed out: $arguments" }
    $result = Get-Content -Raw $output -ErrorAction SilentlyContinue
    if ($process.ExitCode -ne 0) { throw "Browser check failed: $arguments $result $(Get-Content -Raw $errors)" }
    return $result
}
try {
    $app = Start-Process (Join-Path $InstallDirectory 'rex-desktop.exe') -PassThru
    $deadline = [DateTime]::UtcNow.AddMinutes(2)
    do {
        if ($app.HasExited) { throw "Installed desktop exited: $($app.ExitCode)" }
        try { $targets = Invoke-RestMethod "http://127.0.0.1:$port/json/list" -TimeoutSec 2 } catch { $targets = @() }
        if (@($targets | Where-Object type -eq 'page').Count -gt 0) { break }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
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
    Write-Host "Installed WebView2 onboarding, provider switching and native attention commands passed. Evidence: $evidence"
} finally {
    $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = $previousArguments
    if ($app -and !$app.HasExited) { $app.Kill($true); $app.WaitForExit(10000) | Out-Null }
    Get-Process agent-browser -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $cli } | Stop-Process -Force -ErrorAction SilentlyContinue
}
