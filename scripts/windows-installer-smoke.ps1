# Run only in an isolated Windows CI account. Preserve user data through reinstall/uninstall.
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$installers = @(Get-ChildItem "$repoRoot/src-tauri/target/release/bundle/nsis/*.exe")
if ($installers.Count -ne 1) { throw 'Expected one built NSIS installer' }
$directory = Join-Path $env:LOCALAPPDATA ('Rex-Installer-Test-' + [Guid]::NewGuid().ToString('N'))
$dataDirectory = Join-Path $env:LOCALAPPDATA 'ai.rex.desktop'
if (Test-Path $dataDirectory) { throw 'Installer smoke requires a clean test account' }
New-Item -ItemType Directory $dataDirectory | Out-Null
$sentinel = Join-Path $dataDirectory 'installer-retention-test.txt'
[IO.File]::WriteAllText($sentinel, 'preserve-this-data')
function Invoke-Installer([string]$executable, [string]$arguments) {
    $process = Start-Process -FilePath $executable -ArgumentList $arguments -PassThru
    if (!$process.WaitForExit(300000)) { $process.Kill(); throw 'Installer timed out' }
    if ($process.ExitCode -ne 0) { throw "Installer exit code $($process.ExitCode)" }
}
try {
    Invoke-Installer $installers[0].FullName "/S /D=$directory"
    foreach ($name in @('rex-desktop.exe', 'rex-host.exe', 'agent-runtime.exe', 'llama-server.exe', 'uninstall.exe', 'resources/wsl/rootfs.json', 'resources/browser/agent-browser.exe')) {
        if (!(Test-Path (Join-Path $directory $name))) { throw "Installed resource missing: $name" }
    }
    & "$PSScriptRoot/windows-smoke.ps1" -InstallDirectory $directory
    & "$PSScriptRoot/windows-desktop-smoke.ps1" -InstallDirectory $directory
    Invoke-Installer $installers[0].FullName "/S /D=$directory"
    if ((Get-Content -Raw $sentinel) -ne 'preserve-this-data') { throw 'Reinstall changed app data' }
    & "$PSScriptRoot/windows-smoke.ps1" -InstallDirectory $directory
    Invoke-Installer (Join-Path $directory 'uninstall.exe') '/S'
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    while ((Test-Path (Join-Path $directory 'rex-desktop.exe')) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 250 }
    if (Test-Path (Join-Path $directory 'rex-desktop.exe')) { throw 'Uninstall left the app executable' }
    if ((Get-Content -Raw $sentinel) -ne 'preserve-this-data') { throw 'Default uninstall removed app data' }
    Write-Host 'Install, reinstall, bundled service readiness and default uninstall data retention passed.'
} finally {
    # Preserve the original test exception and evidence if a dying WebView still holds a file.
    foreach ($path in @($dataDirectory, $directory)) {
        try {
            if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Recurse -Force -ErrorAction Stop }
        } catch { Write-Warning "Installer smoke cleanup: $($_.Exception.Message)" }
    }
}
