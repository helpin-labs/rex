# Extension browser migration into agent runtime

Status: implemented locally, 2026-10-11. Runtime owns the Extension driver and
Rex uses its authenticated controls. Real Chrome tests on macOS cover navigation,
input, screenshots, takeover, cookies and full runtime restart. Runtime API,
engine and browser race tests, Rex Go tests and frontend checks pass. The existing
nine macOS runtime test failures remain unrelated to this migration.
The rebuilt packaged Rex app also passes Open, connected status, Take over and
Hand back using an existing bot profile. Chrome loads the runtime's extension
without remote-debugging flags; Extension remains the default.

Release gates remaining: Linux virtual-display smoke test, publish the runtime
release, pin it in Rex, and build with `GOWORK=off`. Local builds currently use
the sibling runtime through `go.work`; Rex still pins v0.13.11, so the build
guard rejects distribution without the new runtime. Rex uses its existing HTTP
client for the new controls; no SDK release is needed.

## Goal and scope

Make Extension a built-in agent-runtime backend alongside Standard and Kernel.
Apps such as Rex and Helpin should select it without embedding an extension,
running a command bridge, or launching the browser themselves. Models continue
using the existing `browser_*` tools.

Rex keeps Extension as its local default and keeps its settings and takeover UI.
Other apps retain their existing defaults and explicitly enable Extension.
Backend selection stays fixed for a run. Errors never silently switch an
explicitly selected backend.

Move the working implementation first. Preserve its DOM capabilities, bounded
navigation recovery, visible screenshots, reference invalidation, and human
handling of sign-in and verification. Additional browser capabilities and
Helpin's remote desktop UI are separate work.

## Ownership after migration

| Component | Owner |
| --- | --- |
| Extension manifest, content script and service worker | agent-runtime |
| Extension installation, loopback bridge and credentials | agent-runtime |
| Browser process, profile lease, command queue and deadlines | agent-runtime |
| Backend resolution, tool validation and screenshot artifacts | agent-runtime |
| Browser status, takeover gate and handback reference invalidation | agent-runtime |
| Mode preferences, bot instructions and user-facing controls | Rex or Helpin |
| Standard browser's existing Rex login and takeover integration | Rex, unchanged by this migration |
| Server display, remote input transport and user access control | Helpin deployment and UI |

The Extension path becomes:

```text
Rex or Helpin -> authenticated runtime controls -> browser manager
                                                 -> loopback extension bridge
                                                 -> Chrome extension -> page
Model        -> existing browser_* tools --------> browser manager
```

The runtime's extension bridge binds only to loopback. Apps use the runtime's
authenticated control API; they never expose extension bridge credentials to
users, model prompts or page scripts.

## 1. Move the existing driver into runtime

Move `internal/rex/browser_extension/{manifest.json,background.js,content.js}`
and the installation, bridge, launch and command handling from
`internal/rex/browser_extension.go` into agent-runtime's existing
`internal/tools` browser implementation. Embed the files in the runtime binary.
Rename Rex-specific script identifiers while retaining the same protocol and
tests. Reuse `BrowserManager` rather than create a second browser manager.

Replace the Extension path in runtime's `internal/tools/browser_extension.go`
that forwards commands to Rex with calls to the runtime-owned driver. Keep
runtime screenshot validation, artifact writing and untrusted-content boundaries.
No Extension operation invokes agent-browser or attaches CDP or `chrome.debugger`.

Use an explicitly configured compatible Chromium executable, with bundled
Chrome for Testing discovery as a convenience. Resolve a missing browser to a
clear installation error. Browser installation remains a desktop packaging or
server image setup step; a model tool does not download it. Avoid importing Rex's
macOS browser discovery or Keychain code into runtime.

## 2. Configure identity, selection and profile ownership

Add a validated browser section to the agent's existing `ExecutionConfig` for
the backend and stable profile identity. Use the existing runtime browser
configuration for executable and storage settings. Selection is host policy,
never a URL, page value, model tool argument or free-form chat instruction.

Resolve the backend at run initialization and persist the resolved choice for
resumed runs. Rex translates its existing `browser_backend` preference and cloud
default into this configuration. Changing mode affects a new run, preserving
the current explicit-choice behavior.

Keep run sessions separate from persistent profiles. Namespace profiles by app
and agent, with at most one automation owner per profile. A second run or backend
gets a clear busy response while the profile is in use. Coordinate Extension
leases with Rex's Standard launcher during migration so two processes cannot
open the same profile.

For existing Rex installations, use a trusted app-level storage mapping to the
current `DataDir/browser-profiles/<bot slug>` directories. Reuse the directories
without copying or deleting cookies. Reject path traversal and arbitrary paths
from run input. Helpin supplies a persistent volume and a profile identity that
includes its tenant and user boundary; a disposable container alone cannot
provide durable logins.

Preserve an idle Extension page across consecutive tasks. Ending a run releases
its automation lease and invalidates references; it does not immediately erase
or close the agent's browser. Apply bounded idle cleanup separately, saving the
profile. Runtime shutdown closes owned processes gracefully. Restart recovery
must detect an existing profile holder and fail closed rather than attach a
debugger, kill an unrelated browser, or replay an uncertain action.

## 3. Add authenticated browser controls

Expose browser controls through the existing runtime service API, scoped by app
and agent. They operate on a persistent browser even when no run is active.
Endpoint names can follow the existing API conventions during implementation.

| Control | Behavior |
| --- | --- |
| Status | Backend, connection state, held state and current run; no credentials |
| Open | Open the agent's Extension browser for human use and mark it held |
| Take over | Wait for the current operation, atomically block subsequent automation, retain the page |
| Hand back | Invalidate references and release the automation gate |
| Close | Close the owned browser safely and release its profile lease |

The runtime enforces the held gate at command dispatch, even if an app fails to
pause a run. A paused run alone is insufficient because it can have an operation
in flight. Preserve held state across service restarts, or require explicit
handback before recovered automation proceeds. Expired commands and duplicate
responses remain rejected. Only safe reads and snapshots may retry within the
existing navigation bound; uncertain clicks and submissions are never replayed.

Keep per-profile bridge credentials private, verify allowed extension origins,
and match every response to a pending command and its session, epoch and deadline.
Authorization covers app and profile ownership as well as the run. Add matching
client methods in agent-runtime-go if Rex needs them, and publish that SDK before
Rex upgrades. These controls are host operations, not model-callable tools.

## 4. Desktop Rex and server Helpin

On desktop, runtime launches the visible browser locally. Rex calls Open or
Take over and directs the user to that window. Its browser panel polls runtime
status and calls Hand back. Extension still uses a separate persistent bot
profile and requires no manual extension installation or Developer mode.

On a server, Extension needs a compatible browser and a virtual display on the
same worker as its runtime-owned bridge. Helpin can use the driver before a
takeover UI exists, but must report human-required steps as blocked rather than
promise the user a desktop window.

Remote takeover is a separate Helpin feature: stream the virtual display through
noVNC or WebRTC and forward input through the OS display session, without adding
a DevTools connection. Pause automation with the same runtime gate. Authenticate
the user, check profile ownership, use short-lived access, and isolate displays
between users. In a deployment with several workers, route both the run and its
takeover connection to the worker owning the profile. No server browser inherits
the user's desktop cookies automatically.

## 5. Remove Rex's Extension implementation

After the runtime release and any required SDK release are available, pin them
in Rex and replace its Extension branches with runtime control calls. Preserve
the existing Rex-facing bot browser endpoints where that avoids frontend churn.

Remove from Rex:

- `internal/rex/browser_extension.go` and the embedded extension directory.
- `/extension/browser` and `/runtime/browser/driver` routes and handlers.
- The extension queue, pending responses, tokens, process map and lifecycle code.
- `AGENT_RUNTIME_BROWSER_HOST_URL` and `AGENT_RUNTIME_BROWSER_HOST_TOKEN` setup
  in `cmd/rex-host/main.go`.
- The sidecar script's source inspection for the host callback.
- Tests for implementation details now owned and tested by runtime.

Keep the mode selector, stored bot preference, profile mapping, feature guidance,
Standard login handling and takeover UI. Retain the Standard shim needed for
Rex's existing integration, but remove its Extension callback-specific logic.
The runtime itself must ensure Extension never enters the Standard execution
path. Old Rex extension configuration directories may be removed only after
the old browser has stopped and the new runtime works; browser profiles remain.

Update `docs/BROWSER-extension.md` to describe runtime ownership, requirements
and the new release. Remove all claims that Rex embeds or serves the extension.

## 6. Verification and release order

1. **Runtime implementation:** move driver and tests, add validated configuration,
   controls, profile leases and lifecycle handling. Keep the v0.13.11 host adapter
   temporarily for older consumers; native Extension must work with no host URL.
2. **Runtime checks:** run bridge isolation and credential tests, navigation and
   uncertain-action tests, profile contention tests, takeover races, restart
   recovery, and existing Standard and Kernel regressions. Move the DOM and
   service-worker tests to runtime with a lightweight test runner.
3. **Real browser checks:** run localhost fixtures on macOS and Linux with a
   virtual display. Verify navigation, input, screenshots, stale refs, idle task
   transitions, takeover, and cookie persistence after restart. Verify the
   launch has no debugging flags and the extension has no debugger permission.
4. **Standalone check:** start runtime without Rex or either host callback
   variable, execute the existing browser tools against the fixture, and use its
   authenticated controls. This demonstrates that another app can use the mode.
5. **Runtime and SDK releases:** publish the compatible versions. Rex's removal
   waits for these releases; no production dependency on a local `go.work`.
6. **Rex migration:** pin releases, remove implementation and callback setup,
   verify an existing bot profile retains its cookies, run Go and frontend tests,
   build the packaged app with `GOWORK=off`, and verify takeover and handback.
7. **Cleanup:** remove the deprecated runtime host adapter in a later release
   after its consumers migrate. Helpin adopts native Extension independently;
   remote takeover ships when its display and access controls are ready.

Completion means Rex contains no Extension assets, bridge or browser process
implementation, while its existing browser UI works through runtime. Standalone
runtime works on both desktop and server fixtures. Saved Rex logins survive,
explicit backend errors stay explicit, and a held browser cannot receive agent
commands. Extension remains experimental: passing bot checks is not a release
guarantee, and visible verification still belongs to the human.
