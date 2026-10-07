# Bot logins — plan

Status: implemented 2026-10-06 (internal/rex/logins.go, src/Logins.tsx). Agent-browser receives values as JSON on stdin (`batch`), never as arguments.

## Goal

Let a bot sign in to sites the user chose (for example momondo.se) with
credentials the user stored, without the bot ever seeing the password.

The model sees everything it reads. A password shown to a bot could end up
in a transcript, in memory, in a newsletter, or with a page that tells the
bot "type your password here". So Rex keeps the secret and types it into
the bot's browser itself.

## First choice: the bot's own browser profile

Each bot already has its own browser profile that keeps logins. For most
sites the user signs in once with **Take over browser** and the bot stays
signed in for weeks, with nothing stored in Rex. Stored logins are for
when that is not enough: sessions that expire often, sites that force
re-login, or unattended routines.

## 1. Storage

- Logins belong to one bot and live in the **macOS Keychain** (service
  `ai.rex.desktop`), like provider keys. Never in `rex.db` or files.
- A login is: site (registrable domain, e.g. `momondo.se`), username,
  password, and an optional **TOTP secret** for 2FA codes.
- `rex.db` keeps only non-secret metadata: id, bot, site, username,
  created, last used.
- Only that bot's tools can use a login. Other bots cannot see them; the main
  bot only learns that a login exists for a site.

## 2. Signing in

Rex exposes run-scoped MCP tools to bots with browser access:

- `list_logins` → site and username only, never the password.
- `sign_in(login_id, username_ref, password_ref, submit_ref?)` → the bot
  points at form fields from its page snapshot; Rex fills the values into
  the bot's own agent-browser session and optionally clicks submit. The
  result is only "signed in" or why it failed.
- `fill_code(login_id, code_ref)` → fills the current 6-digit TOTP code.

Checks before Rex types anything:

- **Site match:** the browser's current URL must be on the login's site
  (same registrable domain). A momondo.se password is never typed into
  `momondo-login.xyz`, whatever the page or the bot says. This is the main
  protection against phishing and prompt injection.
- **Own browser:** only the bot's own session (`BrowserControl.active`),
  never while the user has taken it over.
- **Field check:** the password ref must be a password input.
- **Approval:** `sign_in` and `fill_code` are write tools, so bots on
  "Ask for risky actions" show an approval card ("Sign in to momondo.se as
  amad@…?"). The browser profile keeps sessions, so this should be rare.

## 3. Keeping secrets out of everything else

- Passwords and codes never appear in the transcript, tool inputs or
  results, run events, memory or logs.
- Password fields are masked in screenshots and the live view.
- The transcript shows "Signed in to momondo.se as amad@…": the username
  is visible, the password is not.
- To verify before building: how Rex hands the value to agent-browser.
  Command-line arguments are briefly visible to other processes on the Mac;
  prefer stdin, an environment variable scoped to that one call, or the
  stream WebSocket if agent-browser supports it.
- Redact any stored password from text that passes through Rex (memory
  retention already redacts known secret shapes; add exact-value redaction
  for stored logins).

## 4. UI

- **Edit bot → Logins:** add (site, username, password, optional 2FA setup
  key or QR), see last used, replace or remove. Passwords are write-only:
  never shown again.
- **New bot setup:** a bot setting itself up can say "this job needs your
  momondo login"; Rex opens the add-login form. The bot never asks for a
  password in chat.
- **Pasted passwords:** if the user pastes something that looks like a
  password into a chat, Rex warns that the chat can't protect it and offers
  to save it as a login instead.

## 5. Not in the first version

- API keys for commands and scripts: a script can print them. Services with
  APIs fit better as MCP connections, which already carry authentication.
- Sharing one login across bots: each bot gets its own for now.
- Passkeys and logins that need the user's phone: Take over browser stays
  the way to sign in.

## Build order

1. Keychain storage and the Logins section in Edit bot.
2. `list_logins` and `sign_in` with the site check and approval.
3. 2FA codes (`fill_code`).
4. Prompting during setup, and the warning for passwords pasted into chat.

## Tests to write

- A login is stored in the Keychain and never returned by any API or tool.
- `sign_in` refuses when the page is on another domain, including
  look-alikes and subdomains of other domains.
- `sign_in` refuses while the user holds the browser.
- Tool results, run events and memory contain no stored password.
- TOTP codes match a reference implementation.
