param([string]$BinaryPath = 'src-tauri/binaries/rex-host-x86_64-pc-windows-msvc.exe')
$ErrorActionPreference = 'Stop'
if ($env:REX_WINDOWS_SIGNING_THUMBPRINT) { throw 'Run the signing smoke test without an existing signing identity' }
$binary = (Resolve-Path -LiteralPath $BinaryPath).Path
$root = Join-Path $env:TEMP ('rex signing ' + [guid]::NewGuid().ToString('N'))
$githubEnv = $env:GITHUB_ENV
$thumbprint = $null
New-Item -ItemType Directory $root | Out-Null
Push-Location $root
try {
    # Keep the fixture identity out of subsequent workflow steps.
    $env:GITHUB_ENV = $null
    Copy-Item "$PSScriptRoot/windows-sign.ps1", "$PSScriptRoot/windows-preview-signing.ps1" .
    ./windows-preview-signing.ps1
    $thumbprint = $env:REX_WINDOWS_SIGNING_THUMBPRINT
    $public = [Security.Cryptography.X509Certificates.X509Certificate2]::new((Join-Path $root 'release-assets/preview.cer'))
    if ($public.HasPrivateKey) { throw 'Exported a private signing key' }
    $target = Join-Path $root 'desktop fixture.exe'
    Copy-Item -LiteralPath $binary -Destination $target
    $hook = (Get-Content signing-config.json -Raw | ConvertFrom-Json).bundle.windows.signCommand
    # Match Tauri: inherit the environment and capture both output streams.
    $info = [Diagnostics.ProcessStartInfo]::new()
    $info.FileName = $hook.cmd
    $info.UseShellExecute = $false
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    foreach ($argument in $hook.args) {
        $info.ArgumentList.Add($(if ($argument -eq '%1') { $target } else { $argument }))
    }
    $child = [Diagnostics.Process]::Start($info)
    $stdout = $child.StandardOutput.ReadToEndAsync()
    $stderr = $child.StandardError.ReadToEndAsync()
    $child.WaitForExit()
    if ($child.ExitCode -ne 0) { throw "Signing hook failed: $($stdout.Result) $($stderr.Result)" }
    $signature = Get-AuthenticodeSignature -LiteralPath $target
    if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $thumbprint) { throw 'Invalid fixture signature' }
    Write-Host 'PASS: captured signing hook, paths with spaces, expected signer, public-only certificate export.'
} finally {
    try {
        if ($thumbprint) {
            $env:REX_WINDOWS_SIGNING_THUMBPRINT = $thumbprint
            ./windows-preview-signing.ps1 -Remove
            foreach ($store in @('LocalMachine\Root', 'CurrentUser\My')) {
                if (Test-Path "Cert:\$store\$thumbprint") { throw 'Signing certificate cleanup failed' }
            }
        }
    } finally {
        $env:GITHUB_ENV = $githubEnv
        $env:REX_WINDOWS_SIGNING_THUMBPRINT = $null
        Pop-Location
        Remove-Item $root -Recurse -Force
    }
}
