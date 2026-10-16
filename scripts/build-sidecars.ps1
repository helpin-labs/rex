param([ValidateSet('amd64','arm64')][string]$Architecture = 'amd64')
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$repoRoot = Split-Path -Parent $PSScriptRoot
$triple = if ($Architecture -eq 'arm64') { 'aarch64-pc-windows-msvc' } else { 'x86_64-pc-windows-msvc' }
$binDir = Join-Path $repoRoot 'src-tauri/binaries'
New-Item -ItemType Directory -Force $binDir | Out-Null
$previousGOOS, $previousGOARCH, $previousCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
Push-Location $repoRoot
try {
    $env:GOOS = 'windows'; $env:GOARCH = $Architecture; $env:CGO_ENABLED = '0'
    # In CI, GOWORK selects the pinned Windows runtime source until it is released.
    $hostFlags = '-s -w'
    if ($env:REX_GOOGLE_CLIENT_ID) {
        $hostFlags += " -X github.com/helpin-ai/rex/internal/rex.googleClientID=$env:REX_GOOGLE_CLIENT_ID -X github.com/helpin-ai/rex/internal/rex.googleClientSecret=$env:REX_GOOGLE_CLIENT_SECRET"
    }
    go build -trimpath -ldflags $hostFlags -o (Join-Path $binDir "rex-host-$triple.exe") ./cmd/rex-host
    go build -trimpath -ldflags '-s -w' -o (Join-Path $binDir "agent-runtime-$triple.exe") github.com/helpin-ai/agent-runtime/cmd/agent-runtime
    go list -m github.com/helpin-ai/agent-runtime
    & (Join-Path $PSScriptRoot 'build-embedder.ps1') -Architecture $Architecture
    & (Join-Path $PSScriptRoot 'build-browser.ps1') -Architecture $Architecture
    & (Join-Path $PSScriptRoot 'build-wsl.ps1') -Architecture $Architecture
} finally {
    $env:GOOS = $previousGOOS; $env:GOARCH = $previousGOARCH; $env:CGO_ENABLED = $previousCGO
    Pop-Location
}
