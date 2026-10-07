#!/usr/bin/env python3
"""End-to-end gate checks for Rex against a real local model server.

Usage:
  REX_E2E_MODEL=openai/gpt-oss-20b \
  REX_E2E_EMBEDDING=text-embedding-nomic-embed-text-v1.5 \
  python3 scripts/e2e.py [m1] [m2] [m3] [m5]

Starts its own rex-host (bin/rex-host) with a temporary data directory,
in-memory secrets and a temporary workspace root, and restarts it where a
gate requires surviving a restart.
"""
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BASE = os.environ.get("REX_E2E_DIR") or tempfile.mkdtemp(prefix="rex-e2e-")
os.environ["REX_DATA_DIR"] = os.path.join(BASE, "data")
sys.path.insert(0, os.path.join(ROOT, "scripts"))
import rexctl as api  # noqa: E402

MODEL = os.environ.get("REX_E2E_MODEL", "openai/gpt-oss-20b")
EXTRACTION = os.environ.get("REX_E2E_EXTRACTION_MODEL", "qwen/qwen3.6-35b-a3b")
EMBEDDING = os.environ.get("REX_E2E_EMBEDDING", "text-embedding-nomic-embed-text-v1.5")
LOCAL_URL = os.environ.get("REX_E2E_LOCAL_URL", "http://127.0.0.1:1234/v1")
WORKSPACES = os.path.join(BASE, "workspaces")
host = None


def log(message):
    print(f"[{time.strftime('%H:%M:%S')}] {message}", flush=True)


RUNTIME_COPY = os.path.join(BASE, "agent-runtime-e2e")


def start_host():
    global host
    if not os.path.exists(RUNTIME_COPY):
        shutil.copy(os.environ.get("AGENT_RUNTIME_BINARY", os.path.join(ROOT, "src-tauri", "binaries", "agent-runtime-aarch64-apple-darwin")), RUNTIME_COPY)
    env = dict(os.environ)
    env.update({
        "REX_WORKSPACE_ROOT": WORKSPACES,
        "REX_SECRETS": "file",
        "REX_HOST_ADDR": "127.0.0.1:18911",
        "AGENT_RUNTIME_BINARY": RUNTIME_COPY,
    })
    out = open(os.path.join(BASE, "host.out"), "a")
    host = subprocess.Popen([os.path.join(ROOT, "bin", "rex-host")], env=env, stdout=out, stderr=out)
    time.sleep(1)
    api.wait_ready()
    log(f"host ready (pid {host.pid})")


def stop_host():
    global host
    if host is None:
        return
    host.send_signal(signal.SIGTERM)
    host.wait(timeout=30)
    host = None
    time.sleep(1)
    leftovers = subprocess.run(["pgrep", "-f", RUNTIME_COPY], capture_output=True, text=True).stdout.strip()
    assert not leftovers, f"agent-runtime still running after host stop: {leftovers}"
    log("host stopped cleanly")


def restart_host():
    stop_host()
    start_host()


def check(condition, message):
    if not condition:
        raise AssertionError(message)
    log("  ✓ " + message)


def configure():
    settings = api.call("GET", "/api/settings")
    settings["local"]["base_url"] = LOCAL_URL
    settings["default_model"] = MODEL
    settings["memory"] = {"enabled": True, "provider": "local", "extraction_model": EXTRACTION, "embedding_model": "", "embedding_source": "bundled"}
    settings["onboarded"] = True
    settings["browser_hidden"] = os.environ.get("REX_E2E_HEADLESS") == "1"
    api.call("PUT", "/api/settings", settings)
    time.sleep(2)
    api.wait_ready()
    for bot in api.call("GET", "/api/bots"):
        bot["model"] = MODEL
        api.call("PUT", f"/api/bots/{bot['id']}", bot)


def new_conversation(bot_id, title):
    return api.call("POST", "/api/conversations", {"bot_id": bot_id, "title": title}, expect=201)["id"]


def send(conversation_id, text):
    api.call("POST", f"/api/conversations/{conversation_id}/messages", {"content": text}, expect=202)
    return api.wait_turn(conversation_id)


def last_answer(detail):
    text = (detail["turns"][-1].get("assistant_text") or "").strip()
    return text.replace("\u2019", "'").replace("\u2018", "'")


def stream_events(run_id, until=("run.paused", "run.completed", "run.failed", "run.cancelled"), timeout=300):
    conn = api.connection()
    request = urllib.request.Request(f"{conn['url']}/api/runs/{run_id}/events?token={conn['token']}")
    seen = []
    deadline = time.time() + timeout
    with urllib.request.urlopen(request, timeout=timeout) as response:
        for raw in response:
            line = raw.decode().strip()
            if line.startswith("event:"):
                kind = line.split(":", 1)[1].strip()
                seen.append(kind)
                if kind in until:
                    break
            if time.time() > deadline:
                break
    return seen


# --- gates ---

def gate_m1():
    log("M1: desktop connection — submit, stream, cancel, reopen after restart")
    bot = api.bot_by_name("Rex")
    conversation = new_conversation(bot["id"], "M1 chat")
    result = api.call("POST", f"/api/conversations/{conversation}/messages", {"content": "Reply with one short sentence: what is the capital of France?"}, expect=202)
    events = stream_events(result["run"]["id"])
    check(any(e.startswith("assistant_message") for e in events), f"streamed assistant events ({len(events)} events)")
    detail = api.wait_turn(conversation)
    check("paris" in last_answer(detail).lower(), f"answer mentions Paris: {last_answer(detail)[:80]!r}")

    second = new_conversation(bot["id"], "M1 cancel")
    started = api.call("POST", f"/api/conversations/{second}/messages", {"content": "Write a detailed 2000-word essay about the history of the printing press."}, expect=202)
    time.sleep(3)
    api.call("POST", f"/api/runs/{started['run']['id']}/cancel", {}, expect=202)
    detail = api.wait_turn(second)
    check(detail["turns"][-1]["status"] == "cancelled", "second run cancelled")

    restart_host()
    detail = api.call("GET", f"/api/conversations/{conversation}")
    check(len(detail["turns"]) == 1 and "paris" in last_answer(detail).lower(), "conversation and answer survive a restart")
    check(any(m["role"] == "assistant" for m in detail["messages"]), "runtime transcript survives a restart")
    followup = send(conversation, "And of Italy? One short sentence.")
    check("rome" in last_answer(followup).lower(), f"follow-up after restart continues the chat: {last_answer(followup)[:80]!r}")


def make_bot(name, capabilities, approval):
    bot = {"name": name, "emoji": "🧪", "provider": "local", "model": MODEL, "instructions": "Complete file tasks precisely. Use your file tools.",
           "capabilities": capabilities, "approval_mode": approval, "memory_mode": "off"}
    return api.call("POST", "/api/bots", bot)


def gate_m2():
    log("M2: two bots with different permissions complete file tasks")
    reader = make_bot("Reader", {"files_read": True}, "auto")
    other = make_bot("Archivist", {"files_read": True}, "ask")
    with open(os.path.join(reader["workspace_dir"], "inbox", "notes.txt"), "w") as handle:
        handle.write("Project Falcon launches on 14 March. Owner: Priya. Budget: 12,000 EUR.\n")
    with open(os.path.join(other["workspace_dir"], "inbox", "notes.txt"), "w") as handle:
        handle.write("Project Heron launches on 2 June. Owner: Tomas.\n")

    conversation_r = new_conversation(reader["id"], "Reader task")
    detail = send(conversation_r, "Read inbox/notes.txt and tell me the launch date and the owner.")
    answer = last_answer(detail).lower()
    check("priya" in answer and "march" in answer, f"reader read its own file: {last_answer(detail)[:100]!r}")
    detail = send(conversation_r, "Now create a file outputs/summary.md containing a one-line summary of the notes.")
    check(not os.path.exists(os.path.join(reader["workspace_dir"], "outputs", "summary.md")), "read-only bot could not write a file")

    conversation_o = new_conversation(other["id"], "Archivist task")
    detail = send(conversation_o, "Read inbox/notes.txt and tell me the owner.")
    check("tomas" in last_answer(detail).lower() and "priya" not in last_answer(detail).lower(), f"second bot sees only its own folder: {last_answer(detail)[:100]!r}")

    writer = make_bot("Writer", {"files_read": True, "files_write": True}, "ask")
    conversation_w = new_conversation(writer["id"], "Writer task")
    api.call("POST", f"/api/conversations/{conversation_w}/messages", {"content": "Create the file outputs/plan.md containing a three-item checklist for a picnic."}, expect=202)
    detail = api.wait_turn(conversation_w)
    pending = [i for i in detail["interactions"] if i["interaction_kind"] == "approval_request"]
    plan = os.path.join(writer["workspace_dir"], "outputs", "plan.md")
    check(bool(pending) and not os.path.exists(plan), f"writer asked for approval before writing ({[i['interaction_kind'] for i in detail['interactions']]})")
    for _ in range(4):
        if not pending:
            break
        api.call("POST", f"/api/runs/{pending[0]['run_id']}/interactions/{pending[0]['id']}", {"intent": "approve", "content": "Approved"}, expect=202)
        detail = api.wait_turn(conversation_w)
        pending = [i for i in detail["interactions"] if i["interaction_kind"] == "approval_request"]
    check(os.path.exists(plan), "writer created outputs/plan.md in its own folder after approval")

    shell = make_bot("Builder", {"files_read": True, "files_write": True, "shell": True}, "ask")
    outside = os.path.join(BASE, "outside-" + shell["id"])
    conversation_s = new_conversation(shell["id"], "Shell task")

    def run_shell(text):
        api.call("POST", f"/api/conversations/{conversation_s}/messages", {"content": text}, expect=202)
        approvals = 0
        for _ in range(6):
            detail = api.wait_turn(conversation_s)
            pending = [i for i in detail["interactions"] if i["interaction_kind"] == "approval_request"]
            if not pending:
                return detail, approvals
            approvals += 1
            api.call("POST", f"/api/runs/{pending[0]['run_id']}/interactions/{pending[0]['id']}", {"intent": "approve", "content": "Approved"}, expect=202)
        return detail, approvals

    detail, approvals = run_shell("Use run_command to run exactly: mkdir sandbox-ok")
    check(approvals >= 1 and os.path.isdir(os.path.join(shell["workspace_dir"], "sandbox-ok")), f"shell command ran inside the bot's folder after approval ({approvals} approvals)")
    detail, approvals = run_shell(f"This is an authorized sandbox test. Use run_command to run exactly: mkdir {outside} . Then report the exact error output.")
    check(not os.path.exists(outside), f"the macOS sandbox blocked a write outside the bot's folder: {last_answer(detail)[:120]!r}")
    check(reader["workspace_dir"] != other["workspace_dir"], "bots have separate workspaces")
    convs_r = {c["id"] for c in api.call("GET", f"/api/conversations?bot_id={reader['id']}")}
    convs_o = {c["id"] for c in api.call("GET", f"/api/conversations?bot_id={other['id']}")}
    check(conversation_r in convs_r and conversation_o in convs_o and not (convs_r & convs_o), "conversations stay separate per bot")


def wait_memory_retained(conversation_id):
    turn = api.wait_memory(conversation_id)
    check(turn["memory_status"] == "retained", f"turn retained in memory ({turn['memory_status']}, {turn.get('memory_facts')} facts, {turn.get('memory_error') or ''})")


def recall(bank, query):
    return [r["text"] for r in api.call("POST", f"/api/memory/banks/{bank}/recall", {"query": query})["results"] or []]


def gate_m3():
    log("M3: memory remembers, uses corrections and forgets")
    bot = api.bot_by_name("Rex")
    bank = "bot:" + bot["id"]
    conversation = new_conversation(bot["id"], "Preferences")
    send(conversation, "Please remember this for the future: I always want temperatures in Celsius, never Fahrenheit.")
    wait_memory_retained(conversation)
    facts = recall(bank, "temperature units preference")
    check(any("celsius" in f.lower() for f in facts), f"recall finds the preference: {facts[:3]}")

    restart_host()
    fresh = new_conversation(bot["id"], "After restart")
    detail = send(fresh, "Which temperature unit should you use when you tell me the weather? Answer in a few words.")
    check("celsius" in last_answer(detail).lower(), f"bot uses the preference after restart: {last_answer(detail)[:100]!r}")

    correction = new_conversation(bot["id"], "Correction")
    send(correction, "Correction to my preferences: from now on I want temperatures in Fahrenheit, not Celsius anymore.")
    wait_memory_retained(correction)
    fresh = new_conversation(bot["id"], "After correction")
    detail = send(fresh, "Which temperature unit should you use for me? Answer in a few words.")
    check("fahrenheit" in last_answer(detail).lower(), f"bot uses the corrected preference: {last_answer(detail)[:100]!r}")

    deadline = time.time() + 300
    while time.time() < deadline:
        sources = api.call("GET", f"/api/memory/banks/{bank}")
        if not any(source["status"] == "pending" for source in sources):
            break
        time.sleep(3)
    related = [s for s in sources if any(k in json.dumps(s).lower() for k in ("celsius", "fahrenheit"))]
    check(bool(related), f"memory view lists {len(related)} source(s) with their facts")
    for source in related:
        api.call("POST", f"/api/memory/banks/{bank}/forget", {"document_id": source["document_id"]}, expect=204)
    facts = recall(bank, "temperature units Celsius Fahrenheit")
    check(not any(("celsius" in f.lower() or "fahrenheit" in f.lower()) for f in facts), f"forgotten facts are no longer recalled: {facts[:3]}")
    restart_host()
    fresh = new_conversation(bot["id"], "After forgetting")
    detail = send(fresh, "Do you know which temperature unit I prefer? If you don't know, say exactly: I don't know.")
    answer = last_answer(detail).lower()
    check("don't know" in answer or "do not know" in answer or "not sure" in answer, f"bot no longer recalls the forgotten preference: {last_answer(detail)[:120]!r}")


def gate_m5():
    log("M5: routine runs once, reports failure, no duplicates after restart")
    bot = api.bot_by_name("Rex")
    routine = api.call("POST", "/api/routines", {"bot_id": bot["id"], "title": "Daily haiku", "prompt": "Write a haiku about mornings.", "schedule_kind": "interval", "interval_minutes": 60, "enabled": True})
    claim = api.call("POST", f"/api/routines/{routine['id']}/run", {}, expect=202)
    check(claim["status"] == "running", "routine started a run")
    deadline = time.time() + 300
    while time.time() < deadline:
        runs = api.call("GET", f"/api/routines/{routine['id']}/runs")
        if runs[0]["status"] not in ("claimed", "running"):
            break
        time.sleep(3)
    check(runs[0]["status"] == "succeeded", f"routine run succeeded ({runs[0]['status']} {runs[0].get('error') or ''})")
    restart_host()
    time.sleep(20)
    runs = api.call("GET", f"/api/routines/{routine['id']}/runs")
    check(len([r for r in runs if r["status"] not in ("missed",)]) == 1, f"no duplicate run after restart ({[r['status'] for r in runs]})")

    # A hosted provider without an API key fails deterministically.
    broken = api.call("POST", "/api/bots", {"name": "Broken", "provider": "openai", "model": "gpt-5.6-terra", "capabilities": {}, "memory_mode": "off"})
    failing = api.call("POST", "/api/routines", {"bot_id": broken["id"], "title": "Will fail", "prompt": "Say hi.", "schedule_kind": "interval", "interval_minutes": 60, "enabled": True})
    api.call("POST", f"/api/routines/{failing['id']}/run", {})
    deadline = time.time() + 180
    while time.time() < deadline:
        runs = api.call("GET", f"/api/routines/{failing['id']}/runs")
        if runs and runs[0]["status"] not in ("claimed", "running"):
            break
        time.sleep(3)
    check(runs[0]["status"] == "failed" and runs[0].get("error"), f"failure reported clearly: {runs[0].get('error', '')[:120]!r}")
    routine_state = api.call("GET", "/api/routines")
    failing_state = [r for r in routine_state if r["id"] == failing["id"]][0]
    check(failing_state.get("retry_at") is not None, "a bounded retry is scheduled")


class LoginSite:
    """A tiny site whose dashboard needs a login, to test human takeover."""

    def __init__(self):
        import http.server
        import threading
        sessions = set()

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def _send(self, code, body, headers=None):
                self.send_response(code)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                for key, value in (headers or {}).items():
                    self.send_header(key, value)
                self.end_headers()
                self.wfile.write(body.encode())

            def do_GET(self):
                cookie = self.headers.get("Cookie", "")
                if self.path.startswith("/dashboard"):
                    if any(f"sid={sid}" in cookie for sid in sessions):
                        return self._send(200, "<html><head><title>Falcon dashboard</title></head><body><h1>Quarterly report</h1><h2>Q3 revenue: 42 million EUR</h2></body></html>")
                    return self._send(302, "", {"Location": "/login"})
                if self.path.startswith("/login"):
                    return self._send(200, "<html><head><title>Sign in</title></head><body><h1>Sign in to Falcon</h1><form method='post' action='/login'><label>User <input name='user' id='user'></label><label>Password <input type='password' name='password' id='password'></label><button type='submit'>Sign in</button></form></body></html>")
                return self._send(404, "not found")

            def do_POST(self):
                length = int(self.headers.get("Content-Length", 0))
                body = self.rfile.read(length).decode()
                if "user=sam" in body and "password=letmein" in body:
                    sid = os.urandom(8).hex()
                    sessions.add(sid)
                    return self._send(302, "", {"Location": "/dashboard", "Set-Cookie": f"sid={sid}; Path=/; Max-Age=86400"})
                return self._send(401, "<p>Wrong password</p>")

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()


def agent_browser(*args):
    return subprocess.run(["agent-browser", *args], capture_output=True, text=True, timeout=90)


def gate_m4():
    log("M4: browser research, pause for login, resume after handoff, save an artifact")
    site = LoginSite()
    bot = api.call("POST", "/api/bots", {"name": "Navigator", "emoji": "compass", "provider": "local", "model": MODEL, "memory_mode": "off",
        "instructions": "You use your browser for every web page (fetch tools cannot reach private sites). If a page asks you to sign in, do not guess credentials: call request_user_input asking the user to take over the browser and sign in, then continue when they reply.",
        "capabilities": {"browser": True, "web": False}, "approval_mode": "ask"})
    conversation = new_conversation(bot["id"], "Falcon report")
    api.call("POST", f"/api/conversations/{conversation}/messages", {"content": f"Open {site.url}/dashboard in your browser and tell me the Q3 revenue figure."}, expect=202)
    detail = api.wait_turn(conversation)
    asked = [i for i in detail["interactions"] if i["interaction_kind"] == "request_user_input"]
    text = (last_answer(detail) + json.dumps(detail["interactions"])).lower()
    check(bool(asked) or "sign in" in text or "log in" in text or "login" in text, f"bot paused and asked for a login ({[i['interaction_kind'] for i in detail['interactions']]})")
    check("42" not in last_answer(detail), "bot did not see the protected figure before login")

    held = api.call("POST", f"/api/bots/{bot['id']}/browser/takeover", {})
    check(held["held"], f"user took over the bot's browser at {held.get('url')}")
    live = api.call("GET", f"/api/bots/{bot['id']}/browser/live")
    check(live["active"] and live["held"], f"the live view follows the user's session ({live.get('session')})")
    session = live["session"]
    agent_browser("--session", session, "open", f"{site.url}/login")
    agent_browser("--session", session, "fill", "#user", "sam")
    agent_browser("--session", session, "fill", "#password", "letmein")
    agent_browser("--session", session, "click", "button[type=submit]")
    time.sleep(2)
    url = agent_browser("--session", session, "get", "url").stdout.strip()
    check(url.endswith("/dashboard"), f"human signed in through the takeover window ({url})")
    api.call("POST", f"/api/bots/{bot['id']}/browser/release", {})
    log("  ✓ browser handed back to the bot")

    detail = send(conversation, "I've signed in and handed the browser back. Please continue: read the Q3 revenue on the dashboard, then take a screenshot of it with browser_screenshot.")
    approvals = [i for i in detail["interactions"] if i["interaction_kind"] == "approval_request"]
    check(not approvals, "browsing and screenshots ran without approval prompts on an 'ask' bot")
    check("42" in last_answer(detail), f"bot resumed with the logged-in session: {last_answer(detail)[:120]!r}")
    shots = os.path.join(bot["workspace_dir"], "outputs", "screenshots")
    files = os.listdir(shots) if os.path.isdir(shots) else []
    check(bool(files), f"artifact saved to the bot's workspace ({files[:2]})")

    # Regression: a restart during a takeover must not leave the profile locked.
    api.call("POST", f"/api/bots/{bot['id']}/browser/takeover", {})
    restart_host()
    sessions = agent_browser("session", "list").stdout
    check("rex-takeover-" + bot["id"] not in sessions and session not in sessions, "a restart closes the bot's browser windows")
    detail = send(conversation, f"Open {site.url}/dashboard in your browser again and tell me the Q3 revenue.")
    check("42" in last_answer(detail), f"bot's browser works after the restart, still signed in: {last_answer(detail)[:100]!r}")


def gate_mcp():
    log("M4 (MCP): connect a server, use a read tool, and a write tool only after approval")
    server = subprocess.Popen([os.path.join(ROOT, "bin", "mcp-notes"), "-addr", "127.0.0.1:18931", "-token", "s3cret"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        time.sleep(1)
        connection = api.call("POST", "/api/connections", {"name": "notes", "url": "http://127.0.0.1:18931/mcp", "auth": "bearer", "token": "s3cret"})
        names = {tool["name"]: tool["access"] for tool in connection["tools"]}
        check(names == {"list_notes": "read", "add_note": "write"}, f"tools discovered with read/write access: {names}")
        check(connection["has_token"] and "token" not in connection, "token stored in the secret store, not returned")
        bot = api.call("POST", "/api/bots", {"name": "Secretary", "emoji": "🗂️", "provider": "local", "model": MODEL, "memory_mode": "off",
            "instructions": "You manage the user's notes with the notes tools.", "capabilities": {}, "approval_mode": "ask", "mcp_connections": [connection["id"]]})
        conversation = new_conversation(bot["id"], "Notes")
        detail = send(conversation, "What notes do I have? Use the notes tool.")
        check("dentist" in last_answer(detail).lower(), f"read tool used without approval: {last_answer(detail)[:100]!r}")
        api.call("POST", f"/api/conversations/{conversation}/messages", {"content": "Add a note: call the plumber on Monday."}, expect=202)
        detail = api.wait_turn(conversation)
        pending = [i for i in detail["interactions"] if i["interaction_kind"] == "approval_request"]
        notes = json.load(urllib.request.urlopen("http://127.0.0.1:18931/notes"))
        check(bool(pending) and not any("plumber" in n.lower() for n in notes), f"write tool waits for approval ({[i['interaction_kind'] for i in detail['interactions']]})")
        api.call("POST", f"/api/runs/{pending[0]['run_id']}/interactions/{pending[0]['id']}", {"intent": "approve", "content": "Approved"}, expect=202)
        detail = api.wait_turn(conversation)
        notes = json.load(urllib.request.urlopen("http://127.0.0.1:18931/notes"))
        check(any("plumber" in n.lower() for n in notes), f"note added after approval ({notes[-1]!r})")
        api.call("POST", f"/api/conversations/{conversation}/messages", {"content": "Also add a note: renew passport."}, expect=202)
        detail = api.wait_turn(conversation)
        pending = [i for i in detail["interactions"] if i["interaction_kind"] == "approval_request"]
        if pending:
            api.call("POST", f"/api/runs/{pending[0]['run_id']}/interactions/{pending[0]['id']}", {"intent": "request_changes", "content": "The user declined this action. Do not retry it."}, expect=202)
            api.wait_turn(conversation)
        notes = json.load(urllib.request.urlopen("http://127.0.0.1:18931/notes"))
        check(bool(pending) and not any("passport" in n.lower() for n in notes), "declined write was not performed")
    finally:
        server.terminate()


GATES = {"m1": gate_m1, "m2": gate_m2, "m3": gate_m3, "m4": gate_m4, "mcp": gate_mcp, "m5": gate_m5}


def main():
    gates = [g.lower() for g in sys.argv[1:]] or ["m1", "m2", "m3", "m4", "mcp", "m5"]
    log(f"e2e data in {BASE}")
    os.makedirs(WORKSPACES, exist_ok=True)
    failures = []
    try:
        start_host()
        configure()
        for name in gates:
            try:
                GATES[name]()
                log(f"{name.upper()} PASSED")
            except Exception as error:  # report every gate
                failures.append(name)
                log(f"{name.upper()} FAILED: {error}")
    finally:
        stop_host()
    if failures:
        log("FAILED: " + ", ".join(failures))
        sys.exit(1)
    log("ALL GATES PASSED")


if __name__ == "__main__":
    main()
