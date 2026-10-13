import { useCallback, useEffect, useRef, useState } from "react";
import { Bot as BotIcon, Brain, ChevronDown, CircleAlert, Lock, MessageSquare, Pencil, RotateCcw, Search, Settings as SettingsIcon, StickyNote, Trash2, Users, X } from "lucide-react";
import { OperationalMemory } from "./OperationalMemory";
import { api } from "./api";
import type { Bot, MemoryOverview, MemorySource, MemoryLearning } from "./types";
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

/** Why memories fail to save, in plain words; the raw error stays on hover. */
export function memoryFailureReason(error: string): string {
  const address = /dial tcp ([0-9a-z.-]+(?::\d+)?)/i.exec(error)?.[1] || /https?:\/\/([^/"\s]+)/.exec(error)?.[1];
  if (/connection refused|no such host|dial tcp|i\/o timeout/i.test(error)) return `The memory model${address ? ` at ${address}` : ""} isn't reachable. Start your local model server, or run memory with OpenAI.`;
  if (/401|403|unauthori[sz]ed|invalid api key|incorrect api key/i.test(error)) return "The memory model rejected its key. Check the key in Settings.";
  if (/404|model.*not found|no such model/i.test(error)) return "The memory model wasn't found on the server. Pick another in Settings → Memory.";
  return "Saving memories failed. The details are on hover.";
}

const sourceLabel = (source: MemorySource) => source.kind === "episode" ? source.title : source.kind === "chat" ? "Legacy message" : source.kind === "note" ? "You taught it" : "Saved by the bot";
const sourceIcon = (source: MemorySource) => source.kind === "chat" ? <MessageSquare size={12} /> : source.kind === "note" ? <StickyNote size={12} /> : <BotIcon size={12} />;

// MemoryPanel shows what the bot remembers, where it is kept, and lets the
// user teach, correct or forget something without leaving the conversation.
// When saving memories fails, it says why and offers to try again.
export function MemoryPanel({ bot, home, focusDocument, onClose, onOpenSettings }: { bot: Bot; home: MemoryHome; focusDocument?: string; onClose: () => void; onOpenSettings?: () => void }) {
  const [sources, setSources] = useState<MemorySource[]>([]);
  const [learningState, setLearningState] = useState<MemoryLearning | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [note, setNote] = useState("");
  const [filter, setFilter] = useState("");
  const [editing, setEditing] = useState<{ id: string; text: string } | null>(null);
  const [forgetting, setForgetting] = useState("");
  const [showFailed, setShowFailed] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [error, setError] = useState("");
  const focusRef = useRef<HTMLLIElement>(null);

  const load = useCallback(() => Promise.all([api.memorySources(home.bank), api.memoryLearning(home.bank)]).then(([list, learning]) => { setSources(list || []); setLearningState(learning); setLoaded(true); }).catch(caught => setError(errorMessage(caught))), [home.bank]);
  useEffect(() => { void load(); const timer = window.setInterval(() => void load(), 4000); return () => window.clearInterval(timer); }, [load]);
  useEffect(() => { focusRef.current?.scrollIntoView({ block: "center" }); }, [focusDocument, sources.length]);

  const teach = async () => {
    if (!note.trim()) return;
    try { await api.addMemoryNote(home.bank, note.trim()); setNote(""); await load(); } catch (caught) { setError(errorMessage(caught)); }
  };
  // A correction is a note that replaces the source it corrects.
  const correct = async () => {
    if (!editing?.text.trim()) return;
    try { await api.addMemoryNote(home.bank, editing.text.trim(), editing.id); setEditing(null); await load(); } catch (caught) { setError(errorMessage(caught)); }
  };
  const forget = async (source: MemorySource) => {
    try { await api.forget(home.bank, source.document_id); setForgetting(""); await load(); } catch (caught) { setError(errorMessage(caught)); }
  };
  const retry = async (items: MemorySource[]) => {
    setRetrying(true); setError("");
    try { for (const item of items) if (/^(turn|episode)\//.test(item.document_id)) await api.retryMemory(item.document_id.split("/")[1]); await load(); }
    catch (caught) { setError(errorMessage(caught)); } finally { setRetrying(false); }
  };

  const withFacts = sources.filter(source => source.facts?.length);
  const failed = sources.filter(source => source.status === "failed");
  const learning = sources.filter(source => ["pending", "capture", "queued", "extracting"].includes(source.status)).length;
  const factCount = withFacts.reduce((sum, source) => sum + (source.facts?.filter(fact => !fact.replaced).length || 0), 0);
  const words = filter.trim().toLowerCase();
  const shown = words ? withFacts.filter(source => source.facts!.some(fact => fact.text.toLowerCase().includes(words))) : withFacts;
  const reason = failed[0]?.error ? memoryFailureReason(failed[0].error) : "";

  return <aside className="drawer memory-drawer">
    <header className="drawer-header">
      <div><small className="eyebrow">Memory</small><h3>{factCount} {factCount === 1 ? "thing" : "things"} remembered</h3></div>
      <button type="button" className="icon-button ghost" onClick={onClose} aria-label="Close memory"><X size={16} /></button>
    </header>
    <div className="memory-body">
      <div className={`memory-home ${home.shared ? "shared" : ""}`}>
        {home.shared ? <Users size={14} /> : <Lock size={14} />}
        <div className="grow">
          <strong>{home.shared ? "Shared memory" : `Only ${bot.name}`}</strong>
          <small>{home.shared ? home.sharedWith.length ? "Also read and added to by:" : "No other bot uses shared memory yet." : "Private to this bot. Other bots never see it."}</small>
          {home.shared && home.sharedWith.length > 0 && <div className="memory-sharers">{home.sharedWith.map(item => <span key={item.id} className="memory-sharer"><Avatar bot={item} size={18} />{item.name}</span>)}</div>}
        </div>
      </div>

      <OperationalMemory bank={home.bank} state={learningState} onRefresh={load} />
      {failed.length > 0 && <div className="memory-alert" role="alert">
        <CircleAlert size={15} />
        <div className="grow">
          <strong>{failed.length} {failed.length === 1 ? "message wasn't" : "messages weren't"} remembered</strong>
          <small title={failed[0]?.error}>{reason}</small>
          <div className="row memory-alert-actions">
            <button type="button" className="button secondary small" disabled={retrying} onClick={() => void retry(failed)}><RotateCcw size={12} className={retrying ? "spin" : ""} />{retrying ? "Retrying…" : "Retry all"}</button>
            {onOpenSettings && <button type="button" className="button secondary small" onClick={onOpenSettings}><SettingsIcon size={12} />Memory settings</button>}
          </div>
        </div>
      </div>}

      <div className="memory-teach">
        <textarea rows={2} value={note} onChange={event => setNote(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void teach(); } }} placeholder={`Teach ${bot.name} something, e.g. “Verify listing facts before reporting a match”`} />
        <div className="row"><small className="muted">Enter to save</small><span className="spacer" /><button type="button" className="button primary small" onClick={teach} disabled={!note.trim()}>Remember</button></div>
      </div>
      {error && <div className="inline-error"><span>{error}</span></div>}
      {learning > 0 && <p className="muted small memory-learning"><Brain size={12} className="pulse" /> Learning from {learning} recorded {learning === 1 ? "task" : "tasks"}…</p>}
      {withFacts.length > 4 && <label className="memory-filter"><Search size={13} /><input value={filter} onChange={event => setFilter(event.target.value)} placeholder="Filter memories" aria-label="Filter memories" /></label>}

      <ul className="memory-facts">
        {loaded && withFacts.length === 0 && <li className="memory-empty"><Brain size={18} /><strong>Nothing remembered yet</strong><small>Task outcomes, successes, failures and corrections are learned here. Saved task history remains available even when learning is delayed.</small></li>}
        {words && shown.length === 0 && withFacts.length > 0 && <li className="memory-empty"><small>No memories match “{filter}”.</small></li>}
        {shown.map(source => {
          const live = source.facts!.filter(fact => !fact.replaced);
          return <li key={source.document_id} ref={source.document_id === focusDocument ? focusRef : undefined} className={`memory-card ${source.document_id === focusDocument ? "focused" : ""}`}>
            <div className="memory-source">
              {sourceIcon(source)}<span>{sourceLabel(source)} · {relativeTime(source.created_at)}</span>
              {forgetting !== source.document_id && editing?.id !== source.document_id && <span className="memory-actions">
                <button type="button" className="icon-button ghost tiny" title="Correct" aria-label="Correct" onClick={() => setEditing({ id: source.document_id, text: live.map(fact => fact.text).join("\n") })}><Pencil size={11} /></button>
                <button type="button" className="icon-button ghost tiny" title="Forget" aria-label="Forget" onClick={() => setForgetting(source.document_id)}><Trash2 size={11} /></button>
              </span>}
            </div>
            {editing?.id === source.document_id ? <div className="memory-edit">
              <textarea rows={Math.min(6, live.length + 1)} value={editing.text} autoFocus onChange={event => setEditing({ ...editing, text: event.target.value })} />
              <div className="row"><small className="muted">Saved as something you taught; this replaces it.</small><span className="spacer" />
                <button type="button" className="link-button" onClick={() => setEditing(null)}>Cancel</button>
                <button type="button" className="button primary small" disabled={!editing.text.trim()} onClick={() => void correct()}>Save</button></div>
            </div> : <ul className="memory-fact-list">{source.facts!.map(fact => <li key={fact.id} className={fact.replaced ? "replaced" : ""} title={fact.replaced ? "Replaced by a newer memory" : undefined}>{fact.evidence_kind && <small className="muted">{fact.evidence_kind} · {fact.evidence_kind === "correction" ? "user correction" : fact.verified ? "recorded tool evidence" : "unverified claim or inference"}<br /></small>}{fact.text}</li>)}</ul>}
            {forgetting === source.document_id && <div className="memory-confirm">
              <span>Forget everything learned from this? This stops active work using this bank and starts a new working context. Recorded chat stays visible.</span><span className="spacer" />
              <button type="button" className="link-button" onClick={() => setForgetting("")}>Keep</button>
              <button type="button" className="button danger-ghost small" onClick={() => void forget(source)}>Forget</button>
            </div>}
          </li>;
        })}
      </ul>

      {failed.length > 0 && <div className="memory-failed">
        <button type="button" className="memory-failed-toggle" aria-expanded={showFailed} onClick={() => setShowFailed(value => !value)}>
          <ChevronDown size={13} className={showFailed ? "open" : ""} />Not remembered ({failed.length})
        </button>
        {showFailed && <ul>{failed.map(source => <li key={source.document_id} title={source.error}>
          <span className="grow"><small className="muted">{relativeTime(source.created_at)}</small>“{source.excerpt}”</span>
          {/^(turn|episode)\//.test(source.document_id) && <button type="button" className="icon-button ghost tiny" title="Try again" aria-label="Try again" disabled={retrying} onClick={() => void retry([source])}><RotateCcw size={11} /></button>}
        </li>)}</ul>}
      </div>}
    </div>
  </aside>;
}
