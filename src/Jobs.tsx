import { useCallback, useEffect, useState } from "react";
import { ChevronDown, ChevronRight, FolderOpen, ListChecks, LoaderCircle, Pause, Play, Square } from "lucide-react";
import { api } from "./api";
import type { Bot, JobView, WorkItem } from "./types";
import { Avatar, Empty, Modal, PageHeader, errorMessage, relativeTime } from "./ui";

// A batch job runs one task over a long list in the bot engine. These views
// answer three questions at a glance: which jobs are running, how far along
// each is (and on which items right now), and whether one needs the user.

const number = new Intl.NumberFormat();

export function jobActive(view: JobView): boolean {
  const state = view.job?.state || view.state;
  return state === "queued" || state === "running" || state === "verifying" || state === "waiting" || state === "paused";
}

/** Waiting reasons the user clears with Continue. */
const decisionReasons = ["no_progress", "exceptions", "budget", "uncertain_effects"];

const waitingText: Record<string, string> = {
  no_progress: "Workers keep failing to finish items. Check the latest results, then resume or change the approach.",
  exceptions: "More items ended as exceptions than the job allows. Review them, then resume to accept them.",
  budget: "The job reached its token budget.",
  worker_input: "A worker is waiting for your answer in the bot's conversation.",
  worker_auth: "A worker needs the AI connection to be signed in again.",
  dependencies: "The remaining items wait for others to finish.",
  unavailable: "The bot engine cannot run this job right now.",
  retry_backoff: "Waiting to retry items whose sites did not answer.",
  uncertain_effects: "A worker made changes and stopped before finishing. Check what it did, then continue to retry the held items.",
};

/** The job's state in a few words, and its colour. */
export function jobPhase(view: JobView): { label: string; tone: "ok" | "warn" | "danger" | "muted" | "busy" } {
  const job = view.job;
  if (!job) return { label: view.error || "Unavailable", tone: "muted" };
  switch (job.state) {
    case "queued": return { label: "Starting", tone: "busy" };
    case "running": {
      const workers = job.active_runs.length;
      return { label: workers > 1 ? `Running · ${workers} workers` : "Running", tone: "busy" };
    }
    case "verifying": return { label: "Checking results", tone: "busy" };
    case "waiting": return ["worker_input", "worker_auth", "no_progress", "exceptions", "budget", "uncertain_effects"].includes(job.waiting_reason || "")
      ? { label: "Needs you", tone: "warn" } : { label: "Waiting", tone: "muted" };
    case "paused": return { label: "Paused", tone: "muted" };
    case "completed": return { label: "Done", tone: "ok" };
    case "cancelled": return { label: "Cancelled", tone: "muted" };
    default: return { label: "Stopped", tone: "danger" };
  }
}

/** Remaining time from recent throughput, or "" while unknown. */
function eta(view: JobView): string {
  const job = view.job;
  if (!job || !view.items_per_minute || job.state !== "running") return "";
  const remaining = job.counts.total - job.counts.completed - job.counts.failed - job.counts.cancelled;
  const minutes = remaining / view.items_per_minute;
  if (minutes < 1) return "less than a minute left";
  if (minutes < 90) return `about ${Math.round(minutes)} min left`;
  return `about ${Math.round(minutes / 60)} h left`;
}

/** Done / total, for compact badges. */
export function jobFraction(view: JobView): string {
  const counts = view.job?.counts;
  return counts ? `${number.format(counts.completed + counts.failed)} / ${number.format(counts.total)}` : "";
}

export function JobProgress({ view, thin }: { view: JobView; thin?: boolean }) {
  const counts = view.job?.counts;
  if (!counts || counts.total === 0) return null;
  const segment = (value: number, className: string, title: string) =>
    value > 0 ? <span className={`job-bar-part ${className}`} style={{ width: `${(value / counts.total) * 100}%` }} title={`${number.format(value)} ${title}`} /> : null;
  return <div className={`job-bar ${thin ? "thin" : ""}`} role="progressbar" aria-valuemin={0} aria-valuemax={counts.total} aria-valuenow={counts.completed + counts.failed}>
    {segment(counts.found, "found", "found")}
    {segment(counts.no_result, "none", "with no result")}
    {segment(counts.exceptions, "exception", "exceptions")}
    {segment(counts.failed, "failed", "failed")}
    {segment(counts.running, "running", "in progress")}
    {segment(counts.retry_wait, "retrying", "waiting to retry")}
  </div>;
}

export function JobCard({ view, bot, onChanged, showBot }: { view: JobView; bot?: Bot; onChanged: (view: JobView) => void; showBot?: boolean }) {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [itemsOpen, setItemsOpen] = useState(false);
  const job = view.job;
  const phase = jobPhase(view);
  const act = async (action: "pause" | "resume" | "cancel") => {
    if (action === "cancel" && !window.confirm(`Cancel “${view.title}”? Finished items are kept.`)) return;
    setBusy(action); setError("");
    try { onChanged(await api.jobAction(view.id, action)); } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(""); }
  };
  const counts = job?.counts;
  const active = jobActive(view);
  const current = view.current || [];
  return <section className={`job-card ${phase.tone}`} aria-label={`Batch job ${view.title}`}>
    <header className="job-card-head">
      {showBot && bot ? <Avatar bot={bot} size={26} /> : <ListChecks size={17} className="job-icon" />}
      <div className="job-card-title">
        <strong>{view.title}</strong>
        <small className="muted">{showBot && bot ? `${bot.name} · ` : ""}started {relativeTime(view.created_at)}</small>
      </div>
      <span className={`chip ${phase.tone === "busy" ? "info" : phase.tone}`}>{phase.tone === "busy" && <LoaderCircle className="spin" size={11} />}{phase.label}</span>
    </header>
    {counts && <>
      <JobProgress view={view} />
      <div className="job-stats">
        <span><strong>{jobFraction(view)}</strong> items</span>
        {counts.found > 0 && <span className="stat found">{number.format(counts.found)} found</span>}
        {counts.no_result > 0 && <span className="stat none">{number.format(counts.no_result)} no result</span>}
        {counts.exceptions > 0 && <span className="stat exception">{number.format(counts.exceptions)} exceptions</span>}
        {counts.failed > 0 && <span className="stat failed">{number.format(counts.failed)} failed</span>}
        {counts.retry_wait > 0 && <span className="stat">{number.format(counts.retry_wait)} to retry</span>}
        {eta(view) && <span className="muted">{eta(view)}</span>}
      </div>
    </>}
    {current.length > 0 && job?.state === "running" && <p className="job-current">
      <span className="muted">Working on</span> {current.slice(0, 3).map(item => item.label).join(", ")}{current.length > 3 ? ` and ${current.length - 3} more` : ""}
    </p>}
    {job?.state === "waiting" && job.waiting_reason && <p className="job-note">{waitingText[job.waiting_reason] || "Waiting."}</p>}
    {job?.error && <p className="job-note danger-text">{job.error}</p>}
    {error && <p className="job-note danger-text">{error}</p>}
    <footer className="job-actions">
      <button type="button" className="button small ghost" onClick={() => setItemsOpen(true)}><ListChecks size={13} />Items</button>
      {view.dataset_path && <button type="button" className="button small ghost" onClick={() => void api.revealJobResults(view.id).catch(caught => setError(errorMessage(caught)))}><FolderOpen size={13} />Results</button>}
      <span className="spacer" />
      {job && <span className="muted job-tokens" title="Reported model tokens">{number.format(job.usage.input_tokens + job.usage.output_tokens)} tokens</span>}
      {active && job?.state !== "paused" && !(job?.state === "waiting" && decisionReasons.includes(job.waiting_reason || "")) &&
        <button type="button" className="button small ghost" disabled={!!busy} onClick={() => void act("pause")}><Pause size={13} />Pause</button>}
      {(job?.state === "paused" || (job?.state === "waiting" && decisionReasons.includes(job.waiting_reason || ""))) &&
        <button type="button" className="button small primary" disabled={!!busy} onClick={() => void act("resume")}><Play size={13} />{job.state === "paused" ? "Resume" : "Continue"}</button>}
      {active && <button type="button" className="button small ghost danger-text" disabled={!!busy} onClick={() => void act("cancel")}><Square size={12} />Cancel</button>}
    </footer>
    {itemsOpen && <JobItemsModal view={view} onClose={() => setItemsOpen(false)} />}
  </section>;
}

const itemTabs: { key: string; label: string }[] = [
  { key: "", label: "All" }, { key: "running", label: "In progress" }, { key: "completed", label: "Done" },
  { key: "retry_wait", label: "To retry" }, { key: "failed", label: "Failed" }, { key: "pending", label: "Not started" },
];

/** A readable name for an item from common input fields. */
export function itemLabel(item: Pick<WorkItem, "id" | "input">): string {
  const input = item.input as Record<string, unknown> | null;
  if (input && typeof input === "object") {
    for (const key of ["name", "title", "label", "company", "school", "domain", "website", "url", "email"]) {
      const value = input[key];
      if (typeof value === "string" && value.trim()) {
        const place = key === "name" ? [input.city, input.state].filter((part): part is string => typeof part === "string" && part.trim() !== "").map(part => part.trim()).join(", ") : "";
        return place ? `${value.trim()}, ${place}` : value.trim();
      }
    }
  }
  return item.id;
}

function outcomeBadge(item: WorkItem) {
  if (item.state === "running") return <span className="chip info"><LoaderCircle className="spin" size={10} />Working</span>;
  if (item.state === "completed") {
    if (item.outcome === "completed") return <span className="chip ok">Found</span>;
    if (item.outcome === "no_result") return <span className="chip muted">No result</span>;
    return <span className="chip warn" title={item.exception?.detail}>{item.exception?.kind?.replace(/_/g, " ") || "Exception"}</span>;
  }
  if (item.state === "failed") return <span className="chip danger">Failed</span>;
  if (item.state === "retry_wait") return <span className="chip warn">To retry</span>;
  if (item.state === "cancelled") return <span className="chip muted">Cancelled</span>;
  return <span className="chip muted">Not started</span>;
}

/** One line of a result for the list: the first short text fields. */
function resultPreview(item: WorkItem): string {
  if (item.exception) return item.exception.detail;
  if (item.state !== "completed") return item.last_error || "";
  const result = item.result as Record<string, unknown> | undefined;
  if (!result || typeof result !== "object") return "";
  const parts: string[] = [];
  for (const [key, value] of Object.entries(result)) {
    if (typeof value === "string" && value.length < 120) parts.push(value);
    else if (Array.isArray(value)) parts.push(`${value.length} ${key}`);
    if (parts.length === 2) break;
  }
  return parts.join(" · ");
}

export function JobItemsModal({ view, onClose }: { view: JobView; onClose: () => void }) {
  const [tab, setTab] = useState("");
  const [items, setItems] = useState<WorkItem[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState<string | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(async (state: string, after = "") => {
    setLoading(true);
    try {
      const page = await api.jobItems(view.id, state, after, 100);
      setItems(current => after ? [...current, ...page.items] : page.items);
      setNext(page.next_after);
    } catch (caught) { setError(errorMessage(caught)); } finally { setLoading(false); }
  }, [view.id]);
  useEffect(() => { void load(tab); }, [load, tab]);
  // In-progress items change quickly; keep that tab live.
  useEffect(() => {
    if (tab !== "running") return;
    const timer = window.setInterval(() => void load("running"), 3000);
    return () => window.clearInterval(timer);
  }, [load, tab]);
  const counts = view.job?.counts;
  const tabCount: Record<string, number | undefined> = counts ? { "": counts.total, running: counts.running, completed: counts.completed, retry_wait: counts.retry_wait, failed: counts.failed, pending: counts.pending } : {};
  return <Modal title={view.title} subtitle={`${jobFraction(view)} items done`} onClose={onClose} wide className="job-items-modal">
    <div className="job-tabs" role="tablist">
      {itemTabs.map(item => <button type="button" role="tab" key={item.key} aria-selected={tab === item.key} className={`job-tab ${tab === item.key ? "active" : ""}`} onClick={() => { setTab(item.key); setOpen(null); }}>
        {item.label}{tabCount[item.key] !== undefined && <small>{number.format(tabCount[item.key]!)}</small>}
      </button>)}
    </div>
    {error && <p className="danger-text">{error}</p>}
    <div className="job-items">
      {items.map(item => <div key={item.id} className={`job-item ${open === item.id ? "open" : ""}`}>
        <button type="button" className="job-item-row" onClick={() => setOpen(open === item.id ? null : item.id)}>
          {open === item.id ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
          <span className="job-item-name"><strong>{itemLabel(item)}</strong>{itemLabel(item) !== item.id && <small className="muted">{item.id}</small>}</span>
          <span className="job-item-preview muted">{resultPreview(item)}</span>
          {item.attempts > 1 && <small className="muted" title="Attempts">×{item.attempts}</small>}
          {outcomeBadge(item)}
        </button>
        {open === item.id && <div className="job-item-detail">
          {item.result !== undefined && <><h4>Result</h4><pre>{JSON.stringify(item.result, null, 2)}</pre></>}
          {item.exception && <><h4>Exception</h4><p><strong>{item.exception.kind}</strong>: {item.exception.detail}</p></>}
          {item.last_error && item.state !== "completed" && <><h4>Last problem</h4><p>{item.last_error}</p></>}
          <h4>Input</h4><pre>{JSON.stringify(item.input, null, 2)}</pre>
          <p className="muted small">Attempts: {item.attempts}{item.run_id ? ` · worker run ${item.run_id}` : ""} · updated {relativeTime(item.updated_at)}</p>
        </div>}
      </div>)}
      {!loading && items.length === 0 && <p className="muted job-items-empty">No items here.</p>}
      {loading && <p className="muted job-items-empty"><LoaderCircle className="spin" size={14} /> Loading…</p>}
      {!loading && next && <button type="button" className="show-earlier" onClick={() => void load(tab, next)}>Show more</button>}
    </div>
  </Modal>;
}

/** Jobs of one bot, polled while the conversation is open. */
export function useBotJobs(botId: string): [JobView[], (view: JobView) => void] {
  const [jobs, setJobs] = useState<JobView[]>([]);
  useEffect(() => {
    let stopped = false;
    const load = () => api.jobs(botId).then(list => { if (!stopped) setJobs(list); }).catch(() => undefined);
    void load();
    const timer = window.setInterval(load, 3000);
    return () => { stopped = true; window.clearInterval(timer); };
  }, [botId]);
  const replace = (view: JobView) => setJobs(current => current.map(item => item.id === view.id ? { ...item, ...view } : item));
  return [jobs, replace];
}

/** Jobs pinned in a bot's conversation: running ones, plus ones that finished in the last day. */
export function ConversationJobs({ bot }: { bot: Bot }) {
  const [jobs, replace] = useBotJobs(bot.id);
  const [showDone, setShowDone] = useState(false);
  const dayAgo = Date.now() - 24 * 3600 * 1000;
  const active = jobs.filter(jobActive);
  const recent = jobs.filter(view => !jobActive(view) && new Date(view.updated_at).getTime() > dayAgo);
  if (active.length === 0 && recent.length === 0) return null;
  return <div className="conversation-jobs">
    {active.map(view => <JobCard key={view.id} view={view} bot={bot} onChanged={replace} />)}
    {recent.length > 0 && (showDone || active.length === 0
      ? recent.slice(0, 3).map(view => <JobCard key={view.id} view={view} bot={bot} onChanged={replace} />)
      : <button type="button" className="link-button small" onClick={() => setShowDone(true)}>{recent.length} finished job{recent.length > 1 ? "s" : ""}</button>)}
  </div>;
}

/** Every job, newest first, from the Jobs tab. */
export function JobsView({ bots, onOpenBot }: { bots: Bot[]; onOpenBot: (botId: string) => void }) {
  const [jobs, setJobs] = useState<JobView[] | null>(null);
  useEffect(() => {
    let stopped = false;
    const load = () => api.jobs().then(list => { if (!stopped) setJobs(list); }).catch(() => undefined);
    void load();
    const timer = window.setInterval(load, 3000);
    return () => { stopped = true; window.clearInterval(timer); };
  }, []);
  const replace = (view: JobView) => setJobs(current => current ? current.map(item => item.id === view.id ? { ...item, ...view } : item) : current);
  const active = (jobs || []).filter(jobActive);
  const finished = (jobs || []).filter(view => !jobActive(view));
  return <div className="page jobs-page">
    <PageHeader title="Batch jobs" description="Long lists your bots work through in the background, ten items at a time. You can close the app; jobs continue when Rex is open again." />
    {jobs === null ? <Empty icon={<LoaderCircle className="spin" size={20} />} title="Loading jobs…" />
      : jobs.length === 0 ? <Empty icon={<ListChecks size={22} />} title="No batch jobs yet">Ask a bot to work through a long list, such as “find the parent forums for these 500 schools”, and it starts a job here.</Empty>
      : <>
        {active.length > 0 && <h3 className="jobs-section">Running</h3>}
        {active.map(view => { const bot = bots.find(item => item.id === view.bot_id); return <div key={view.id} className="jobs-entry">
          <JobCard view={view} bot={bot} showBot onChanged={replace} />
          {bot && <button type="button" className="link-button small" onClick={() => onOpenBot(bot.id)}>Open {bot.name}'s conversation</button>}
        </div>; })}
        {finished.length > 0 && <h3 className="jobs-section">Finished</h3>}
        {finished.map(view => { const bot = bots.find(item => item.id === view.bot_id); return <div key={view.id} className="jobs-entry">
          <JobCard view={view} bot={bot} showBot onChanged={replace} />
          {bot && <button type="button" className="link-button small" onClick={() => onOpenBot(bot.id)}>Open {bot.name}'s conversation</button>}
        </div>; })}
      </>}
  </div>;
}
