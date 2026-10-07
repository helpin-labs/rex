#!/usr/bin/env bash
# Builds the Go sidecars that ship inside Rex.app:
#   rex-host       – Rex's local backend
#   agent-runtime  – the execution engine, github.com/helpin-ai/agent-runtime
#
# agent-runtime comes from GitHub at the version pinned in go.mod (a `tool`
# there), the same version rex-host links for memory. To build against a
# local checkout instead, create an untracked go.work:
#   go work init . ../agent-runtime
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
target_triple="${TARGET_TRIPLE:-$(rustc -vV | sed -n 's/^host: //p')}"
bin_dir="$repo_root/src-tauri/binaries"
mkdir -p "$bin_dir" "$repo_root/bin"
# agent-runtime is private: fetch it with git credentials, not the public proxy.
export GOPRIVATE="${GOPRIVATE:+$GOPRIVATE,}github.com/helpin-ai/*"

flags=(-trimpath -ldflags "-s -w")
(cd "$repo_root" && go build "${flags[@]}" -o "$bin_dir/rex-host-$target_triple" ./cmd/rex-host)
(cd "$repo_root" && go build "${flags[@]}" -o "$bin_dir/agent-runtime-$target_triple" github.com/helpin-ai/agent-runtime/cmd/agent-runtime)
if [ -f "$repo_root/go.work" ]; then
  echo "agent-runtime: local checkout from go.work"
else
  echo "agent-runtime: $(cd "$repo_root" && go list -m -f '{{.Version}}' github.com/helpin-ai/agent-runtime) from GitHub"
fi
cp "$bin_dir/rex-host-$target_triple" "$repo_root/bin/rex-host"
"$repo_root/scripts/build-embedder.sh"
echo "sidecars built for $target_triple"
