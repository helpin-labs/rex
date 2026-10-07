#!/usr/bin/env bash
# Builds the two Go sidecars that ship inside Rex.app:
#   rex-host      – Rex's local backend
#   agent-runtime  – the bundled execution engine (from ../agent-runtime)
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
runtime_source="${AGENT_RUNTIME_SOURCE:-$(cd "$repo_root/../agent-runtime" && pwd)}"
target_triple="${TARGET_TRIPLE:-$(rustc -vV | sed -n 's/^host: //p')}"
bin_dir="$repo_root/src-tauri/binaries"
mkdir -p "$bin_dir" "$repo_root/bin"

flags=(-trimpath -ldflags "-s -w")
(cd "$repo_root" && go build "${flags[@]}" -o "$bin_dir/rex-host-$target_triple" ./cmd/rex-host)
(cd "$runtime_source" && go build "${flags[@]}" -o "$bin_dir/agent-runtime-$target_triple" ./cmd/agent-runtime)
cp "$bin_dir/rex-host-$target_triple" "$repo_root/bin/rex-host"
"$repo_root/scripts/build-embedder.sh"
echo "sidecars built for $target_triple"
