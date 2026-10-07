import { useEffect, useRef, useState } from "react";
import { api } from "./api";
import type { AttentionItem, Bot } from "./types";

// Polls what is waiting for the user, posts a system notification for each
// new item (shown only while Rex is in the background), and keeps the Dock
// badge and menu bar count in step. Clicking a notification opens its conversation.

const isTauri = () => !!(window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;

async function invoke(command: string, args: Record<string, unknown>) {
  if (!isTauri()) return;
  const { invoke } = await import("@tauri-apps/api/core");
  await invoke(command, args);
}

function plain(text: string, max = 160): string {
  const value = text.replace(/[`*_#>[\]]/g, "").replace(/\s+/g, " ").trim();
  return value.length > max ? `${value.slice(0, max - 1)}…` : value;
}

export function notificationFor(item: AttentionItem, bot: Bot | undefined): { title: string; body: string } {
  const name = bot?.name || "A bot";
  switch (item.kind) {
    case "approval": return { title: `${name} needs your approval`, body: plain(item.detail || item.title || "Open Rex to review it.") };
    case "question": return { title: `${name} has a question`, body: plain(item.detail || item.title || "Open Rex to answer it.") };
    case "reconnect": return { title: `${name} needs you to sign in again`, body: plain(item.detail || "Open Rex and sign in to continue.") };
    case "proposal": return { title: `${name} suggests new instructions`, body: plain(item.detail || "Open Rex to apply or discard the change.") };
    case "missed": return { title: `${item.title || "A routine"} didn't run`, body: plain(`${name} missed it while your Mac was off. Open Rex to run it now or skip it.`) };
    default: return { title: item.title || "Routine finished", body: plain(`${name} · ${item.detail || "Finished"}`) };
  }
}

/** Conversations waiting on the user (routine reports only notify). */
export function waitingConversations(items: AttentionItem[]): Set<string> {
  return new Set(items.filter(item => item.kind !== "routine").map(item => item.conversation_id));
}

export function useAttention({ bots, enabled, onOpen }: { bots: Bot[]; enabled: boolean; onOpen: (conversationId: string) => void }) {
  const [items, setItems] = useState<AttentionItem[]>([]);
  const seen = useRef<Set<string> | null>(null);
  const botsRef = useRef(bots);
  botsRef.current = bots;
  const enabledRef = useRef(enabled);
  enabledRef.current = enabled;
  const openRef = useRef(onOpen);
  openRef.current = onOpen;

  useEffect(() => {
    let cancelled = false;
    const poll = async () => {
      try {
        const next = await api.attention();
        if (cancelled) return;
        // What was already waiting when Rex opened shows in the badge only.
        const first = seen.current === null;
        if (first) seen.current = new Set();
        for (const item of next) {
          if (seen.current!.has(item.id)) continue;
          seen.current!.add(item.id);
          // The app skips the notification while its window is in front.
          if (first || !enabledRef.current) continue;
          const { title, body } = notificationFor(item, botsRef.current.find(bot => bot.id === item.bot_id));
          void invoke("notify", { title, body, conversationId: item.conversation_id }).catch(caught => console.warn("notify failed", caught));
        }
        setItems(next);
      } catch { /* retried on the next poll */ }
    };
    void poll();
    const timer = window.setInterval(poll, 3000);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, []);

  const count = waitingConversations(items).size;
  useEffect(() => { void invoke("set_attention", { count }).catch(() => undefined); }, [count]);

  useEffect(() => {
    if (!isTauri()) return;
    let unlisten: (() => void) | undefined;
    void import("@tauri-apps/api/event").then(({ listen }) => listen<string>("open-conversation", event => openRef.current(event.payload))).then(stop => { unlisten = stop; });
    return () => unlisten?.();
  }, []);

  return items;
}
