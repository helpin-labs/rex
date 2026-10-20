#!/usr/bin/env bash
# Bundle the same pinned Chrome and standalone CLI used by Windows.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
target_triple="${TARGET_TRIPLE:-$(rustc -vV | sed -n 's/^host: //p')}"
if [ "$target_triple" != aarch64-apple-darwin ]; then
  echo "A macOS browser bundle has only been verified for Apple Silicon" >&2
  exit 1
fi
cache="$repo_root/.cache"
destination="$repo_root/src-tauri/resources/browser"
mkdir -p "$cache" "$destination"

fetch() {
  local archive="$cache/$1" expected="$2" url="$3"
  if [ ! -f "$archive" ] || [ "$(shasum -a 256 "$archive" | cut -d' ' -f1)" != "$expected" ]; then
    curl -fL --retry 3 -o "$archive.part" "$url"
    if [ "$(shasum -a 256 "$archive.part" | cut -d' ' -f1)" != "$expected" ]; then
      echo "Browser archive checksum mismatch: $1" >&2
      rm -f "$archive.part"
      exit 1
    fi
    mv "$archive.part" "$archive"
  fi
}
fetch chrome-149.0.7827.115-mac-arm64.zip \
  562af577e8a8ae69f91bba8440a6806f4e54c2e8d4caa384b610b55007386d79 \
  https://storage.googleapis.com/chrome-for-testing-public/149.0.7827.115/mac-arm64/chrome-mac-arm64.zip
fetch agent-browser-0.25.5.tgz \
  12126bd9b6befdd422474e5fc85372326444f18de96dac12fb21d3acbb4a7b37 \
  https://registry.npmjs.org/agent-browser/-/agent-browser-0.25.5.tgz

extracted="$(mktemp -d "$cache/browser.XXXXXX")"
trap 'rm -rf "$extracted"' EXIT
unzip -q "$cache/chrome-149.0.7827.115-mac-arm64.zip" -d "$extracted"
tar -xzf "$cache/agent-browser-0.25.5.tgz" -C "$extracted" package/bin/agent-browser-darwin-arm64 package/LICENSE
rm -rf "$destination/chrome-mac-arm64"
mv "$extracted/chrome-mac-arm64" "$destination/"
cp "$extracted/package/bin/agent-browser-darwin-arm64" "$destination/agent-browser"
cp "$extracted/package/LICENSE" "$destination/agent-browser-LICENSE"
chmod +x "$destination/agent-browser"
test -x "$destination/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"
# Chrome for Testing ships linker signatures; seal its nested app for distribution.
codesign --force --deep --sign - "$destination/chrome-mac-arm64/Google Chrome for Testing.app"
codesign --force --sign - "$destination/agent-browser"
codesign --verify --deep --strict "$destination/chrome-mac-arm64/Google Chrome for Testing.app"
test "$("$destination/agent-browser" --version)" = 'agent-browser 0.25.5'
echo "browser bundle ready (Chrome 149.0.7827.115, agent-browser 0.25.5)"
