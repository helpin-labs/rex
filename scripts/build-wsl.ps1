param([ValidateSet('amd64','arm64')][string]$Architecture = 'amd64')
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$repoRoot = Split-Path -Parent $PSScriptRoot
$destination = Join-Path $repoRoot 'src-tauri/resources/wsl'
New-Item -ItemType Directory -Force $destination | Out-Null
$root = Join-Path $env:TEMP ('rex-wsl-build-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $root | Out-Null
$distribution = 'Rex-Rootfs-Build-' + [Guid]::NewGuid().ToString('N')
$alpineArch = if ($Architecture -eq 'arm64') { 'aarch64' } else { 'x86_64' }
$checksum = if ($Architecture -eq 'arm64') { '6bf491907b705caa5fc65773fbbc1d0954530b6c455d4446f670a4c1fdcea489' } else { '412454ed98025ad9cd910d13f3d448b184e81502baa83180e89f98d0f13674be' }
$archive = Join-Path $root alpine.tar.gz
Invoke-WebRequest "https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/$alpineArch/alpine-minirootfs-3.22.2-$alpineArch.tar.gz" -OutFile $archive
if ((Get-FileHash $archive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $checksum) { throw 'WSL base rootfs checksum mismatch' }
function Invoke-WSL {
    & "$env:SystemRoot\System32\wsl.exe" @args
    if ($LASTEXITCODE -ne 0) { throw "WSL build command failed: $LASTEXITCODE" }
}
function LinuxPath([string]$Path) {
    $absolute = [IO.Path]::GetFullPath($Path)
    if ($absolute -notmatch '^[A-Za-z]:\\') { throw 'WSL build requires a local Windows drive' }
    return '/mnt/' + $absolute.Substring(0,1).ToLowerInvariant() + $absolute.Substring(2).Replace('\','/')
}
$previousGOOS, $previousGOARCH, $previousCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
$imported = $false
Push-Location $repoRoot
try {
    $env:GOOS = 'linux'; $env:GOARCH = $Architecture; $env:CGO_ENABLED = '0'
    $helper = Join-Path $root sandbox
    go build -trimpath -ldflags '-s -w' -o $helper github.com/helpin-ai/agent-runtime/cmd/agent-runtime-sandbox
    if ($LASTEXITCODE -ne 0) { throw 'Could not build the matching Linux sandbox helper' }
    [IO.File]::WriteAllText((Join-Path $root 'wsl.conf'), "[interop]`nenabled=false`nappendWindowsPath=false`n[automount]`noptions=uid=1000,gid=1000,umask=077`n[user]`ndefault=rex`n")
    Invoke-WSL --import $distribution (Join-Path $root distribution) $archive --version 2
    $imported = $true
    Invoke-WSL -d $distribution --exec /sbin/apk add --no-cache python3 py3-pip git curl ripgrep ca-certificates
    Invoke-WSL -d $distribution --exec /usr/sbin/adduser -D rex
    Invoke-WSL -d $distribution --exec /bin/mkdir -p /opt/agent-runtime
    Invoke-WSL -d $distribution --exec /bin/cp (LinuxPath $helper) /opt/agent-runtime/sandbox
    Invoke-WSL -d $distribution --exec /bin/chmod 755 /opt/agent-runtime/sandbox
    Invoke-WSL -d $distribution --exec /bin/cp (LinuxPath (Join-Path $root 'wsl.conf')) /etc/wsl.conf
    Invoke-WSL --terminate $distribution
    Invoke-WSL --export $distribution (Join-Path $destination rootfs.tar)
    @{ SHA256 = (Get-FileHash (Join-Path $destination rootfs.tar) -Algorithm SHA256).Hash.ToLowerInvariant(); Architecture = $Architecture } | ConvertTo-Json | Set-Content -Encoding ascii (Join-Path $destination rootfs.json)
} finally {
    $env:GOOS = $previousGOOS; $env:GOARCH = $previousGOARCH; $env:CGO_ENABLED = $previousCGO
    Pop-Location
    if ($imported) { Invoke-WSL --unregister $distribution }
    Remove-Item -Recurse -Force $root
}
