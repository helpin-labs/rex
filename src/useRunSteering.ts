import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, api } from "./api";
import type { RunSteeringCommand } from "./types";

type Pending = { key: string; command_id: string; expected_revision: number; content: string };

// Sends the user's messages to a working run as steering instructions, and
// keeps the latest one's receipt: waiting for a safe boundary, applied, or
// not applied. A retried send reuses its command ID, so it is never applied
// twice; a stale revision (409) starts over with a fresh read.
export function useRunSteering(conversationId: string, runId: string | undefined, live: boolean) {
  const key = `${conversationId}:${runId ?? ""}`;
  const [state, setState] = useState<{ key: string; available: boolean; receipt: RunSteeringCommand | null }>({ key: "", available: false, receipt: null });
  const pending = useRef<Pending | null>(null);
  const sending = useRef(false);
  const currentKey = useRef(key);
  currentKey.current = key;
  const receipt = state.key === key ? state.receipt : null;

  useEffect(() => {
    if (!runId) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      try {
        const commands = await api.runSteering(conversationId, runId);
        if (!disposed) setState(previous => {
          const latest = commands.at(-1) ?? null;
          const kept = previous.key === key ? previous.receipt : null;
          // A receipt from a send can be newer than the list just read.
          const newer = kept && (!latest || kept.revision > latest.revision || (kept.revision === latest.revision && kept.state !== "accepted"));
          return { key, available: true, receipt: newer ? kept : latest };
        });
      } catch { /* keep the last receipt through a connection hiccup */ }
      // Poll while the bot works; when it stops, this effect runs once more
      // and reads the final state.
      if (!disposed && live) timer = setTimeout(() => void refresh(), 3000);
    };
    void refresh();
    return () => { disposed = true; clearTimeout(timer); };
  }, [conversationId, runId, key, live]);

  const send = useCallback(async (content: string) => {
    if (!runId || !live) throw new Error("The bot finished; send this as a new message.");
    if (sending.current) throw new Error("An instruction is already being sent.");
    sending.current = true;
    try {
      if (!pending.current || pending.current.key !== key || pending.current.content !== content) {
        const history = await api.runSteering(conversationId, runId);
        if (currentKey.current !== key) throw new Error("The conversation moved to a new run.");
        pending.current = { key, command_id: crypto.randomUUID(), expected_revision: history.at(-1)?.revision ?? 0, content };
      }
      const { key: _key, ...command } = pending.current;
      try {
        const result = await api.steerRun(conversationId, runId, command);
        if (currentKey.current === key) setState({ key, available: true, receipt: result });
        pending.current = null;
      } catch (caught) {
        if (caught instanceof ApiError && caught.status === 409 && pending.current?.command_id === command.command_id) pending.current = null;
        throw caught;
      }
    } finally { sending.current = false; }
  }, [conversationId, runId, live, key]);

  return { canSteer: !!runId && live && state.key === key && state.available, receipt, send };
}

export function steeringReceiptText(command: RunSteeringCommand): string {
  if (command.state === "accepted") return "Instruction sent. The bot will read it at its next step.";
  if (command.state === "applied") return "The bot has your instruction.";
  return "The bot finished before it could read your instruction. Send it again as a new message if it still applies.";
}
