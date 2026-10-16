# Windows development preview

Windows 11 x64 is the initial desktop target. The branch builds an unsigned,
per-user NSIS installer. It is not yet a signed public Windows release. Local VM acceptance checks passed;
the hosted CI WebView2 GUI gate remains unresolved. See the verification ledger
before treating any installer as release-ready.
ARM64 native host and WSL tests also run in the supplied VM; an ARM64 desktop
installer and Windows 10 remain separate verification gates.

## Install and first run

Use the `rex-windows-x64-unsigned-development` artifact from the Windows workflow.
Extract and run the setup executable. Unsigned development builds can trigger
Windows SmartScreen. WebView2 is installed by its Microsoft bootstrapper if absent.
The installer bundles the runtime, CPU embedding server/model, browser/CLI, and
Linux command environment; users do not need Go, Node, Python, or a compiler.
A model server, provider key, or ChatGPT sign-in is still required for generation.

Local app data is under `%LOCALAPPDATA%\ai.rex.desktop`; workspaces default to
`%USERPROFILE%\Rex`. Credentials use Windows Credential Manager. Large OAuth
records are split into verified, atomically replaced credential chunks.

Chat, memory, MCP and routines work without WSL. File changes, commands and Python
remain disabled until Settings → command sandbox setup completes its confinement
probe. On a machine without WSL2, open an administrator terminal, run
`wsl --install --no-distribution`, and restart if Windows requests it. Managed
machines may need administrator approval. Setup imports a private Rex distribution;
it does not replace the default or modify unrelated distributions.

The sandbox disables Windows executable interop and Windows PATH inheritance.
Landlock confines command access to the bot's folder and required runtime paths.
A failed probe disables commands instead of falling back to native shell access.
Use Settings → repair to stage and verify a replacement before switching to it.

## Updates and removal

Reinstalling preserves local data, credentials and the owned command environment.
Uninstall keeps app data by default. If app-data deletion is selected, Rex asks
separately about its Linux environment and caches. Removal validates both the
recorded path and a random ownership marker before unregistering a distribution.
A failed ownership check preserves app data. Other WSL distributions are kept.
Credential Manager entries and user workspace folders are not deleted by NSIS.

## Build and verify

On Windows with Go, Rust/MSVC, Node/pnpm, CMake and WSL2 installed:

```powershell
pnpm install --frozen-lockfile
./scripts/build-sidecars.ps1
pnpm tauri build --config src-tauri/tauri.windows.conf.json
```

`go.mod` pins the verified runtime commit as a Go pseudo-version; normal builds
and CI consume that same code without a local workspace override. Replace the
preview pin with the corresponding release tag after the runtime branch merges.

The workflow runs native host/credential/process tests, Chrome and Edge tests,
bundled CPU memory readiness, WSL setup/repair/removal, then builds and installs
the actual package. Installer checks use a disposable CI account and verify
reinstall/uninstall retention. Do not run that check against personal app data.
`scripts/e2e.py` runs actual model-driven conversations, approvals, memory, MCP
and routines with a separate data directory. See the
[implementation ledger](PLAN-windows-implementation.md) for verified results and
remaining release gates. Desktop and Credential Manager checks require a
signed-in Windows desktop session; an SSH network logon can be rejected by
Credential Manager. Backend fixtures use their own test data and secrets.

## Release signing

Tauri's Windows bundle supports `certificateThumbprint`, `digestAlgorithm`,
`timestampUrl` and `tsp` (or a CI-specific `signCommand`) under `bundle.windows`.
Supply the release certificate through the release runner's protected certificate
store and an additional private Tauri config. Do not commit certificate material
or passwords. The development workflow intentionally does not claim a signed
artifact. Verify Authenticode and a clean-machine install before publishing.
