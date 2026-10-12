# Experimental extension browser

The move of this driver into agent-runtime is described in
[the migration plan](PLAN-extension-runtime.md).

A bot can select **Standard**, **Extension (experimental)** or **Cloud (Kernel)**
in **Edit bot → Abilities → Browser mode**. Bots without an explicit mode use Extension for local browsing.
An explicitly enabled cloud default still uses Kernel. Standard remains an
explicit choice; there is no automatic fallback. The chosen backend is fixed for the run. Changing
mode causes the next message to start a new run; it does not transfer the live
page between drivers.

## Build

Rex pins **agent-runtime v0.13.12**, which includes the native Extension driver.
Build the packaged app with `GOWORK=off pnpm tauri:build` to use this release
even if a local development workspace exists.

For local runtime development, an ignored `go.work` can use both checkouts:

```sh
go work init . ../agent-runtime  # only if go.work does not already exist
pnpm sidecars
pnpm tauri dev
```

An older running runtime cannot launch agent-browser for an Extension session:
the shim rejects it before any debugger is attached.

## Installation

Use the default local mode or select **Extension** for the bot. Agent-runtime loads the extension automatically in
agent-browser's bundled Chrome for Testing when browsing starts. No Developer
mode or manual installation is required. Install the bundled browser with
`agent-browser install` if it is missing. The browser runs visibly, including
when Standard mode is configured to be hidden. The extension is embedded in
`agent-runtime` and refreshed at launch; it attaches no DevTools connection.

**Open extension browser** opens the bot's browser for human use. Hand it back
in Rex when ready. Extension mode always uses the bundled browser, independently
of the browser selected for Standard mode.

## Capabilities

The existing runtime browser tools support navigation, DOM snapshots, page text,
click/fill/type/select/check/uncheck, bounded waits, scrolling and visible-tab
PNG/JPEG screenshots. Screenshot capture requires the bot's tab to be active.
Snapshots use fresh references on every snapshot, document, run or takeover.
Navigation waits up to five seconds for the new content script. Interrupted reads
and snapshots can retry within that bound; clicks and form actions are never
replayed when their result is uncertain. Inspect a fresh snapshot before acting.

Snapshots are a bounded DOM representation rather than the complete browser
accessibility tree. Only the main frame is controlled in this first version.
Embedded frames, keyboard presses, hover, file uploads, recording, full-page or
annotated screenshots and saved-password filling return a clear error or require
human takeover. Existing local login cookies remain in the same bot profile.
The live panel directs the user to watch the real window instead of opening a
DevTools stream. Extension windows stay open until runtime's idle timeout.
Closing saves the page URL; reopening restores that URL and the persistent profile.

## Ownership and security

Agent-runtime owns tool schemas, input validation, backend selection, artifact
storage, extension installation, profiles, process launch and the takeover gate.
Its native Extension driver never executes agent-browser. Rex sends authenticated
control requests and keeps the existing mode selector and takeover UI. Its profile
mapping preserves existing `browser-profiles/<bot slug>` cookies.

The loopback bridge has a separate random credential for each bot. Credentials
are stored in that bot's extension folder with user-only permissions and stay in
the service worker. Content scripts have no credential, page messaging bridge,
externally-connectable entry or debugger permission. Unexpected browser origins
are rejected. Replies must match an outstanding command for the authenticated
bot; expired and duplicate replies are rejected. Commands carry a deadline,
run session and reference epoch. The Manifest V3 worker uses bounded polling and
alarms to reconnect, without automatically replaying an action after a failure.

Takeover waits for the current extension operation, blocks further commands, and
keeps the same page open. Held state survives runtime restart. Handback invalidates
references. A second run cannot claim an occupied profile. Sign-in and visible
verification are performed by the human. Kernel is launched with `stealth=false`
because its stealth setting loads an automatic CAPTCHA solver; this changes the
previous cloud behavior. Explicit cloud failures never silently become local
browsing. Rex's existing cloud integration still lacks takeover and persistent
cloud profiles.

## Checks and rollout

```sh
pnpm test
pnpm build
GOWORK=off go test ./...
cd ../agent-runtime
go test -race ./internal/tools ./internal/api ./internal/engine -run 'Test(Browser|HostSelected|Extension|Native|StartRunFreezesTrustedBrowserPolicy|ExplicitCloud)'
AGENT_RUNTIME_EXTENSION_E2E=1 go test ./internal/tools -run TestExtensionBrowserWithoutDevTools -count=1 -v
```

The last check uses a temporary profile and localhost fixture, with no DevTools
connection. It exercises navigation, form input, text, screenshots, references
and cookie persistence across full runtime restart. It requires the agent-browser-installed Chrome for
Testing, but does not use agent-browser to drive it.

Protected-site completion rates, Linux virtual displays, all supported Chromium
products and long-running worker recovery still need representative trials before
broader rollout. A host crash can leave a bot's browser open; close that window
before reopening Extension mode if its profile is reported as locked. No claim
is made that extension automation passes bot checks. Automatic backend switching
and production extension distribution through a store are deferred until the
explicit driver has demonstrated useful task completion rates.
