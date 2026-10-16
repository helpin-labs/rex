# Bundle the same Chrome for Testing build verified by native Windows CI.
param([ValidateSet('amd64','arm64')][string]$Architecture = 'amd64')
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
if ($Architecture -ne 'amd64') { throw 'A Windows ARM64 browser bundle has not been verified' }
$repoRoot = Split-Path -Parent $PSScriptRoot
$destination = Join-Path $repoRoot 'src-tauri/resources/browser'
$cache = Join-Path $repoRoot '.cache'
New-Item -ItemType Directory -Force $cache | Out-Null
$archive = Join-Path $cache 'chrome-149.0.7827.115-win64.zip'
$expected = '1553389900824037aec828effab3051337df57a571e2f8800ee71cf8ed6fa76d'
if (!(Test-Path $archive) -or (Get-FileHash $archive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected) {
    Invoke-WebRequest https://storage.googleapis.com/chrome-for-testing-public/149.0.7827.115/win64/chrome-win64.zip -OutFile "$archive.part" -TimeoutSec 300
    if ((Get-FileHash "$archive.part" -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected) { throw 'Chrome checksum mismatch' }
    Move-Item -Force "$archive.part" $archive
}
New-Item -ItemType Directory -Force $destination | Out-Null
Expand-Archive -Force $archive $destination
if (!(Test-Path (Join-Path $destination 'chrome-win64/chrome.exe'))) { throw 'Browser bundle is incomplete' }
