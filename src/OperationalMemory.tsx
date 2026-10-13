import { useState } from "react";
import { api } from "./api";
import type { HistoryExcerpt, HistoryPage, MemoryLearning } from "./types";
import { errorMessage, relativeTime } from "./ui";

export function OperationalMemory({ bank, state, onRefresh }: { bank: string; state: MemoryLearning | null; onRefresh: () => Promise<unknown> }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState<HistoryPage | null>(null);
  const [source, setSource] = useState<HistoryExcerpt | null>(null);
  const run = async (action: () => Promise<unknown>) => { setBusy(true); setError(""); try { await action(); } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); } };
  const search = (cursor = 0) => run(async () => { setPage(await api.historySearch(bank, query, cursor)); setSource(null); });
  const read = (id: string, offset = 0) => run(async () => { setSource(await api.historyRead(bank, id, offset)); });
  if (!state) return null;
  const saved = Object.values(state.counts).reduce((sum, count) => sum + count, 0) - (state.counts.forgotten || 0);
  return <section className="operational-memory">
    <strong>{saved} saved {saved === 1 ? "task" : "tasks"} · {state.lessons.filter(lesson => lesson.state === "supported").length} supported lessons</strong>
    <small className="muted">Recalled records shown in chat are the subset used for that task.</small>
    <small>Learning model: {state.model || "Not configured"}{!state.available && " · unavailable"}</small>
    <small>{state.context.enabled ? `Compaction at ${state.context.trigger_tokens?.toLocaleString()} tokens · ${state.context.context_window?.toLocaleString()} token window` : state.context.warning || "Compaction status appears after this bot starts a task."}</small>
    <div className="row"><small className="muted">{state.consolidation.last_success ? `Consolidated ${relativeTime(state.consolidation.last_success)}` : "No consolidation yet"}</small><span className="spacer" /><button type="button" className="button secondary small" disabled={busy || !state.available} onClick={() => void run(async () => { await api.consolidateMemory(bank); await onRefresh(); })}>{busy ? "Working…" : "Consolidate now"}</button></div>
    {state.consolidation.error && <p className="inline-error" role="alert">Consolidation: {state.consolidation.error}{state.consolidation.retry_at ? ` · retry ${relativeTime(state.consolidation.retry_at)}` : " · retry limit reached; use Consolidate now after fixing the model"}</p>}
    {state.lessons.length > 0 && <details><summary>Operational lessons (latest {state.lessons.length})</summary><ul className="memory-facts">{state.lessons.map(lesson => <li className="memory-card" key={lesson.id}><small className="muted">{lesson.state} · {lesson.task_count} distinct tasks · {lesson.verified_tasks} verified</small><p>{lesson.text}</p>{lesson.source_ids?.map(id => <button type="button" className="link-button small" key={id} onClick={() => void read(id)}>Read evidence</button>)}</li>)}</ul></details>}
    <details><summary>Saved task history ({saved})</summary>
      <form className="row" onSubmit={event => { event.preventDefault(); void search(); }}><input aria-label="Search recorded task history" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search older work, failures or outcomes" /><button className="button secondary small" disabled={busy}>Search</button></form>
      {page ? <><ul className="memory-facts">{page.entries.map(entry => <li key={entry.source_id}><small>{entry.kind} · {relativeTime(entry.created_at)}</small><p>{entry.text}{entry.truncated && " (excerpt)"}</p><button type="button" className="link-button" onClick={() => void read(entry.source_id)}>Read source</button></li>)}</ul>{page.entries.length === 0 && <p className="muted small">No recorded matches.</p>}{page.next_cursor !== undefined && <button type="button" className="link-button" onClick={() => void search(page.next_cursor)}>Next page</button>}</> : <ul className="memory-facts">{state.episodes.filter(episode => !episode.forgotten).map(episode => <li key={episode.turn_id}><div className="row"><button type="button" className="link-button" onClick={() => void read(`episode/${episode.turn_id}`)}>{relativeTime(episode.created_at)} · {episode.status.replaceAll("_", " ")}</button><span className="spacer" />{episode.status === "failed" && <button type="button" className="button secondary small" disabled={busy} onClick={() => void run(async () => { await api.retryMemory(episode.turn_id); await onRefresh(); })}>Retry learning</button>}</div><small>{episode.facts} learned records{episode.model && ` · ${episode.model}`}</small>{episode.error && <p className="inline-error" role="alert">{episode.error}</p>}</li>)}</ul>}
    </details>
    {source && <div className="memory-evidence"><div className="row"><strong>{source.kind}</strong><span className="spacer" /><button type="button" className="link-button" onClick={() => setSource(null)}>Close source</button></div><pre>{source.text}</pre>{source.truncated && <button type="button" className="link-button" onClick={() => void read(source.source_id, source.next_offset)}>Read next part</button>}</div>}
    {error && <p className="inline-error" role="alert">{error}</p>}
  </section>;
}
