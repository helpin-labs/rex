# Rename Dost to Rex — plan

Status: implemented 2026-10-07, all steps at once. Differences from the plan:
Keychain items move lazily (`LegacySecrets` in `internal/rex/legacy.go` reads
Dost's service when Rex has no value and copies it; Dost's items are left in
place), and stored turn origins are rewritten to `"rex"` by the migration
rather than read both ways. The migration is `MigrateFromDost`, run by
rex-host before anything else. A full backup from before the rename is in
`../.rename-backup-20261007` (source, Dost data folder, ~/Dost).

## Goal

The app, the main assistant and the code are called Rex. Nothing a user
already has is lost: bots, conversations, memories, skills, files, browser
logins, stored passwords, provider keys and MCP sign-ins all carry over on
the first launch of Rex.

## Scope today

About 420 mentions in 72 files (excluding dependencies and build output):
37 files in `internal/dost`, the frontend, `src-tauri`, `cmd/dost-host`,
scripts and docs. Most are plain text or Go package names. The ones that
matter are the names stored on the user's Mac:

| Name | Where it lives | Risk if renamed blindly |
|---|---|---|
| `ai.dost.desktop` (bundle id) | `tauri.conf.json`; app data dir `~/Library/Application Support/ai.dost.desktop` (dost.db, memory.db, browser profiles, logs) | Rex starts empty |
| `ai.dost.desktop` (Keychain service) | `cmd/dost-host/main.go`, `DOST_KEYCHAIN_SERVICE` | Provider keys, bot logins and MCP OAuth tokens disappear |
| `~/Dost` | workspace root; skills; bots' `workspace_dir` stored as absolute paths in `bots` | Bots lose their files and skills |
| `AppID = "dost"` | sent to agent-runtime with every run | Memory banks and run history are keyed by it |
| `LocalActorID = "dost-local-user"` | agent-runtime actor | Same |
| `dost-bundled/…` embedding model id | `memory.db` vectors | Memories re-embed or stop matching |
| turn origin `"dost"` | `turns.origin` rows | "From Dost" labels stop showing |
| Main bot named "Dost" | `bots` row with role `main` | Still called Dost |
| macOS notification and login-item permission | tied to bundle id | User is asked again (cannot be carried over) |

## Decisions

1. **Rename everything a person sees**, now: app name, window and menu bar
   title, Dock, notifications, the main assistant's name, onboarding, settings,
   prompts ("You are Rex…"), "From Rex" labels, docs.
2. **Rename the code**: Go module `github.com/helpin-ai/rex`, package
   `internal/rex`, binaries `rex-host` and `rex-desktop`, `scripts/rexctl.py`,
   env vars `REX_*`, MCP implementation names `rex-setup`, `rex-bots` and so on,
   tray id, CSS classes. The repo folder moves from `dost/` to `rex/`.
3. **Move the data on first launch** instead of keeping old names forever:
   the bundle id becomes `ai.rex.desktop` and `~/Dost` becomes `~/Rex`, with
   a one-time migration (below).
4. **Keep the three agent-runtime keys** (`AppID "dost"`, `dost-local-user`,
   `dost-bundled/…`) as constants named `legacyAppID` and so on, with a comment.
   They are internal, nobody sees them, and changing them means migrating
   agent-runtime's own data. Revisit only if agent-runtime gets a rename API.
5. Stored turn origin `"dost"` stays readable: accept `"dost"` and `"rex"`,
   write `"rex"`.

## One-time migration (rex-host, before anything else starts)

Runs only when `ai.rex.desktop` does not exist and `ai.dost.desktop` does.

1. Check that Dost is not running (its `connection.json` pid). If it is,
   tell the user to quit Dost first and stop.
2. Back up `dost.db` with `VACUUM INTO` (as the threads merge did).
3. Move `~/Library/Application Support/ai.dost.desktop` to `ai.rex.desktop`
   (same volume, so a rename; keep a `MOVED-TO-REX` note in the old place).
4. Move `~/Dost` to `~/Rex` if `~/Rex` does not exist; otherwise leave both
   and only rewrite paths that point into `~/Dost`.
5. Rewrite stored paths in `dost.db`: `bots.workspace_dir`, settings that hold
   the workspace root, and any skill or file paths that start with the old root.
   Then rename `dost.db` to `rex.db`.
6. Copy Keychain items from service `ai.dost.desktop` to `ai.rex.desktop`
   (provider keys, `login:*`, MCP OAuth tokens), check each copy reads back,
   then delete the old ones. On any failure keep the old items and leave a
   fallback that reads the old service.
7. Rename the main bot "Dost" to "Rex" (only if the user never renamed it),
   and update its instructions' greeting.
8. Write a marker so the migration never runs twice, and log what moved.

macOS will ask again for notifications and, if used, launch at login; the
first-run screen says why ("Dost is now Rex").

## Order of work

1. Visible text and the main bot's name (small, safe, can ship alone).
2. Code rename: module, package, binaries, env vars, scripts, sidecar build,
   `tauri.conf.json` product name and bundle id. One mechanical commit,
   built and tested before anything else.
3. Migration code with tests on a copy of a real data dir (paths, Keychain
   copy through the `Secrets` seam, idempotency, Dost still running).
4. Build Rex.app, run it against a copy of the current Dost data, then
   against the real data.
5. Remove the old Dost.app bundle after the user confirms everything is there.

## Tests to write

- Migration moves data dir and workspace, rewrites `workspace_dir`, and is a
  no-op on second run.
- Keychain items are copied and readable under the new service; a failed copy
  keeps the old ones and the fallback finds them.
- The main bot is renamed only when still called "Dost".
- Turns with origin `"dost"` still show "From Rex".
- Memory recall works after the move (agent-runtime keys unchanged).

## Not in this change

- Renaming agent-runtime's stored app and actor ids (decision 4).
- The Windows PRD (`docs/PRD-windows-wsl2.md`) gets a find-and-replace when it
  is picked up.
