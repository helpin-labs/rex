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

# The pinned CLI is standalone; end users do not need Node or npm.
$cliArchive = Join-Path $cache 'agent-browser-0.25.5.tgz'
$cliChecksum = '12126bd9b6befdd422474e5fc85372326444f18de96dac12fb21d3acbb4a7b37'
if (!(Test-Path $cliArchive) -or (Get-FileHash $cliArchive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $cliChecksum) {
    Invoke-WebRequest https://registry.npmjs.org/agent-browser/-/agent-browser-0.25.5.tgz -OutFile "$cliArchive.part" -TimeoutSec 300
    if ((Get-FileHash "$cliArchive.part" -Algorithm SHA256).Hash.ToLowerInvariant() -ne $cliChecksum) { throw 'Browser CLI checksum mismatch' }
    Move-Item -Force "$cliArchive.part" $cliArchive
}
$extracted = Join-Path $cache ('browser-cli-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $extracted | Out-Null
try {
    tar -xzf $cliArchive -C $extracted package/bin/agent-browser-win32-x64.exe package/LICENSE
    if ($LASTEXITCODE -ne 0) { throw 'Browser CLI extraction failed' }
    Copy-Item (Join-Path $extracted 'package/bin/agent-browser-win32-x64.exe') (Join-Path $destination agent-browser.exe) -Force
    Copy-Item (Join-Path $extracted 'package/LICENSE') (Join-Path $destination agent-browser-LICENSE) -Force
    $version = & (Join-Path $destination agent-browser.exe) --version
    if ($LASTEXITCODE -ne 0 -or $version -notmatch '0\.25\.5') { throw 'Bundled browser CLI did not start at the expected version' }
} finally {
    Remove-Item -Recurse -Force $extracted
}
