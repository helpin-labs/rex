#!/usr/bin/env python3
"""Small client for the Rex host API, used by the end-to-end checks.

Reads the API URL and token from <data dir>/connection.json
(REX_DATA_DIR, default ~/Library/Application Support/ai.rex.desktop).
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

DATA_DIR = os.environ.get("REX_DATA_DIR") or os.path.expanduser("~/Library/Application Support/ai.rex.desktop")


def connection():
    with open(os.path.join(DATA_DIR, "connection.json")) as handle:
        return json.load(handle)


def call(method, path, body=None, expect=None):
    conn = connection()
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(conn["url"] + path, data=data, method=method)
    request.add_header("Authorization", "Bearer " + conn["token"])
    if data is not None:
        request.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(request, timeout=600) as response:
            payload = response.read()
            status = response.status
    except urllib.error.HTTPError as error:
        payload = error.read()
        status = error.code
    value = json.loads(payload) if payload else None
    if expect is not None and status != expect:
        raise RuntimeError(f"{method} {path} -> {status}: {value}")
    if expect is None and status >= 400:
        raise RuntimeError(f"{method} {path} -> {status}: {value}")
    return value


def wait_ready(timeout=90):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if call("GET", "/api/status").get("runtime_ready"):
                return
        except Exception:
            pass
        time.sleep(1)
    raise RuntimeError("host did not become ready")


def wait_turn(conversation_id, timeout=600):
    """Wait until the newest turn of a conversation leaves 'working' or the
    run pauses for approval/input. Returns the conversation detail."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        detail = call("GET", f"/api/conversations/{conversation_id}")
        turns = detail.get("turns") or []
        pending = detail.get("interactions") or []
        if turns and turns[-1]["status"] != "working":
            return detail
        if pending:
            return detail
        time.sleep(2)
    raise RuntimeError("turn did not settle in time")


def wait_memory(conversation_id, timeout=600):
    deadline = time.time() + timeout
    while time.time() < deadline:
        turns = call("GET", f"/api/conversations/{conversation_id}")["turns"]
        if turns and turns[-1]["memory_status"] not in ("pending",):
            return turns[-1]
        time.sleep(3)
    raise RuntimeError("memory did not settle in time")


def bot_by_name(name):
    for bot in call("GET", "/api/bots"):
        if bot["name"] == name:
            return bot
    raise KeyError(name)


def show(conversation_id):
    detail = call("GET", f"/api/conversations/{conversation_id}")
    for run in detail["runs"]:
        print("RUN", run["id"], run["status"], run.get("pause_reason"), run.get("error_message") or "")
    for message in detail["messages"]:
        print(f"[{message['role']}] {message['content'][:600]}")
    for interaction in detail["interactions"]:
        print("PENDING", interaction["interaction_kind"], interaction.get("title"), interaction.get("summary"))
    for turn in detail["turns"]:
        print("TURN", turn["status"], "memory:", turn["memory_status"], turn.get("memory_error") or "", turn.get("memory_facts"))
    return detail


if __name__ == "__main__":
    command, *args = sys.argv[1:] or ["status"]
    if command == "status":
        print(json.dumps(call("GET", "/api/status"), indent=2))
    elif command == "show":
        show(args[0])
    elif command == "wait":
        wait_turn(args[0])
        show(args[0])
    elif command == "send":
        call("POST", f"/api/conversations/{args[0]}/messages", {"content": args[1]}, expect=202)
        wait_turn(args[0])
        show(args[0])
    else:
        raise SystemExit(f"unknown command {command}")
