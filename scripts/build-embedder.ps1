# Native Windows CPU embedder. Keep model identity/checksum in sync with build-embedder.sh.
param([ValidateSet('amd64','arm64')][string]$Architecture = 'amd64')
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$PSNativeCommandUseErrorActionPreference = $true
$repoRoot = Split-Path -Parent $PSScriptRoot
$llamaTag = 'b11430'
$modelFile = 'bekko-embedding-v1-a8m-Q8_0.gguf'
$modelHash = '0b0214214287e90ffb98ffb47e1071e5b193fb420d1d99b2ec8aab0ebb507fc0'
$modelUrl = "https://huggingface.co/hotchpotch/bekko-embedding-v1-a8m-GGUF/resolve/4acacdea60b9393ae247a6ebf5b5707938c9755b/$modelFile"
$triple = if ($Architecture -eq 'arm64') { 'aarch64-pc-windows-msvc' } else { 'x86_64-pc-windows-msvc' }
$platform = if ($Architecture -eq 'arm64') { 'ARM64' } else { 'x64' }
$binDir = Join-Path $repoRoot 'src-tauri/binaries'
$modelDir = Join-Path $repoRoot 'src-tauri/resources/models'
$cache = Join-Path $repoRoot '.cache'
New-Item -ItemType Directory -Force $binDir, $modelDir, $cache | Out-Null
$model = Join-Path $modelDir $modelFile
if (!(Test-Path $model) -or (Get-FileHash $model -Algorithm SHA256).Hash.ToLowerInvariant() -ne $modelHash) {
    Invoke-WebRequest -Uri $modelUrl -OutFile "$model.part" -TimeoutSec 300
    if ((Get-FileHash "$model.part" -Algorithm SHA256).Hash.ToLowerInvariant() -ne $modelHash) {
        Remove-Item "$model.part"
        throw 'Embedding model checksum mismatch'
    }
    Move-Item -Force "$model.part" $model
}
$server = Join-Path $binDir "llama-server-$triple.exe"
$stamp = Join-Path $cache "llama-server-$llamaTag-$triple-cpu"
if ((Test-Path $server) -and (Test-Path $stamp)) { Write-Host 'Windows embedder is up to date'; exit 0 }
$source = Join-Path $cache "llama.cpp-windows-$llamaTag"
if (!(Test-Path $source)) { git clone --depth 1 --branch $llamaTag https://github.com/ggml-org/llama.cpp.git $source }
$tag = git -C $source describe --tags --exact-match
if ($tag -ne $llamaTag) { throw 'Unexpected llama.cpp source version' }
$build = Join-Path $cache "llama-build-$llamaTag-$triple"
cmake -S $source -B $build -A $platform `
    -DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreaded `
    -DBUILD_SHARED_LIBS=OFF -DGGML_BACKEND_DL=OFF `
    -DGGML_METAL=OFF -DGGML_VULKAN=OFF -DGGML_CUDA=OFF `
    -DGGML_NATIVE=OFF -DGGML_OPENMP=OFF `
    -DLLAMA_CURL=OFF -DLLAMA_OPENSSL=OFF `
    -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_SERVER=ON
cmake --build $build --config Release --target llama-server --parallel 4
Copy-Item (Join-Path $build 'bin/Release/llama-server.exe') $server
New-Item -ItemType File -Force $stamp | Out-Null
Write-Host "Built CPU embedder for $triple"
