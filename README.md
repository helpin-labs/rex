# Rex

Personal bots that work on your Mac, remember what matters, and ask before they act.

Rex is a local-first macOS app. Each bot has its own instructions, model, permissions, workspace folder and memory. It runs on a local model server (LM Studio, Ollama) or a hosted provider (Anthropic, OpenAI, OpenRouter), and it uses the bundled `agent-runtime` for execution.

## What it does

- **Bots as contacts.** You can create any number of bots, each with a name, an avatar, instructions, a model and permissions.
- **Conversations** stream responses and show tool activity. They have approval cards, questions from the bot, attachments, a stop button, and a workspace-files drawer.
- **Permissions.** These are the web, the browser, reading and writing files in the bot's own folder, and shell commands. Shell commands always ask first, and every command runs in a macOS sandbox (Seatbelt) that can write only inside the bot's folder and cannot read your credentials or Rex's data. Each bot is set to "ask before changes" or "act within scope".
- **Automatic memory.** After each answer, durable facts from what *you* said are extracted in the background by the bot's own chat model, and this never delays a reply. Finding memories uses a built-in embedding model (Bekko Embedding v1 a8m, a small multilingual model, on a bundled llama.cpp server), so it needs no model server. Relevant memories are recalled into context before each new message. The memory view lists each source with its facts and lets you search, teach, correct or forget. Each bot has its own memory by default; sharing is explicit.
- **Browser** with a persistent profile per bot. When a site needs a login, the bot asks you to **take over**: the bot's browser opens in a window, you sign in, and you hand it back. The bot cannot act while you hold it, and screenshots are saved to the bot's `outputs/` folder.
- **MCP connections.** You can connect a Streamable HTTP MCP server, choose its tools and whether each is read or write, and attach it to bots. Write tools follow the bot's approval setting.
- **Skills** are editable Markdown instruction files in `~/Rex/skills`, enabled per bot.
- **Routines.** These run on daily or repeating schedules. A slot is claimed in the database before it runs, so a restart never duplicates a run. Retries are bounded (up to 2), five failures in a row pause the routine, and slots missed by more than an hour are recorded as *missed* rather than run late. Routines need the Mac awake and Rex running; closing the window keeps Rex in the menu bar.
- **Credentials** are stored in the macOS Keychain. The UI talks to the backend over loopback with a per-launch token.

## Architecture

```
Tauri 2 shell (Rust) ── supervises ──▶ rex-host (Go)  ── supervises ──▶ agent-runtime (Go, SQLite)
                                                       └─ supervises ──▶ llama-server (embeddings)
  React UI  ◀── HTTP + SSE, token ──┘   bots, chats, routines,             native_sdk runs, tools,
                                         memory (agent-runtime/memory),     approvals, events
                                         workspace + MCP callbacks
```

- `cmd/rex-host` and `internal/rex` make up the host. It owns product state (`rex.db`), memory (`memory.db`), settings and secrets. It generates the runtime's app config: model endpoints, workspace provider, target-context endpoint and browser artifacts.
- `agent-runtime` is unchanged and built from `../agent-runtime`.
- Data lives in `~/Library/Application Support/ai.rex.desktop/` and bot workspaces in `~/Rex/<bot>/`.

## Develop

Prerequisites: macOS, Go 1.25+, Rust, Node 20+, pnpm, cmake (to build the bundled llama.cpp embedder once), `agent-browser` (for browser bots), and a model server or an API key.

```bash
pnpm install
pnpm tauri:dev        # builds sidecars (incl. the embedder), starts Vite and the app
```

To run the backend without the shell:

```bash
./scripts/build-sidecars.sh
REX_DATA_DIR=/tmp/rex REX_SECRETS=file \
  AGENT_RUNTIME_BINARY=src-tauri/binaries/agent-runtime-aarch64-apple-darwin ./bin/rex-host
pnpm dev   # then open http://127.0.0.1:5180/?token=<token from /tmp/rex/connection.json>
```

## Test

```bash
go test ./...                         # unit tests
pnpm test                             # frontend tests
REX_E2E_MODEL=openai/gpt-oss-20b python3 scripts/e2e.py   # end-to-end gates against LM Studio
```

`scripts/e2e.py` starts its own host in a temporary directory and checks the release gates against a real model:

| Gate | Checks |
|---|---|
| m1 | Submit, stream, cancel, restart, reopen the conversation, continue the chat |
| m2 | Bots with different permissions: a read-only bot cannot write, a writer asks before writing, and a shell command is blocked outside the bot's folder |
| m3 | A preference survives a restart, a correction wins, a forgotten source stops being recalled |
| m4 | A bot hits a login wall, asks for takeover, continues after handoff, and saves a screenshot |
| m5 | A routine runs once, there is no duplicate after a restart, and failures are reported with a bounded retry |

## Build

```bash
pnpm tauri:build      # produces src-tauri/target/release/bundle/macos/Rex.app and a .dmg
```

The build is unsigned. To distribute it, set `APPLE_SIGNING_IDENTITY` and the notarization variables Tauri documents, and add the updater plugin with a release feed.

## Known limits

- Commands run in the agent-runtime `seatbelt` sandbox: writes are limited to the bot's folder and credential folders are unreadable, but other files are readable and network access is open.
- The runtime's browser snapshot shows headings, links and form controls, not body paragraphs, so bots read pages best through headings, tables of links, or `fetch_url` for public pages.
- Local models must support tool calling and should be loaded with at least 16k context. Use the connection test in Settings → Models to check.
