param([switch]$Remove)
$ErrorActionPreference = 'Stop'
if ($Remove) {
    $thumbprint = $env:REX_WINDOWS_SIGNING_THUMBPRINT
    if ($thumbprint -and $thumbprint -match '^[0-9A-Fa-f]{40}$') {
        foreach ($store in @('LocalMachine\Root', 'CurrentUser\My')) {
            $path = "Cert:\$store\$thumbprint"
            if (Test-Path $path) {
                if ($store -eq 'CurrentUser\My') { Remove-Item $path -DeleteKey -Force } else { Remove-Item $path -Force }
            }
        }
    }
    return
}
if ($env:REX_WINDOWS_SIGNING_THUMBPRINT) { throw 'A preview signing identity is already configured' }
$certificate = New-SelfSignedCertificate -Type CodeSigningCert -Subject 'CN=Rex Development Preview' -CertStoreLocation Cert:\CurrentUser\My -HashAlgorithm SHA256 -KeyAlgorithm RSA -KeyLength 3072 -KeyExportPolicy NonExportable -NotAfter (Get-Date).AddYears(1)
$env:REX_WINDOWS_SIGNING_THUMBPRINT = $certificate.Thumbprint
try {
    # Machine trust avoids the interactive current-user root prompt on disposable elevated CI runners.
    $store = [Security.Cryptography.X509Certificates.X509Store]::new('Root', 'LocalMachine')
    try { $store.Open('ReadWrite'); $store.Add($certificate) } finally { $store.Close() }
    New-Item -ItemType Directory -Force release-assets | Out-Null
    Export-Certificate -Cert $certificate -FilePath release-assets/preview.cer | Out-Null
    # Use the same PowerShell 7 host and modules that created the certificate.
    $command = @{ cmd = (Join-Path $PSHOME 'pwsh.exe'); args = @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', (Join-Path $PSScriptRoot 'windows-sign.ps1'), '%1') }
    @{ bundle = @{ windows = @{ signCommand = $command } } } | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 signing-config.json
    if ($env:GITHUB_ENV) { "REX_WINDOWS_SIGNING_THUMBPRINT=$($certificate.Thumbprint)" | Out-File $env:GITHUB_ENV -Append -Encoding utf8 }
    Write-Host "Created development signing certificate $($certificate.Thumbprint). Only its public certificate is exported."
} catch {
    & $PSCommandPath -Remove
    throw
}
