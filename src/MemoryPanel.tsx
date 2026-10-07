import { useCallback, useEffect, useRef, useState } from "react";
import { Lock, MessageSquare, Plus, StickyNote, Trash2, Users, X } from "lucide-react";
import { api } from "./api";
import type { Bot, MemoryOverview, MemorySource } from "./types";
import { Avatar, errorMessage, relativeTime } from "./ui";

// Where a bot's memories live: its own bank, or the shared bank that other
// bots read and write too. Rex's memory is a private bank of the main bot.
export type MemoryHome = { bank: string; shared: boolean; sharedWith: Bot[] };

export function memoryHome(bot: Bot, overview: MemoryOverview | null, bots: Bot[]): MemoryHome | null {
  if (bot.memory_mode === "off" || !overview) return null;
  const bank = overview.banks.find(item => item.bot_ids.includes(bot.id));
  if (!bank) return null;
  const shared = bank.bank === "shared";
  return { bank: bank.bank, shared, sharedWith: shared ? bots.filter(item => item.id !== bot.id && bank.bot_ids.includes(item.id)) : [] };
}

/** A short label for where memories are kept, e.g. "Shared with 2 bots". */
export function memoryWhereLabel(home: MemoryHome | null, bot: Bot): string {
  if (!home) return "";
  if (!home.shared) return `Only ${bot.name}`;
  return home.sharedWith.length ? `Shared with ${home.sharedWith.length} ${home.sharedWith.length === 1 ? "bot" : "bots"}` : "Shared memory";
}

/** A readable name for the bank a recalled memory came from. */
export function bankLabel(bank: string, overview: MemoryOverview | null): string {
  if (bank === "shared") return "Shared memory";
  return overview?.banks.find(item => item.bank === bank)?.label || "Memory";
}

// MemoryPanel shows what the bot remembers, where it is kept, and lets the
// user teach or forget something without leaving the conversation.
export function MemoryPanel({ bot, home, focusDocument, onClose }: { bot: Bot; home: MemoryHome; focusDocument?: string; onClose: () => void }) {
  const [sources, setSources] = useState<MemorySource[]>([]);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const focusRef = useRef<HTMLLIElement>(null);

  const load = useCallback(() => api.memorySources(home.bank).then(list => setSources(list || [])).catch(caught => setError(errorMessage(caught))), [home.bank]);
  useEffect(() => { void load(); const timer = window.setInterval(() => void load(), 4000); return () => window.clearInterval(timer); }, [load]);
  useEffect(() => { focusRef.current?.scrollIntoView({ block: "center" }); }, [focusDocument, sources.length]);

  const teach = async () => {
    if (!note.trim()) return;
    try { await api.addMemoryNote(home.bank, note.trim()); setNote(""); await load(); } catch (caught) { setError(errorMessage(caught)); }
  };
  const forget = async (source: MemorySource) => {
    if (!window.confirm("Forget everything learned from this?")) return;
    try { await api.forget(home.bank, source.document_id); await load(); } catch (caught) { setError(errorMessage(caught)); }
  };

  const withFacts = sources.filter(source => source.facts?.length);
  const learning = sources.filter(source => source.status === "pending").length;
  const factCount = withFacts.reduce((sum, source) => sum + (source.facts?.filter(fact => !fact.replaced).length || 0), 0);

  return <aside className="drawer memory-drawer">
    <header className="drawer-header">
      <div><small className="eyebrow">Memory</small><h3>{factCount} {factCount === 1 ? "thing" : "things"} remembered</h3></div>
      <button type="button" className="icon-button ghost" onClick={onClose} aria-label="Close memory"><X size={16} /></button>
    </header>
    <div className={`memory-home ${home.shared ? "shared" : ""}`}>
      {home.shared ? <Users size={15} /> : <Lock size={15} />}
      <div>
        <strong>{home.shared ? "Shared memory" : `Only ${bot.name}`}</strong>
        <small>{home.shared
          ? home.sharedWith.length ? "Everything here is also read and added to by:" : "No other bot uses shared memory yet."
          : "Private to this bot. Other bots never see it."}</small>
        {home.shared && home.sharedWith.length > 0 && <div className="memory-sharers">{home.sharedWith.map(item => <span key={item.id} className="memory-sharer"><Avatar bot={item} size={22} />{item.name}</span>)}</div>}
      </div>
    </div>
    <div className="memory-teach">
      <input value={note} onChange={event => setNote(event.target.value)} onKeyDown={event => { if (event.key === "Enter") void teach(); }} placeholder={`Teach ${bot.name} something`} />
      <button type="button" className="icon-button" onClick={teach} disabled={!note.trim()} aria-label="Remember this"><Plus size={15} /></button>
    </div>
    {error && <div className="inline-error">{error}</div>}
    {learning > 0 && <p className="muted small memory-learning">Learning from {learning} recent {learning === 1 ? "message" : "messages"}…</p>}
    <ul className="memory-facts">
      {withFacts.length === 0 && <li className="muted small">Nothing yet. Durable things you mention, like preferences, people and plans, show up here after each answer.</li>}
      {withFacts.map(source => <li key={source.document_id} ref={source.document_id === focusDocument ? focusRef : undefined} className={source.document_id === focusDocument ? "focused" : ""}>
        <div className="memory-source">
          {source.kind === "chat" ? <MessageSquare size={11} /> : <StickyNote size={11} />}
          <span>{source.kind === "chat" ? "From a message" : source.kind === "note" ? "Taught" : "Saved by a bot"} · {relativeTime(source.created_at)}</span>
          <button type="button" className="icon-button ghost tiny" title="Forget" onClick={() => forget(source)}><Trash2 size={11} /></button>
        </div>
        {source.facts!.map(fact => <p key={fact.id} className={fact.replaced ? "replaced" : ""}>{fact.text}</p>)}
      </li>)}
    </ul>
  </aside>;
}
