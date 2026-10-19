param([Parameter(Mandatory)][string]$Path)
$ErrorActionPreference = 'Stop'
if ($env:REX_WINDOWS_SIGNING_THUMBPRINT -notmatch '^[0-9A-Fa-f]{40}$') { throw 'Missing preview signing certificate' }
$certificate = Get-Item "Cert:\CurrentUser\My\$env:REX_WINDOWS_SIGNING_THUMBPRINT"
if (!$certificate.HasPrivateKey) { throw 'Signing certificate has no private key' }
$signature = Set-AuthenticodeSignature -LiteralPath $Path -Certificate $certificate -HashAlgorithm SHA256
if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $certificate.Thumbprint) {
    throw "Authenticode signing failed for ${Path}: $($signature.Status)"
}
Write-Host "Development-signed $(Split-Path -Leaf $Path)"
