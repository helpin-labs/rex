$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$browser = Join-Path $PSScriptRoot '../src-tauri/resources/browser'
go test ./... -timeout 5m
try {
    $env:REX_AGENT_BROWSER_EXECUTABLE = Join-Path $browser 'agent-browser.exe'
    $env:REX_BROWSER_PIPE_E2E = Join-Path $browser 'chrome-win64/chrome.exe'
    go test ./internal/rex -run '^TestPlainBrowserNativePipe$' -count=1 -timeout 1m
    $env:REX_BROWSER_PIPE_E2E = "${env:ProgramFiles(x86)}/Microsoft/Edge/Application/msedge.exe"
    if (!(Test-Path $env:REX_BROWSER_PIPE_E2E)) { throw 'Edge was not found on the Windows runner' }
    go test ./internal/rex -run '^TestPlainBrowserNativePipe$' -count=1 -timeout 1m
    $env:REX_BROWSER_E2E = Join-Path $browser 'chrome-win64/chrome.exe'
    go test ./internal/rex -run '^TestPlainBrowserHandsCookiesToTheBot$' -count=1 -timeout 2m
} finally {
    $env:REX_AGENT_BROWSER_EXECUTABLE = $null
    $env:REX_BROWSER_PIPE_E2E = $null
    $env:REX_BROWSER_E2E = $null
}
$previousArch = $env:GOARCH
try {
    $env:GOARCH = 'arm64'
    go build -o (Join-Path $env:TEMP 'rex-host-arm64.exe') ./cmd/rex-host
} finally { $env:GOARCH = $previousArch }
