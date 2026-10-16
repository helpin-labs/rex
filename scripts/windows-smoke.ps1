# Exercise real Windows sidecars in an isolated profile. No real account/model keys.
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$binDir = Join-Path $repoRoot 'src-tauri/binaries'
$triple = 'x86_64-pc-windows-msvc'
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('rex-windows-smoke-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $testRoot | Out-Null
$processInfo = [Diagnostics.ProcessStartInfo]::new((Join-Path $binDir "rex-host-$triple.exe"))
$processInfo.UseShellExecute = $false
$processInfo.CreateNoWindow = $true
$processInfo.RedirectStandardOutput = $true
$processInfo.RedirectStandardError = $true
$processInfo.Environment['REX_DATA_DIR'] = Join-Path $testRoot 'data'
$processInfo.Environment['REX_WORKSPACE_ROOT'] = Join-Path $testRoot 'workspace'
$processInfo.Environment['REX_HOST_ADDR'] = '127.0.0.1:0'
$processInfo.Environment['REX_SECRETS'] = 'memory'
$processInfo.Environment['AGENT_RUNTIME_BINARY'] = Join-Path $binDir "agent-runtime-$triple.exe"
$processInfo.Environment['REX_EMBEDDER_BINARY'] = Join-Path $binDir "llama-server-$triple.exe"
$processInfo.Environment['REX_BROWSER_EXECUTABLE'] = Join-Path $repoRoot 'src-tauri/resources/browser/chrome-win64/chrome.exe'
$processInfo.Environment['REX_EMBEDDING_MODEL'] = Join-Path $repoRoot 'src-tauri/resources/models/bekko-embedding-v1-a8m-Q8_0.gguf'
$hostProcess = [Diagnostics.Process]::Start($processInfo)
$stdout = $hostProcess.StandardOutput.ReadToEndAsync()
$stderr = $hostProcess.StandardError.ReadToEndAsync()
$children = @()
try {
    $deadline = [DateTime]::UtcNow.AddMinutes(3)
    $connectionPath = Join-Path $testRoot 'data/connection.json'
    $ready = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($hostProcess.HasExited) { throw "Rex host exited during startup ($($hostProcess.ExitCode))" }
        if (Test-Path $connectionPath) {
            $connection = Get-Content -Raw $connectionPath | ConvertFrom-Json
            try {
                $status = Invoke-RestMethod -Uri ($connection.url + '/api/status') -Headers @{ Authorization = 'Bearer ' + $connection.token }
                if ($status.runtime_ready -and $status.memory.available -and $status.memory.status -eq 'ready') { $ready = $true; break }
            } catch { Write-Verbose 'Host services are still starting' }
        }
        Start-Sleep -Milliseconds 500
    }
    if (!$ready) { throw 'Runtime and bundled memory did not become ready' }
    if ($status.write_tools) { throw 'Unverified Windows command/editing capability was enabled' }
    foreach ($file in @('runtime.pid', 'embedder.pid')) {
        $idFile = Join-Path $testRoot "data/$file"
        if (!(Test-Path $idFile)) { throw "Missing sidecar PID: $file" }
        $child = Get-Process -Id ([int](Get-Content -Raw $idFile)) -ErrorAction Stop
        $children += @{ ID = $child.Id; Started = $child.StartTime }
    }
    Write-Host 'Native Windows runtime and bundled memory are ready; commands remain disabled.'
} finally {
    if (!$hostProcess.HasExited) { $hostProcess.Kill(); $hostProcess.WaitForExit(10000) | Out-Null }
    Write-Host ($stdout.GetAwaiter().GetResult())
    Write-Host ($stderr.GetAwaiter().GetResult())
    foreach ($log in @('rex.log', 'runtime.log', 'embedder.log')) {
        $logPath = Join-Path $testRoot "data/$log"
        if (Test-Path $logPath) { Get-Content -Tail 40 $logPath | Write-Host }
    }
    Start-Sleep -Seconds 2
    foreach ($child in $children) {
        $remaining = Get-Process -Id $child.ID -ErrorAction SilentlyContinue
        if ($remaining -and $remaining.StartTime -eq $child.Started) { throw "Sidecar survived host death: $($child.ID)" }
    }
    Write-Host "Smoke evidence directory: $testRoot"
}
