# Rex for Windows (with WSL2 for commands) — PRD

Status: draft, saved for later · 2026-10-06

## Summary

Bring Rex to Windows 10/11 with the same product as on macOS: personal bots
with their own instructions, model, memory, browser, workspace folder,
routines and connections. Agent commands (shell, Python, package installs)
run inside a Rex-owned WSL2 distribution, confined with agent-runtime's
existing Linux Landlock sandbox. Everything else runs natively on Windows.

On macOS, Rex offers "Run shell commands" and "Create and edit files" only
when the runtime confirms commands are confined (Seatbelt). Windows has no
equivalent built in. WSL2 gives us a Linux kernel where agent-runtime's
Landlock mode already exists, so the same safety promise can hold without
building a Windows sandbox first.

## Goals

- Rex installs and runs on Windows 10 (22H2) and Windows 11, x64 and arm64.
- Feature parity with the macOS release: chat, models (local and hosted),
  memory with the bundled embedding model, web, browser control with the
  user's installed Chromium-family browser and a profile per bot, takeover,
  live browser view, files, routines, skills, MCP connections, TypeSafe,
  notifications and restart recovery.
- Commands and Python run confined: writes only inside the bot's workspace
  (and the trusted caches), no reads of credential folders or Rex's data,
  the same promise as Seatbelt on macOS.
- Without WSL2, Rex still works; only commands and file editing are
  unavailable, with a clear way to turn them on.

## Non-goals

- A native Windows sandbox (AppContainer, restricted tokens, Job objects).
  It stays the long-term option and is out of scope here.
- Running commands without confinement, even behind approval.
- Windows-native shells for bots (PowerShell, cmd). Bots get a Linux shell
  and Linux Python inside WSL.
- Modifying or depending on the user's own WSL distributions.
- Restricting network access from commands (also not done on macOS).

## Users and scenarios

- A Windows user who wants a travel bot that searches flights in their own
  browser profile. No WSL needed.
- A user whose bot writes and runs Python to clean a spreadsheet in the bot's
  folder. Needs WSL2; the files appear in their normal Rex folder.
- A user on a locked-down work laptop where WSL is disabled by policy. Rex
  works for chat, browser and memory and explains that commands are off.

## Product requirements

### Install and first run

1. Signed installer (MSI or NSIS) with WebView2 bootstrap. Per-user install,
   no admin rights for Rex itself.
2. Onboarding checks for WSL2: `wsl.exe --status` and the WSL version.
   - Installed: offer "Set up command sandbox". It imports a Rex-owned
     distribution (`wsl --import Rex <data dir>\wsl <rootfs>`) from a small
     bundled rootfs (Debian or Alpine slim with python3, pip, venv, git,
     curl, coreutils and the Landlock helper). No changes to the user's
     distributions or their default distribution.
   - Not installed: explain what it adds and offer to run
     `wsl --install --no-distribution` (this needs admin rights and may need
     a reboot). Skipping is fine; commands stay off.
   - Disabled by policy: say so plainly; commands stay off.
3. Settings → General shows the sandbox state (ready, setting up, missing,
   blocked by policy, Landlock unsupported) with a "Repair" action that
   re-imports the distribution.

### Commands and Python

4. "Run shell commands" and "Create and edit files" appear only when the
   runtime reports `execution_isolation: wsl` and its startup probe passed,
   matching the macOS rule.
5. `run_command` executes inside the Rex distribution through
   `wsl.exe -d Rex --cd <workspace> -- landlock-exec …`, confined to the
   bot's workspace (read and write) and trusted caches, with credential
   folders and Rex's own data unreadable.
6. Python: bots use the distribution's `python3`, with per-workspace
   virtual environments and pip caches as on Linux workers today.
7. Approval rules are unchanged (ask for risky actions; TypeSafe review and
   auto-approve work the same).
8. Command output, exit codes and timeouts match the macOS behaviour.
   Cancelling a run stops its WSL processes.

### Files

9. Bot workspaces stay where users can see them: `%USERPROFILE%\Rex\<bot>`
   by default, visible in Explorer. Inside WSL they appear as
   `/mnt/c/Users/<user>/Rex/<bot>`; the runtime translates paths both ways.
   (See open question 1: if Landlock is unreliable on the Windows-drive
   mount, workspaces move into the distribution's own disk and Explorer
   opens them through `\\wsl.localhost\Rex\…`.)
10. File tools (read, list, write, edit, patch) run in the runtime on
    Windows with the existing workspace path checks; only commands go
    through WSL.

### Browser

11. Detect installed Chromium-family browsers on Windows (Chrome, Edge,
    Brave, Vivaldi, Chromium) from their standard install paths and the
    registry. Edge is always present, so there is always a default.
12. Per-bot profiles under `%LOCALAPPDATA%\ai.rex.desktop\browser-profiles`,
    pre-launched by Rex exactly as on macOS (headed or hidden, takeover,
    live view, idle close, resume after restart).
13. Stopping a browser that holds a bot's profile uses the Windows process
    list instead of `ps`; "bring to front" uses the window API instead of
    AppleScript.

### Everything else

14. Secrets in Windows Credential Manager (the keyring library already
    supports it), service `ai.rex.desktop`.
15. Data in `%LOCALAPPDATA%\ai.rex.desktop` (the embedding model, runtime
    database and logs are machine-local; nothing roams).
16. Bundled embedding server: a llama.cpp build for Windows (CPU, with
    Vulkan where available), same model and fixed model ID as macOS so
    memory moves between machines without a rebuild.
17. Notifications through Windows toasts; the waiting count as a taskbar
    overlay badge and in the tray icon tooltip.
18. "Reveal" actions open Explorer (`explorer /select,`).
19. Restart recovery as on macOS: stable host port, interrupted runs
    continued by the runtime, browsers reopened on their last page.

## Architecture

```
Tauri 2 + React (WebView2)                      Windows
   │  HTTP + SSE on 127.0.0.1 (per-launch token)
   ▼
rex-host.exe (Go)                               Windows
   │  bots, memory, settings (Credential Manager), browser control,
   │  scheduler, WSL setup and health checks
   ▼
agent-runtime.exe (Go)                           Windows
   │  file tools, browser tools (agent-browser.exe), MCP, models
   │  execution isolation "wsl": each command ──────────────┐
   ▼                                                         ▼
Chrome / Edge / Brave with the bot's profile        wsl.exe -d Rex
                                                     landlock-exec → bash / python3
                                                     (Linux kernel, Landlock ABI 3+)
```

Why the runtime stays on Windows rather than moving entirely into WSL: the
browser, the user's installed browser profiles, Credential Manager and the
Explorer-visible workspace all live on Windows. Only the command step needs
Linux, so only that step crosses into WSL. Process supervision stays simple
(one Windows process tree), and a machine without WSL still gets a working
app.

## Engineering work

### agent-runtime (additive, opt-in, like Seatbelt)

- New isolation mode `AGENT_RUNTIME_EXECUTION_ISOLATION=wsl` with settings
  for the distribution name and the in-distribution path of the
  `landlock-exec` helper.
- Command dispatch: translate the run root and allowed paths to WSL paths,
  build the `wsl.exe` invocation, stream output, map exit codes, kill the
  process group inside WSL on cancel or timeout.
- Startup probe: run the helper once to confirm Landlock ABI 3+ and that a
  write outside the run root is denied. The server admits command-capable
  lightweight runs only when the probe passes (the same rule as Seatbelt).
- Capabilities report `execution_isolation: wsl` and the probe result.
- Windows build: replace Unix-only code (repository cache `flock` and
  `O_NOFOLLOW` opens) with Windows equivalents behind build tags; keep Linux
  and macOS behaviour unchanged.
- Linux build of the `landlock-exec` helper shipped in the rootfs.

### Rex

- Windows builds of rex-host: process supervision with Job objects instead
  of process groups and signals (runtime, embedding server, orphan reaping),
  Windows pidfile checks.
- WSL setup service in rex-host: detect, import, repair, health check;
  expose state in `/api/status` and Settings.
- Browser module: Windows detection, profile-holder cleanup, bring-to-front.
- Paths: data dir, workspace root, reveal, log reveal.
- Tauri: Windows bundle (installer, signing, WebView2), toast notifications,
  taskbar overlay badge; drop the macOS-only notification crate on Windows.
- Build scripts: Windows sidecar builds (rex-host, agent-runtime,
  agent-browser, llama-server) and the rootfs image.
- CI: Windows job building the installer and running the gates.

## Security model

- Same promise as macOS: a bot's commands can write only inside its own
  workspace and trusted caches, cannot read credential folders or Rex's
  data, and run only when confinement is verified.
- The Rex distribution is dedicated: no user files besides the mounted
  workspace paths that Landlock allows. Windows drive mounts other than the
  workspace are unreadable to commands through Landlock rules.
- Interop: `wsl.conf` in the Rex distribution disables Windows interop
  (`[interop] enabled=false`) so commands cannot launch Windows programs
  (`cmd.exe`, `powershell.exe`) from inside the sandbox, and
  `appendWindowsPath=false`.
- No automatic fallback to unconfined commands. If the probe fails, commands
  and file editing are off and Settings says why.

## Release gates

Re-run the macOS gates on Windows, plus WSL-specific ones:

- M1 chat, streaming, cancel, restart.
- M2 permissions: writer approval; a command writing outside the workspace
  is denied; reading `%USERPROFILE%\.ssh` (via `/mnt/c/...`) is denied;
  `cmd.exe` cannot be launched from a command.
- M3 memory with the bundled embedding model.
- M4 browser: own profile per bot, takeover, live view, idle close, resume.
- MCP and M5 routines.
- W1 no WSL: app works; commands and editing hidden; onboarding explains.
- W2 WSL present: setup imports the distribution; probe passes; a bot runs
  Python in a venv in its workspace and the output file shows in Explorer.
- W3 restart during a running command: the run continues after relaunch.
- Packaging: install, upgrade, uninstall leave no processes, and uninstall
  offers to remove the Rex distribution.

## Risks and open questions

1. **Landlock on the Windows-drive mount.** `/mnt/c` is a 9p (drvfs) mount;
   Landlock rules attach to inodes, and the runtime already rejects 9p for
   its repository cache. Verify first that path-beneath rules on drvfs are
   reliable. If they are not, workspaces live on the distribution's ext4
   disk (faster too), and Explorer reaches them through `\\wsl.localhost`.
   This is the first spike and decides requirement 9.
2. **Landlock in the WSL kernel.** Confirm the shipped WSL2 kernel has
   Landlock enabled in its LSM list with ABI 3+. If a user's kernel lacks
   it, the probe fails and commands stay off.
3. **Windows 10 support.** WSL2 on Windows 10 has older kernels; decide
   whether commands require Windows 11 or a minimum WSL version.
4. **arm64 Windows.** Check llama.cpp, agent-browser and the rootfs on
   arm64.
5. **Command performance on `/mnt/c`.** File-heavy work (pip installs,
   many small files) is slower across the mount; keep virtual environments
   and caches inside the distribution's disk.
6. **Enterprise policy.** WSL may be blocked; the app must stay useful
   without it.
7. **Distribution updates.** Decide how the bundled rootfs is updated
   (replace on app upgrade, keep workspaces outside it).
