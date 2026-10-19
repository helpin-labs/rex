# Rex

Personal AI bots that live on your Mac. Each one has its own chat, memory, browser, skills and schedule, asks before it acts, and suggests ways to improve itself.

Rex runs on a local model (LM Studio, Ollama), your API keys (Anthropic, OpenAI, OpenRouter) or your ChatGPT subscription. Conversations, memory and files stay on your Mac; only the model calls you choose leave it.

Windows support is in development; see the [Windows preview instructions and verification status](docs/WINDOWS.md).

## Features

- **Bots.** Make as many as you like, each with its own instructions, model, permissions, workspace folder and face. A new bot sets itself up by asking you a few questions.
- **Rex, the main assistant.** Rex answers general questions, hands tasks to the right bot, and creates new bots for you.
- **Choose when to ask.** Each bot has three approval modes: Ask prompts for protected actions; Auto (Jev) uses [TypeSafe](https://typesafe.ai) to clear routine local steps, falling back to Ask when unavailable; Allow all skips automatic approval for enabled tools. Command confinement still applies, and explicit approval requests and questions always pause.
- **Memory.** Rex records task evidence locally and submits background learning jobs through the agent-runtime Go SDK. The runtime owns job claims, retries, recovery and consolidation; Rex supplies each bot's evidence/model and idle permission. After each answer, Rex keeps lasting facts from what you said and brings back the relevant ones before each new message. This happens in the background, with a built-in embedding model. You can view, search, correct or forget memories. Each bot has its own memory unless you choose to share it.
- **Browser.** Extension is the local default, with a separate profile for each bot so logins persist. Standard and Cloud remain explicit choices; modes never switch automatically.
  - When a site needs you, for a sign-in or a CAPTCHA, you take over the browser and then hand it back.
  - In Standard mode, saved logins are filled in by Rex, so the bot never sees your passwords.
  - Optionally, bots can browse in [Kernel](https://www.kernel.sh)'s cloud instead.
- **Skills.** Reusable instructions in the [Agent Skills](https://agentskills.io) format, kept in `~/Rex/skills`.
  - Browse curated GitHub collections (Anthropic's skills, marketing, sales, coding) and install a skill for a bot at a pinned version.
  - Bots can search the catalog and suggest a skill themselves.
- **Self-improvement.** After corrections, failures, or every few tasks, a bot looks back at its recent work and suggests better instructions or a skill. Nothing changes until you apply it.
- **Routines.** Daily, weekly or repeating tasks. Retries are bounded, and a restart never runs a task twice. A routine missed while the Mac was off is offered to you, not run late.
- **MCP connections.** Connect Streamable HTTP MCP servers (signing in with OAuth where they need it), pick their tools, and attach them to bots.
- **Private by design.** Keys, ChatGPT sign-in and logins live in the macOS Keychain, and the app talks to its backend over loopback with a per-launch token.

## Architecture

```
Tauri 2 shell (Rust) ──▶ rex-host (Go) ──▶ agent-runtime (Go, SQLite)
  React UI ◀─ HTTP + SSE ─┘     │       └─▶ llama-server (embeddings)
                                └─ bots, chats, routines, memory, skills,
                                   settings, workspace and MCP callbacks
```

- **`cmd/rex-host` and `internal/rex`** are the host. It owns the app's state (`rex.db`), memory (`memory.db`), settings and secrets, and configures the runtime.
- **[agent-runtime](https://github.com/helpin-ai/agent-runtime)** runs the bots: tools, approvals, browser, sandbox and events. It is pinned in `go.mod` as a Go `tool`, so the host and the bundled engine always match. To work against a local checkout, run `go work init . ../agent-runtime`.
- **Data** lives in `~/Library/Application Support/ai.rex.desktop/`, and bot workspaces in `~/Rex/<bot>/`.

## Develop

You need macOS, Go 1.25+, Rust, Node 20+, pnpm, cmake (to build the bundled embedder once), [`agent-browser`](https://github.com/vercel-labs/agent-browser) for browser bots, and a model server, an API key or a ChatGPT subscription.

```bash
pnpm install
pnpm tauri:dev     # builds the sidecars, starts Vite and the app
```

Backend without the shell:

```bash
./scripts/build-sidecars.sh
REX_DATA_DIR=/tmp/rex REX_SECRETS=file \
  AGENT_RUNTIME_BINARY=src-tauri/binaries/agent-runtime-aarch64-apple-darwin ./bin/rex-host
pnpm dev           # open http://127.0.0.1:5180/?token=<token from /tmp/rex/connection.json>
```

## Test

```bash
go test ./...
pnpm test
REX_E2E_MODEL=openai/gpt-oss-20b python3 scripts/e2e.py   # release gates against LM Studio
```

`scripts/e2e.py` starts its own host in a temporary folder and runs these release gates against a real model:

| Gate | Checks |
|---|---|
| m1 | Stream, cancel, restart, reopen, continue a chat |
| m2 | Permissions: a read-only bot can't write, a writer asks first, the sandbox blocks commands outside the bot's folder |
| m3 | Memory survives a restart, a correction wins, a forgotten source stays forgotten |
| m4 | Login wall, takeover, handoff, screenshot |
| m5 | A routine runs once, never twice after a restart, and retries within bounds |

## Build

```bash
pnpm tauri:build   # Rex.app and a .dmg in src-tauri/target/release/bundle/
```

Merges to `main` use [semantic desktop releases](docs/RELEASING.md) to publish matching Windows x64 EXE and Apple Silicon DMG installers after both platform checks pass. Preview Windows releases are self-signed; macOS releases are ad-hoc signed.

Builds run on Apple Silicon and are ad-hoc signed. They aren't notarized, so on another Mac the first launch needs System Settings → Privacy & Security → Open Anyway. To distribute Rex widely, sign it with a Developer ID and notarize it.

## Known limits

- The sandbox limits writes to the bot's folder and hides credential folders. Bots can still read other files, and network access is open.
- Local models must support tool calling. Load them with at least 16k context, and use Settings → Models → Test connection to check.
- Cloud browsers (Kernel) start fresh for each task: no saved logins and no takeover.
- Routines run only while the Mac is awake and Rex is running. Closing the window keeps Rex in the menu bar.

## License

[Apache License 2.0](LICENSE)
