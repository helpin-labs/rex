$ErrorActionPreference = 'Stop'
foreach ($name in @('build-wsl.ps1', 'windows-host-checks.ps1')) {
    $parseErrors = $null
    [System.Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $name), [ref]$null, [ref]$parseErrors) | Out-Null
    if ($parseErrors) { throw ($parseErrors | Out-String) }
}
$originalTemp = $env:TEMP
$root = Join-Path $originalTemp ('rex-wsl-cache-fixture-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory "$root/scripts", "$root/src-tauri/resources/wsl" -Force | Out-Null
try {
    $env:TEMP = $root
    Copy-Item "$PSScriptRoot/build-wsl.ps1" "$root/scripts/build-wsl.ps1"
    Set-Content "$root/go.mod" 'module cache-fixture'
    Set-Content "$root/go.sum" 'fixture dependency'
    $fingerprint = (@('go.mod', 'go.sum', 'scripts/build-wsl.ps1') | ForEach-Object { (Get-FileHash (Join-Path $root $_) -Algorithm SHA256).Hash.ToLowerInvariant() }) -join ':'
    $bundle = "$root/src-tauri/resources/wsl/rootfs.tar"
    $metadata = "$root/src-tauri/resources/wsl/rootfs.json"
    [IO.File]::WriteAllText($bundle, 'cache fixture bytes')
    @{ BuildInputs = $fingerprint; Architecture = 'amd64'; SHA256 = (Get-FileHash $bundle -Algorithm SHA256).Hash.ToLowerInvariant() } | ConvertTo-Json | Set-Content $metadata
    $result = (& "$root/scripts/build-wsl.ps1" 6>&1 | Out-String)
    if ($result -notmatch 'Verified cached WSL image') { throw 'Matching cache was not reused' }
    function Invoke-WebRequest { throw 'CACHE_MISS_EXPECTED' }
    foreach ($scenario in @('tampered image', 'different architecture', 'malformed metadata', 'changed dependency')) {
        [IO.File]::WriteAllText($bundle, 'cache fixture bytes')
        @{ BuildInputs = $fingerprint; Architecture = 'amd64'; SHA256 = (Get-FileHash $bundle -Algorithm SHA256).Hash.ToLowerInvariant() } | ConvertTo-Json | Set-Content $metadata
        if ($scenario -eq 'changed dependency') { Set-Content "$root/go.mod" 'module updated-fixture' }
        if ($scenario -eq 'tampered image') { [IO.File]::AppendAllText($bundle, 'modified') }
        if ($scenario -eq 'different architecture') {
            @{ BuildInputs = $fingerprint; Architecture = 'arm64'; SHA256 = (Get-FileHash $bundle -Algorithm SHA256).Hash.ToLowerInvariant() } | ConvertTo-Json | Set-Content $metadata
        }
        if ($scenario -eq 'malformed metadata') { Set-Content $metadata '{bad json' }
        $rejected = $false
        try { & "$root/scripts/build-wsl.ps1" } catch { $rejected = $_ -match 'CACHE_MISS_EXPECTED' }
        if (!$rejected) { throw "Invalid cache was accepted: $scenario" }
    }
    Write-Host 'PASS: PowerShell syntax; valid WSL cache reused; tampered, wrong-architecture, malformed and outdated caches rebuilt.'
} finally {
    $env:TEMP = $originalTemp
    Remove-Item $root -Recurse -Force
}
