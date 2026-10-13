#!/usr/bin/env python3
"""Operational-memory smoke with real sidecars, local learning and isolated data.

Build sidecars first. By default, file-task responses use a deterministic provider
and learning uses openai/gpt-oss-20b on the local model server. Set REX_E2E_MODEL
to test task execution with a real local model whose loaded context fits the tools.
Uses the existing e2e host lifecycle; leaves live Rex profiles untouched.
"""
import os
import json
import threading
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import time
import urllib.parse

os.environ.setdefault("REX_E2E_MODEL", "fixture-task")
os.environ.setdefault("REX_E2E_EXTRACTION_MODEL", "openai/gpt-oss-20b")
import e2e as gate


class TaskProvider:
    """Deterministic file-task model; learning uses the actual local model."""
    def __init__(self):
        local_url = gate.LOCAL_URL
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                data = [{"id": "fixture-task", "context_length": 32768, "loaded_context_length": 32768, "max_completion_tokens": 4096}, {"id": gate.EXTRACTION, "loaded_context_length": 4096}]
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"data": data}).encode())

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                if body.get("model") != "fixture-task":
                    request = urllib.request.Request(local_url + "/chat/completions", data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
                    with urllib.request.urlopen(request, timeout=130) as response:
                        value = response.read()
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.end_headers()
                    self.wfile.write(value)
                    return
                messages = body.get("messages", [])
                latest = next((item.get("content", "") for item in reversed(messages) if item.get("role") == "user"), "")
                missing = "Read unavailable.txt" in str(latest)
                call_id = "fixture-missing" if missing else "fixture-progress"
                completed = any(item.get("tool_call_id") == call_id for item in messages)
                content = "The requested file was missing; no contents were verified." if missing else "progress.txt confirms 120 completed rows and 17 unverified rows, manifest proof-03."
                message = {"role": "assistant", "content": content if completed else ""}
                if not completed:
                    message["tool_calls"] = [{"id": call_id, "type": "function", "function": {"name": "read_files", "arguments": json.dumps({"files": [{"path": "unavailable.txt" if missing else "progress.txt"}]})}}]
                choice = {"index": 0, "message": message, "finish_reason": "stop" if completed else "tool_calls"}
                response = {"id": "fixture", "object": "chat.completion", "model": "fixture-task", "choices": [choice], "usage": {"prompt_tokens": 100, "completion_tokens": 30, "total_tokens": 130}}
                self.send_response(200)
                if body.get("stream"):
                    self.send_header("Content-Type", "text/event-stream")
                    self.end_headers()
                    choice["delta"] = choice.pop("message")
                    self.wfile.write(("data: " + json.dumps(response) + "\n\ndata: [DONE]\n\n").encode())
                else:
                    self.send_header("Content-Type", "application/json")
                    self.end_headers()
                    self.wfile.write(json.dumps(response).encode())
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.server.server_port}/v1"

    def close(self):
        self.server.shutdown()
        self.server.server_close()


def wait_learning(bank, minimum, timeout=180):
    deadline = time.monotonic() + timeout
    path = "/api/memory/banks/" + urllib.parse.quote(bank, safe="") + "/learning"
    while time.monotonic() < deadline:
        state = gate.api.call("GET", path)
        failed = [item for item in state["episodes"] if item["status"] == "failed"]
        assert not failed, "learning failed: " + "; ".join(item.get("error", "") for item in failed)
        indexed = [item for item in state["episodes"] if item["status"] in ("indexed", "no_lesson")]
        if len(indexed) >= minimum:
            return state
        time.sleep(1)
    raise AssertionError("learning queue did not finish within its bounded deadline")


def main():
    gate.log("operational-memory data: " + gate.BASE)
    os.makedirs(gate.WORKSPACES, exist_ok=True)
    provider = TaskProvider() if gate.MODEL == "fixture-task" else None
    if provider:
        gate.LOCAL_URL = provider.url
    try:
        gate.start_host()
        gate.configure()
        bot = gate.api.call("POST", "/api/bots", {
            "name": "Operational memory verifier", "emoji": "bot", "provider": "local", "model": gate.MODEL,
            "instructions": "Read only the requested file once. Report exact progress or the file error. Do not create files or repeat failed reads.",
            "memory_mode": "private", "retro_off": True,
            "capabilities": {"files_read": True, "memory": True}, "approval_mode": "auto",
        })
        bank = "bot:" + bot["id"]
        prefix = "/api/memory/banks/" + urllib.parse.quote(bank, safe="")
        os.makedirs(bot["workspace_dir"], exist_ok=True)
        with open(os.path.join(bot["workspace_dir"], "progress.txt"), "w") as handle:
            handle.write("Batch 03: 120 rows completed; 17 rows remain unverified. Manifest ID: proof-03.\n")
        conversation = gate.new_conversation(bot["id"], "Operational memory smoke")
        first = gate.send(conversation, "Read progress.txt and report the completed and unverified row counts, with its manifest ID.")
        gate.check(first["turns"][-1]["status"] == "settled", "file task settled through the real runtime")
        second = gate.send(conversation, "Read unavailable.txt once. If it is missing, report that failure without inventing any contents.")
        gate.check(second["turns"][-1]["status"] == "settled", "failed file read still completed its report")
        state = wait_learning(bank, 2)
        gate.check(state["context"]["enabled"], "reported task-model limits enable compaction")
        gate.check(all(item.get("model") == gate.EXTRACTION for item in state["episodes"]), "actual learning model recorded")
        history = gate.api.call("POST", prefix + "/history/search", {"query": "progress"})
        gate.check(bool(history["entries"]), "recorded task found through scoped history search")
        failures = gate.api.call("POST", prefix + "/history/search", {"query": "unavailable"})
        gate.check(any(item["kind"] == "tool_failure" for item in failures["entries"]), "failed tool result captured as evidence")
        gate.api.call("POST", prefix + "/consolidate", {})
        state = gate.api.call("GET", prefix + "/learning")
        gate.check(bool(state["lessons"]), "source-backed lessons consolidated")
        gate.restart_host()
        restored = gate.api.call("GET", prefix + "/learning")
        gate.check(restored["consolidation"].get("last_success") == state["consolidation"].get("last_success"), "consolidation cooldown survives restart")
        source = "episode/" + first["turns"][-1]["id"]
        gate.api.call("POST", prefix + "/forget", {"document_id": source})
        try:
            gate.api.call("POST", prefix + "/history/read", {"source_id": source})
            raise AssertionError("forgotten source was readable")
        except RuntimeError as error:
            assert "not found" in str(error).lower(), error
        gate.check(True, "forgetting excludes the canonical source after restart")
        gate.log("OPERATIONAL MEMORY PASSED")
    finally:
        gate.stop_host()
        if provider:
            provider.close()


if __name__ == "__main__":
    main()
