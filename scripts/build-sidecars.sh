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
# Rex's own Google client, for Gmail and Google Sheets. Without one, people
# add their own in Apps.
host_ldflags="-s -w"
if [ -n "${REX_GOOGLE_CLIENT_ID:-}" ]; then
  pkg=github.com/helpin-ai/rex/internal/rex
  host_ldflags+=" -X $pkg.googleClientID=$REX_GOOGLE_CLIENT_ID -X $pkg.googleClientSecret=${REX_GOOGLE_CLIENT_SECRET:-}"
fi
(cd "$repo_root" && go build -trimpath -ldflags "$host_ldflags" -o "$bin_dir/rex-host-$target_triple" ./cmd/rex-host)
(cd "$repo_root" && go build "${flags[@]}" -o "$bin_dir/agent-runtime-$target_triple" github.com/helpin-ai/agent-runtime/cmd/agent-runtime)
echo "agent-runtime: $(cd "$repo_root" && go list -m -f '{{if .Main}}local checkout from go.work{{else}}{{.Version}} from GitHub{{end}}' github.com/helpin-ai/agent-runtime)"
cp "$bin_dir/rex-host-$target_triple" "$repo_root/bin/rex-host"
"$repo_root/scripts/build-embedder.sh"
echo "sidecars built for $target_triple"
