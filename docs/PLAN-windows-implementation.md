# Windows implementation and verification plan

Status: in progress. Created 2026-10-16. No Windows release is verified yet.

## Goal and boundaries

Ship a native Windows Rex desktop app with chat, credentials, memory, web,
browsers, files, routines, MCP, and restart recovery. Commands run only through
an isolated Rex-owned WSL2 distribution after a confinement probe succeeds.
Without WSL, the app remains usable and command execution stays unavailable.
Keep macOS and Linux behavior working. Target Windows x64 first; ARM64 is a
separate gate, not implied by an x64 build. Windows 10 support remains contingent
on passing the same WebView2 and WSL checks as Windows 11.

agent-runtime owns portable locking/file access, command dispatch and isolation,
browser execution, durable memory jobs, and capability reporting. Rex owns
Windows service supervision, local credentials/storage, WSL installation and
repair policy, UI, native notifications, and packaging. The Go SDK owns typed
shared API contracts; extend it only when the runtime API requires a new contract.
Do not duplicate these responsibilities in Rex or change other apps' defaults.

## Baseline evidence

- Windows PRD: docs/PRD-windows-wsl2.md, draft dated 2026-10-06.
- Rex host Windows amd64 cross-build fails on Unix process groups/signals.
- Published runtime v0.13.14 Windows amd64 cross-build fails on repository-cache
  Unix open/locking and memory job flock calls.
- Browser discovery, reveal actions, executable names and environments assume macOS.
- Embedding build enables Metal and has no Windows artifact path.
- Notifications are intentionally a no-op outside macOS.
- Installer targets are app/dmg; no Windows build/test workflow exists in Rex.
- The existing keyring dependency supports Windows but limits a secret to 2,560 bytes.

## Work sequence and acceptance gates

### 1. Platform-neutral runtime build

- Extract existing Unix file locking and safe lock-file opening behind platform
  files. Implement Windows shared/exclusive locks and reparse-point protection.
- Reuse that implementation for repository-cache and memory-worker ownership.
- Inventory remaining unguarded Unix calls and split process, filesystem, and
  command-specific code by platform. Do not weaken containment or ownership checks.
- Gate: runtime builds for Windows amd64/arm64; lock contention and process-exit
  release, shared-reader/exclusive-writer behavior, symlink/reparse rejection,
  memory queue durability, and all touched Unix tests pass.

### 2. Native Rex startup and shutdown

- Centralize platform-dependent sidecar filenames, target triples, environment
  construction, and application-data locations. Use .exe and Windows path rules.
- Keep Unix process groups on macOS/Linux. Use Windows Job objects to own the
  runtime and embedder process trees, including cancellation and parent death.
- Identify an orphan by executable identity before terminating it; never kill an
  unrelated reused PID. Remove syscall.Exec assumptions from browser forwarding.
- Gate: host cross-builds for amd64/arm64; macOS tests remain green. On Windows,
  start/quit/crash/restart leave no orphan runtime/embedder and recover checkpoints.

### 3. Windows core app without WSL

- Configure native per-user data/workspace directories and Explorer reveal.
- Keep the Secrets interface. Support large OAuth payloads securely using the
  Windows credential store with atomic replacement and deletion; handle legacy
  small records. Avoid plaintext files or secrets in the UI/logs.
- Build/package a Windows llama-server using CPU as the verified baseline and
  the same pinned embedding model/checksum. GPU acceleration is not required.
- Gate: hosted/local chat, streaming, cancellation, credential refresh/restart,
  large-token storage/deletion, memory extract/recall/consolidate/forget, schedules,
  and MCP work without WSL. Command/editing capabilities fail closed.

### 4. Native Windows browsers

- Add Chrome/Edge/Brave/Vivaldi discovery and executable selection on Windows.
- Add runtime Chrome for Testing discovery and Windows process cleanup. Keep
  browser lifecycle execution in runtime; Rex supplies product policy/settings.
- Verify per-bot profiles, takeover/handback, screenshots/live view, idle release,
  locked-profile recovery, extension connectivity, and restart.
- Gate: real Edge and Chrome tests; unrelated user browsers remain untouched.

### 5. WSL/Landlock proof before enabling commands

- On Windows 11, verify the actual WSL kernel exposes supported Landlock and
  confinement applies to the chosen workspace filesystem. Do not assume drvfs
  or /mnt/c is safe. If necessary use ext4 workspaces exposed through Explorer.
- Build a minimal dedicated rootfs with pinned inputs and a Linux helper.
  Disable Windows interop and appended Windows PATH in that distribution.
- Gate: allowed workspace read/write succeeds; outside writes, credential reads,
  reparse/symlink escapes and launching Windows executables are denied. Missing
  WSL, unsupported kernel, or failed probes leave commands disabled.
- Record the tested kernel, filesystem, architecture, and commands in this file.

### 6. Runtime WSL command execution and Rex setup

- Add opt-in wsl isolation configuration, startup probe and capability reporting.
- Translate Windows/WSL workspace/cache paths without shell interpolation. Route
  commands/Python through the Rex distribution, preserving stdin/output/status.
- Cancellation/timeout must terminate the Linux command tree, not just wsl.exe.
- Rex detects WSL availability/policy, offers setup/import/repair, and never
  changes a user's default or unrelated distribution. Handle admin/reboot needs
  explicitly. Keep caches/venvs inside the distribution where appropriate.
- Extend SDK contracts if needed, then consume them from Rex.
- Gate: real Python/venv/file tasks, approvals, auto-review, cancellation, timeout,
  crash/restart, and unavailable/disabled WSL cases pass on Windows.

### 7. Windows desktop integration and installation

- Add Windows notifications with click-through and taskbar/tray attention states.
- Configure NSIS per-user installer, WebView2 bootstrap, icons, sidecar/resources,
  app-data permissions, signing support, and install/upgrade/uninstall behavior.
- Keep the existing macOS bundle and notification code intact.
- Gate: installer works on a clean machine; large-token authentication and bundled
  memory work without developer tools; upgrade preserves data; uninstall leaves
  no owned processes and asks before deleting user data or the Rex distribution.

### 8. CI, release and verification evidence

- Add Windows-hosted build/test jobs for both repositories and cross-platform
  compilation to catch regressions early. Private Go modules need a read-only
  repository credential; do not print it or put it in artifacts.
- Run Go tests/vet and applicable race tests, React tests/typecheck, Rust checks,
  sidecar builds, installer build and smoke checks. Distinguish tests skipped by
  platform from tests that actually ran. Record failures rather than suppressing.
- Verify WSL and GUI behavior on a Windows machine/VM capable of those features;
  GitHub-hosted compilation alone does not satisfy those gates.
- Keep changes on feature branches, version the shared runtime/SDK first when
  required, then pin released versions in Rex. Test before publishing installers.

## Completion evidence ledger

| Gate | Status | Evidence |
| --- | --- | --- |
| Plan saved before implementation | complete | This file, initial plan commit |
| Runtime Windows amd64 build | native build passed | [Windows run 38870509025](https://github.com/helpin-ai/agent-runtime/actions/runs/38870509025): locks, snapshot ACLs, SQLite URIs, memory jobs and banks |
| Runtime Windows arm64 build | cross-build passed on Windows runner | run 38870509025 |
| Rex Windows amd64 build | native build and full host suite passed | Rex run 38873145341, host job |
| Rex Windows arm64 build | cross-build passed on Windows runner | Rex run 38873145341; native ARM64 execution not verified |
| Windows native core/memory/credentials | core tests passed; bundled service startup pending | Runtime run 38873202939; Rex run 38872456628 credential/ACL/job checks |
| Windows browser integration | native Chrome E2E and Chrome/Edge pipes passed; installer/browser cleanup gates remain | Runtime run 38872748373; Rex run 38872456628 |
| WSL confinement proof | passed on tested WSL2 kernel, ext4 and Windows-mounted drive | [run 38874224638](https://github.com/helpin-ai/agent-runtime/actions/runs/38874224638), kernel 6.18.40.1, ABI 7 |
| WSL setup/commands/cancel/restart | pending | |
| Installer/signing/upgrade/uninstall | pending | |
| Windows notifications and attention | pending | |
| macOS regression checks | foundation passed | Rex full Go tests/vet; runtime workspace/memory/runtime/API/cancellation tests; lock/memory race tests |
| Linux regression checks | pending | |

## Stop conditions and verification rules

Do not enable unconfined commands as a fallback. Do not call a cross-build a
Windows runtime test, and do not mark a platform verified from mocked tests alone.
If a Windows runner, VM, signing credential, or private-module credential is
missing, record the exact blocked gate and continue independent implementation.
A release is complete only when all mandatory gates have evidence; document
optional architecture/OS support separately when its gates remain unverified.

## Implementation log

- Runtime branch `feat/windows-support`: 4e6ec74 ports locks/process cancellation
  and adds native Windows CI. 684ae52 fixes URI formatting exposed by the first
  real Windows memory job tests, also covering memory banks/checkpoints.
- Rex process ownership now uses a kill-on-close Windows Job object; Unix process
  groups remain. Windows commands remain disabled (`execution_isolation=none`).
- Native Windows CI includes host-death descendant cleanup, core tests, and both
  host architecture builds. Cross-repository runtime input is pinned by full SHA
  until a runtime release contains these changes.
- Additional browser gap found: legacy takeover uses `exec.Cmd.ExtraFiles` for
  CDP pipes; Windows needs a verified transport before that mode can be enabled.
- GitHub connector can read workflow results but rejected draft PR creation with
  HTTP 403. Git pushes and branch-triggered Windows CI work.

- Runtime native CI caught and fixed Windows SQLite URI formatting and snapshot
  confidentiality. Snapshot files now use an owner-only protected Windows ACL.
- The first full Windows runtime/API test run (38871162496) exposed approval
  evidence path formatting and credential-redaction fixture issues; under investigation.
- Rex now uses LocalAppData and an owner-only inheritable NTFS ACL. Large Windows
  credentials use checksummed, generation-based Credential Manager parts, published
  atomically; failure and deletion tests pass locally. Native ACL/keyring tests added.
- Windows CPU embedding/sidecar build scripts and a development NSIS configuration
  are implemented. CI will start real sidecars, verify memory readiness and command
  disablement, kill the host and check child cleanup, then build an unsigned installer.
  These scripts and installer are not yet verified on a Windows runner.

- Runtime approval fixture failures were caused by the Windows runner's inherited
  PostgreSQL password matching ordinary fixture narrative, not a weakened guard.
  Tests now isolate that fixture variable; production redaction stays unchanged.
- Rex memory readers and browser history now use escaped SQLite file URIs with
  Windows drive handling. Tests cover spaces, hashes and percent signs.
- Windows browser discovery and PE version metadata are implemented. Plain takeover
  now uses Chromium's inherited Windows HANDLE transport. The shared pipe handshake
  passed against a real macOS browser; native Chrome and Edge CI checks are queued.
- Windows notifications use Tauri's maintained WinRT toast library with activation
  routed to the existing conversation event. Taskbar attention/overlay and a tray
  tooltip carry attention state. Interactive notification delivery remains unverified.
- Regression checks passed: full Rex Go tests/vet, 68 frontend tests, production UI
  build, Rust macOS check, and workflow validation. All Windows GUI/installer/WSL
  acceptance gates remain pending their native evidence.

- Native Rex CI run 38872456628 passed job ownership, owner-only directory ACLs,
  real large-token Credential Manager roundtrips, and pipe control against both
  Chrome for Testing and installed Edge. Remaining host failures were Unix path
  fixtures and the legacy migration LIKE prefix, whose Windows separator was
  incorrectly treated as an escape; fixed without disabling the migration test.
- Runtime native Chrome extension E2E passed in run 38872748373, covering page
  actions, screenshots, takeover/handback, stale references, persisted cookies and
  runtime browser restart. This verifies Chrome, not all Chromium-family browsers.
- A separate disposable WSL2 proof imports a checksum-pinned minimal rootfs, turns
  interop off, and tests ABI >= 3, ext4/drvfs writes, outside credential reads/writes,
  symlink escapes and Windows executable denial. It never enables app commands.
- Private Rex workflow reads now work via Git's existing noninteractive GitHub
  authentication; connector permission is no longer blocking native CI review.

- Runtime Windows run 38873202939 is green for the expanded suite, including
  native API/model-loop regressions, memory jobs/banks, ACLs, command descendant
  cancellation, Chrome extension E2E, and x64/ARM64 builds.
- The disposable WSL2 import succeeded on a hosted Windows runner. Its first
  test invocation failed on PowerShell argument parsing, before confinement ran;
  quoted test flags fix that harness issue. The confinement rerun is pending.
- The Windows installer now includes checksum-pinned Chrome for Testing, passed
  to the runtime by the desktop shell. Extension mode needs no external Node or
  agent-browser install. Legacy CDP mode's separate CLI dependency remains a gate.
- Windows-facing copy and bot instructions now use platform-neutral wording.
  Commands continue to fail closed with a verified-sandbox message.

- WSL proof run 38874224638 passed on kernel `6.18.40.1-microsoft-standard-WSL2`,
  Landlock ABI 7. Filesystems: ext4 `0xef53` and Windows-mounted drive `0x1021997`.
  Both allowed workspace writes and denied outside reads/writes, symlink escapes,
  and Windows executable launches. Distribution-local interop was functionally
  tested; a global binfmt registration is not evidence that this distro permits it.
- Full native Rex host tests and both architecture builds passed in run 38873145341.
- The first real host/runtime startup caught the runtime store's CGO-only driver.
  Windows now selects the already-installed pure Go SQLite GORM driver; Unix keeps
  its existing driver. Rex CI pins the fix while live startup is reverified.
- File reads/uploads now use `os.Root`; native junction-escape tests are queued.
  Windows browser discovery also checks registered App Paths for custom installs.
- Phase 6 implementation reuses the runtime's Linux workspace handlers for command
  validation, Python environments, output limits and confinement. The Windows
  bridge passes execution scope over stdin, holds a cancellation pipe, and leaves
  artifact publishing and credentials on the native runtime side. No commands
  will be enabled until the bridge, cancellation and startup probe pass WSL CI.

- Rex run 38876174569 passed the full x64 host suite and real host/runtime plus
  CPU embedder readiness, memory readiness and host-death child cleanup. Its
  unsigned installer build exposed a macOS-only RunEvent::Reopen match arm; the
  arm is now platform-gated and the native installer rerun is pending.
- The supplied Windows 11 ARM64 VM is reachable over SSH. Disposable WSL tests
  leave its existing Rex-VM-Probe distribution untouched. ARM64 runtime-death
  cleanup passed; the first unprivileged write probe correctly failed closed.
  The distribution now maps Windows-drive ownership to its unprivileged UID;
  the corrected command/Python/confinement tests are running.
- Runtime exposes its existing WSL preflight under sandbox/wsl for host reuse.
  Rex now stages, checksums and probes a replacement owned distribution before
  activation. It keeps a previous installation through failed setup and checks
  a random ownership marker before retiring only its own distributions.
  Missing WSL or failed preflight keeps native chat/memory usable with commands off.
- Settings includes explicit sandbox setup/repair, administrator/reboot guidance
  when WSL is unavailable, and task-idle checks. The complete bundle builds an
  unprivileged Python/tool rootfs from a checksum-pinned Alpine base, with
  interop disabled. Native setup/repair tests are added but not yet verified.
- Full Rex Go tests and vet, 68 UI tests, production UI build, Windows host test
  cross-build and macOS Rust check pass after setup integration. One broader
  runtime Python test still assumes Linux /proc; it fails on macOS and is not
  counted as a passing cross-platform check.

- Full runtime native Windows suite passed at bfe81b6 (run 38877984848).
  ARM64 Linux worker Landlock dispatch also passed in the supplied VM.
- The WSL probe now launches from the same directory as real commands. The
  ARM64 startup probe passes with allowed writes and outside/interop denials.
  Literal shell metacharacters were preserved; its test now decodes JSON before
  comparing output. Full Python and cancellation checks are rerunning.
- Existing bots retain their saved command preferences when a sandbox is
  unavailable, but runtime registration, run configuration and fingerprints
  remove the unavailable tools. Unrelated bot edits remain possible; new command
  permissions still require a verified sandbox. Windows command permission also
  admits the bundled run_python handler and its private venv.
