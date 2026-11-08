$ErrorActionPreference = 'Stop'
# Tauri downloads its NSIS toolset from a GitHub release link while bundling,
# and that link can answer 504 for long stretches. Stage the same files in
# Tauri's cache first, fetched through the GitHub API with retries and
# checked against the hashes Tauri pins; the bundler then skips the download.
# Keep these in step with the Tauri CLI (crates/tauri-bundler nsis/mod.rs).
$nsisTag = 'nsis-3.11'
$nsisSha1 = 'EF7FF767E5CBD9EDD22ADD3A32C9B8F4500BB10D'
$utilsTag = 'nsis_tauri_utils-v0.5.3'
$utilsSha1 = '75197FEE3C6A814FE035788D1C34EAD39349B860'

$tools = Join-Path $env:LOCALAPPDATA 'tauri'
$nsis = Join-Path $tools 'NSIS'
$utils = Join-Path $nsis 'Plugins/x86-unicode/additional/nsis_tauri_utils.dll'
if ((Test-Path (Join-Path $nsis 'makensis.exe')) -and (Test-Path $utils) -and (Get-FileHash $utils -Algorithm SHA1).Hash -eq $utilsSha1) {
    Write-Host "NSIS toolset already staged at $nsis"
    exit 0
}

function Get-ReleaseAsset([string]$repo, [string]$tag, [string]$name, [string]$sha1, [string]$dir) {
    for ($attempt = 1; $attempt -le 6; $attempt++) {
        Remove-Item (Join-Path $dir $name) -ErrorAction SilentlyContinue
        gh release download $tag --repo $repo --pattern $name --dir $dir --clobber
        $path = Join-Path $dir $name
        if ($LASTEXITCODE -eq 0 -and (Test-Path $path) -and (Get-FileHash $path -Algorithm SHA1).Hash -eq $sha1) { return $path }
        Write-Warning "Download of $name failed or did not match its hash (attempt $attempt); retrying"
        Start-Sleep -Seconds (10 * $attempt)
    }
    throw "Could not download $name from $repo $tag"
}

$download = Join-Path $env:RUNNER_TEMP 'nsis-download'
New-Item -ItemType Directory $download, $tools -Force | Out-Null
$zip = Get-ReleaseAsset 'tauri-apps/binary-releases' $nsisTag 'nsis-3.11.zip' $nsisSha1 $download
$dll = Get-ReleaseAsset 'tauri-apps/nsis-tauri-utils' $utilsTag 'nsis_tauri_utils.dll' $utilsSha1 $download

Remove-Item $nsis, (Join-Path $tools 'nsis-3.11') -Recurse -Force -ErrorAction SilentlyContinue
Expand-Archive $zip -DestinationPath $tools -Force
Rename-Item (Join-Path $tools 'nsis-3.11') 'NSIS'
New-Item -ItemType Directory (Split-Path $utils) -Force | Out-Null
Copy-Item $dll $utils
foreach ($required in @('makensis.exe', 'Bin/makensis.exe', 'Stubs/lzma-x86-unicode', 'Include/MUI2.nsh', 'Plugins/x86-unicode/additional/nsis_tauri_utils.dll')) {
    if (-not (Test-Path (Join-Path $nsis $required))) { throw "Staged NSIS toolset is missing $required" }
}
Write-Host "Staged NSIS toolset at $nsis"
