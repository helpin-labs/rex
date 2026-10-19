# Windows implementation and verification plan

Status: implementation and bounded local VM verification completed. Created 2026-10-16. Hosted CI GUI verification remains unresolved; no signed Windows release is verified.

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
| Rex Windows arm64 build | cross-build passed on Windows runner | Native ARM64 host suite in supplied VM; timezone and interactive credential follow-ups passed |
| Windows native core/memory/credentials | native suites and complete real-model gates passed | ARM64 backend gates; installed x64 CPU memory gate: extraction, restart recall, saved-preference correction, consolidation (7 facts, 1 observation), forget and no recall after restart; interactive Credential Manager passed |
| Windows browser integration | Chrome E2E, Chrome/Edge pipes, exact-profile cleanup and real cookie handoff passed | Runtime run 38882994906; Rex run 38883376932 |
| WSL confinement proof | passed on tested WSL2 kernel, ext4 and Windows-mounted drive | [run 38874224638](https://github.com/helpin-ai/agent-runtime/actions/runs/38874224638), kernel 6.18.40.1, ABI 7 |
| WSL setup/commands/cancel/restart | native x64 and ARM64 passed; ARM64 repair/removal passed | Runtime run 38879116759; supplied VM bridge and 459-second setup/repair/removal test |
| Installer/signing/upgrade/uninstall | native x64 lifecycle and full interactive VM lifecycle passed; signing pending | Rex run 38888788644; clean install/GUI/reinstall/uninstall passed in the fast Windows 11 VM |
| Windows notifications and attention | interactive Windows 11 passed | Installed x64 app in ARM64 HVF VM: numeric taskbar badge/clear, native toast, click-through from another bot and tray Quit; all sidecars stopped |
| macOS regression checks | foundation passed | Rex full Go tests/vet; runtime workspace/memory/runtime/API/cancellation tests; lock/memory race tests |
| Linux regression checks | passed | Runtime run 38882994906, command/isolation regression job |

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

- Native setup caught a Windows service ACL requirement: WSL could not mount a
  VHDX inheriting Rex's owner-only ACL. Only the managed WSL storage subtree now
  grants SYSTEM access; callback tokens, credentials and other app data keep
  owner-only ACLs. A native inheritance/containment test and setup rerun cover it.
- The ARM64 VM's next WSL instance hit a CreateInstance timeout under software
  emulation before application tests ran. The previous confinement/worker and
  runtime-death results stand; the full bridge and product setup remain pending.

- Runtime a92d469 passed all native Windows, Linux regression and real WSL bridge
  jobs: runs 38879116898 and 38879116759. Python, file persistence, timeouts,
  cancellation, runtime-death cleanup and filesystem/interop confinement passed.
- ARM64 native host suite completed in the supplied clean VM. Two named-timezone
  cases failed because no Go zone database is installed; Windows builds now embed
  standard-library tzdata. Credential Manager correctly rejects the SSH network
  logon session; its VM verification must use an interactive Windows login.
  Other native host cases passed, including job cleanup, ACLs and junction guards.
- Standard browser mode now bundles the same standalone agent-browser 0.25.5 CLI
  as the runtime image, with a pinned archive checksum and Apache license. No
  Node/npm install is required by users. Real cookie handoff is a native CI gate.
- Windows profile-lock cleanup now enumerates native processes, reads argv through
  the Windows API, matches the exact profile and excludes renderer children. It
  holds the process handle during tree termination to prevent PID reuse. Native
  tests ensure a similarly prefixed unrelated profile remains running.

- Rex setup/repair and sidecar smoke checks passed natively in run 38879760629;
  the installer build is running. The ARM64 full WSL bridge also passed in the
  supplied VM: command/Python file writes, timeout, cancellation, runtime death,
  outside-file/interop denial. The existing probe distro remains unchanged.
- ARM64 timezone cases now pass without Go installed, and both private-data and
  WSL-specific SYSTEM ACL tests pass. Native process-profile assertions passed;
  its temporary hard link exposed a Windows image-lock issue. The shim now copies
  its executable, with a repeated-launch regression test.
- Windows taskbar attention now renders the actual count (capped at 99+) and
  clears at zero. Rust count/overflow tests pass; interactive rendering is pending.

- ARM64 Rex-owned setup/repair passed in the VM, including replacing the sandbox,
  retaining the previous environment until activation, preserving it through
  failed install, ownership checks and WSL storage ACLs (191 seconds under TCG).
- Windows Standard browser handoff found inherited daemon stdout keeping Go's
  capture open after the CLI exited. Runtime now owns a shared browser.RunCLI
  helper with a bounded pipe wait; real subprocess tests cover retained daemon
  output, nonzero exit and cancellation. Rex consumes it rather than duplicating
  the fix. Native cookie-handoff verification is rerunning.

- Rex run 38883376932 is fully green: native host suite, Chrome/Edge pipes,
  Standard-mode cookie handoff, bundled CPU memory readiness, WSL setup/repair,
  and unsigned NSIS build. Runtime run 38882994906 is fully green too.
- Native ARM64 setup/repair/removal passed in the supplied VM (459 seconds).
  Removal is idempotent and rejects a foreign ownership record. The uninstall
  hook preserves data if ownership/removal cannot be verified. A native x64
  install/reinstall/uninstall gate now reuses the sidecar smoke check.
- The VM's x86/x64 installer and CLI stall under software ARM64 emulation before
  product initialization; this does not establish a native x64 installer result.
  Actual model-driven native ARM64 chat/memory/permissions/MCP/routine gates are
  running against the existing local model server with isolated data.

- The preview now pins runtime 26f2748 directly in go.mod as
  v0.13.16-0.20261016094832-26f2748cde84. The Windows workflow's temporary
  cross-repository checkout/workspace is removed, so ordinary local builds and
  CI consume the same code. SDK stays at v0.9.0, with no SDK changes required.

- Live native ARM64 application gates passed against the dedicated 32K local
  model: streaming/cancel/restart/follow-up, per-bot files and approvals, real
  command confinement, MCP read/approve/decline, routines/retries, and complete
  memory extraction/recall/correction/consolidation/forget across restarts.
  Consolidation processed five facts and created two observations. An invalid
  source citation was rejected and recovered on its bounded retry; no manual
  source cleanup was needed.
- Fast approval responses exposed a Rex UI race: pending interactions were
  visible while the runtime was still saving its paused checkpoint. Rex now
  exposes controls only for paused runs. Four-state regression passes on Mac
  and native ARM64 Windows; the complete real-model file/command gate passed.
- Actual explicit-path CLI teardown removed only the disposable Rex WSL distro,
  preserved a conflicting environment directory, and left Rex-VM-Probe intact.
  The WSL VM was cleanly shut down and an independent APFS-cloned fast Windows
  disk is being used for installer/GUI checks.

- Installed x64 desktop checks passed in the independent hardware-accelerated
  Windows 11 ARM64 VM: default Credential Manager, bundled CPU memory readiness,
  onboarding/local model check, real streamed chat, numeric badge/clear, native
  toast delivery and click-through to the correct conversation. Tray Quit left
  no Rex/runtime/embedder processes. This is x64 emulation on ARM64, separately
  from the native x64 CI lifecycle gate.
- The exact installed-WebView2 smoke script passed in the interactive tester
  session. Diagnostics now retain app/host logs, initial DOM and screenshots;
  cleanup cannot mask the original exception. The macOS Quit shortcut is preserved; Windows uses the
  tested native tray Quit action. Final CI rerun is pending.

- The complete installer acceptance script passed locally in the interactive VM:
  clean install, real bundled sidecars, installed WebView2 onboarding/provider
  switching/attention commands, reinstall, and default uninstall retaining the
  sentinel data. No owned services remained. An SSH-session attempt was rejected
  by Windows Credential Manager's missing logon session; no storage fallback or
  production change was added for that noninteractive environment.

- The installed x64 package also passed the complete live memory gate with its
  bundled llama-server/Bekko CPU embedder: extraction, recall after restart,
  explicit saved-preference correction, fresh-context use, manual consolidation
  (seven facts processed, one observation created), recall, Forget and no recall
  after another restart. This closes the earlier server-embedding distinction.
- One ambiguous “from now on” correction made the model propose an instruction
  change instead of saving a note. Inspection confirmed the correction evidence
  was stored and retrieved newest-first; the proposal remained unapplied. The
  memory fixture now explicitly requests a saved-memory correction. Its fresh
  complete run passed, with no manual alteration of stored facts.
- Final native host CI run 38899795851 passed all host tests, real Chrome/Edge
  pipes, cookie handoff and the ARM64 cross-build. Its desktop job passed UI,
  real bundled sidecars and WSL setup/repair; final installer verification is
  still running. Production signing, Windows 10, fresh provider OAuth and a
  native ARM64 desktop installer remain separate unverified gates.

- Physical Ctrl+Q input reached the Windows WebView but did not trigger the
  native menu accelerator, even after registering a window menu through Tauri.
  The Windows tray now omits the unsupported shortcut hint. Native tray Quit
  remains the verified shutdown action; no global hotkey or frontend workaround
  was added. This final Rust change requires another native installer build.

## Final bounded verification checkpoint — 2026-10-16

- Runtime application source: `26f2748cde84a03b01fb538ff060b3479c872b5d`,
  feature branch `feat/windows-support`; native Windows/Linux CI passed.
- Rex application source: `914e3e6718417b6a0b05660180a26519d9709a42`,
  feature branch `feat/windows-support`; SDK remains v0.9.0 unchanged.
- Local VM gates completed: native ARM64 host/credentials/WSL confinement and
  cleanup; real chat/stream/cancel/restart, approvals, MCP and routines; installed
  x64 desktop under ARM64 hardware emulation, bundled CPU memory lifecycle,
  notification/badge/click-through, tray shutdown, and installer lifecycle.
- Hosted CI run 38899795851 failed the installed GUI gate because WebView2 did
  not expose a debugging page. Preserved app/host logs confirm runtime and CPU
  memory became ready. This is not a passing hosted GUI result, and the root
  cause is not established. Local interactive GUI evidence remains separate.
- Run 38902598676 tests the final application source; its native host suite passed
  and its desktop installer build was still running at this checkpoint. No
  further rebuild or diagnostic loop was started. Review its eventual result:
  https://github.com/helpin-labs/rex/actions/runs/38902598676
- The fast VM retains the verified development installer (4b98dc7), not a claim
  that the final 914e3e6 installer has been verified. Test tasks were removed and
  the fake GUI profile archived after clock correction; original test evidence
  and both original VM disks were preserved. The Mac Rex instance was untouched.
- Remaining release gates: hosted CI GUI result, final-source installed-package
  verification, signing, fresh provider OAuth, Windows 10 and native ARM64
  desktop packaging. These are explicitly unverified, not waived.


## Preview release automation — 2026-10-16

- Added semantic version planning on main, reusable macOS/Windows packaging and
  draft-first publication only after both platforms pass. Artifacts must have
  matching versions, source commits and checksums. No real release was published
  during local verification. See [desktop releases](RELEASING.md).
- Windows self-signing passed in the supplied VM using copies of the three owned
  services and existing NSIS installer. The actual configured PowerShell hook
  handled paths with spaces, signatures verified, tampering was rejected, and
  only the public certificate was exported. Temporary key/trust cleanup passed.
  The test also caught an interactive current-user trust prompt; CI now uses its
  elevated disposable machine store for verification and removes trust afterward.
- An isolated macOS preview build stamped version 0.1.1 in all application
  manifests and produced the app and DMG. Signature verification, real bundled
  runtime/memory readiness and graceful service cleanup passed. The running Mac
  app and its data were not changed.
- Release-planning/asset checks and all 68 UI tests passed locally. Workflow
  validation passed. Final existing Windows CI run 38902598676 also failed the
  installed WebView2 page gate; native host tests passed. This check remains
  mandatory and blocks release publication until it passes. The new complete
  signed Windows packaging workflow and public publication remain unverified.
- Self-signed/ad-hoc previews do not establish trusted publisher identities.
  Trusted signing/notarization, fresh OAuth, Windows 10 and native ARM64 Windows
  desktop packaging remain separate gates.


## Elevated Windows CI fixes — 2026-10-19

- The macOS CGO correction passed hosted run 39031804616, including app/DMG
  packaging, signature verification and real bundled-service readiness.
- Reproduced the Windows GUI failure in an elevated, noninteractive VM session:
  WebView2 150+ intentionally ignores environment-based debugging overrides.
  The GUI test now temporarily sets AdditionalBrowserArguments through an HKLM
  policy scoped to rex-desktop.exe, preserving and restoring existing policy.
  No application or macOS code changed. The complete elevated GUI check passed
  with onboarding, provider switching and attention commands; policy restoration
  after success and removal after forced failure also passed.
- Reproduced the browser-cookie test race with a response delayed beyond its
  four-second sleep. The test now waits for the actual login cookie before
  closing the plain browser. The delayed-response regression failed before the
  change and passed afterward twice on Mac and twice in the signed-in Windows
  VM. Chrome native pipes passed both runs and Edge native pipes also passed.
  An SSH-only attempt reached cookie readiness but its browser CLI could not
  configure Chrome; the signed-in desktop checks above remain separate evidence.
- Native Go tests, go vet and workflow validation passed. Changes are confined
  to test readiness, elevated test setup/cleanup and this ledger. The next hosted
  Windows run must pass before the branch is treated as ready for release.
