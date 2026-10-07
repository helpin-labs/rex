#!/usr/bin/env bash
# Builds the bundled embedding server and fetches its model:
#   src-tauri/binaries/llama-server-<triple>   static llama.cpp server (Metal)
#   src-tauri/resources/models/<model>.gguf    Bekko Embedding v1 a8m, Q8_0
# Both are pinned: the llama.cpp tag below and the model's SHA-256.
set -euo pipefail

LLAMA_CPP_TAG="${LLAMA_CPP_TAG:-b11430}"
# Q8_0 matches the full model's retrieval quality (see the model card) and
# is half the size of BF16. Pinned to a commit of the model repository.
MODEL_FILE="bekko-embedding-v1-a8m-Q8_0.gguf"
MODEL_URL="https://huggingface.co/hotchpotch/bekko-embedding-v1-a8m-GGUF/resolve/4acacdea60b9393ae247a6ebf5b5707938c9755b/${MODEL_FILE}"
MODEL_SHA256="0b0214214287e90ffb98ffb47e1071e5b193fb420d1d99b2ec8aab0ebb507fc0"

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
target_triple="${TARGET_TRIPLE:-$(rustc -vV | sed -n 's/^host: //p')}"
cache="$repo_root/.cache"
bin_dir="$repo_root/src-tauri/binaries"
model_dir="$repo_root/src-tauri/resources/models"
mkdir -p "$cache" "$bin_dir" "$model_dir"

# --- model ---
model="$model_dir/$MODEL_FILE"
verify() { [ -f "$1" ] && [ "$(shasum -a 256 "$1" | cut -d' ' -f1)" = "$MODEL_SHA256" ]; }
if ! verify "$model"; then
  curl -fL --retry 3 -o "$model.part" "$MODEL_URL"
  mv "$model.part" "$model"
  verify "$model" || { echo "embedding model checksum mismatch" >&2; rm -f "$model"; exit 1; }
fi

# --- llama-server ---
server="$bin_dir/llama-server-$target_triple"
stamp="$cache/llama-server-$LLAMA_CPP_TAG-$target_triple"
if [ -x "$server" ] && [ -f "$stamp" ]; then
  echo "embedder up to date ($LLAMA_CPP_TAG)"
  exit 0
fi
cmake_bin="${CMAKE:-$(command -v cmake || true)}"
if [ -z "$cmake_bin" ]; then
  echo "cmake is required (brew install cmake, or pip install cmake ninja)" >&2
  exit 1
fi
src="$cache/llama.cpp"
if [ ! -d "$src/.git" ] || [ "$(git -C "$src" describe --tags 2>/dev/null)" != "$LLAMA_CPP_TAG" ]; then
  rm -rf "$src"
  git clone --quiet --depth 1 --branch "$LLAMA_CPP_TAG" https://github.com/ggml-org/llama.cpp.git "$src"
fi
generator=()
if command -v ninja >/dev/null 2>&1; then generator=(-G Ninja); fi
"$cmake_bin" -S "$src" -B "$src/build" "${generator[@]}" \
  -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_SHARED_LIBS=OFF \
  -DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON \
  -DGGML_NATIVE=OFF \
  -DLLAMA_CURL=OFF -DLLAMA_OPENSSL=OFF \
  -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_SERVER=ON >/dev/null
"$cmake_bin" --build "$src/build" --target llama-server -j >/dev/null
cp "$src/build/bin/llama-server" "$server"
strip -x "$server" 2>/dev/null || true
touch "$stamp"
echo "embedder built: llama.cpp $LLAMA_CPP_TAG for $target_triple"
