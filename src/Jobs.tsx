import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronDown, ChevronRight, FolderOpen, ListChecks, LoaderCircle, Pause, Play } from "lucide-react";
import { api } from "./api";
import { isControlTool, stepLabel } from "./steps";
import { ApprovalCard, QuestionCard } from "./Chat";
import type { Bot, Interaction, JobView, JobWorker, RunToolCall, WorkItem } from "./types";
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
  worker_input: "Workers are waiting for your answer below.",
  worker_auth: "A worker needs a connection to be signed in again. Open its details to identify the connection.",
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
  const [steeringOpen, setSteeringOpen] = useState(false);
  const [instruction, setInstruction] = useState("");
  const job = view.job;
  const phase = jobPhase(view);
  const act = async (action: "pause" | "resume") => {
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
    {(view.workers?.length ?? 0) > 0 && job?.state === "running" ? <JobWorkers workers={view.workers!} /> : current.length > 0 && job?.state === "running" && <p className="job-current">
      <span className="muted">Working on</span> {current.slice(0, 3).map(item => item.label).join(", ")}{current.length > 3 ? ` and ${current.length - 3} more` : ""}
    </p>}
    {(view.pending?.length ?? 0) > 0 && <JobPending view={view} onChanged={onChanged} />}
    {view.browser_note && active && <p className="job-note muted">{view.browser_note}</p>}
    {job?.state === "waiting" && job.waiting_reason && <p className="job-note">{waitingText[job.waiting_reason] || "Waiting."}</p>}
    {job?.state === "paused" && <p className="job-note muted">Paused. Workers finish their current batch; no new batches start.</p>}
    {job?.error && <p className="job-note danger-text">{job.error}</p>}
    {error && <p className="job-note danger-text">{error}</p>}
    {steeringOpen && job && <form className="job-note" onSubmit={async event => {
      event.preventDefault(); setBusy("steer"); setError("");
      try { onChanged(await api.steerJob(view.id, job.spec_revision, instruction)); setInstruction(""); setSteeringOpen(false); }
      catch (caught) { setError(errorMessage(caught)); } finally { setBusy(""); }
    }}>
      <label>Instructions for workers<textarea value={instruction} onChange={event => setInstruction(event.target.value)} maxLength={32768} required /></label>
      <p className="muted small">Applied at the next safe boundary. Completed results stay saved; work already in progress may finish.</p>
      <button className="button small primary" disabled={!!busy || !instruction.trim()}>Send instructions</button>
    </form>}
    <footer className="job-actions">
      {active && <button type="button" className="button small ghost" aria-expanded={steeringOpen} onClick={() => setSteeringOpen(!steeringOpen)}>Steer workers</button>}
      <button type="button" className="button small ghost" onClick={() => setItemsOpen(true)}><ListChecks size={13} />Items</button>
      {view.dataset_path && <button type="button" className="button small ghost" onClick={() => void api.revealJobResults(view.id).catch(caught => setError(errorMessage(caught)))}><FolderOpen size={13} />Results</button>}
      <span className="spacer" />
      {job && <span className="muted job-tokens" title="Reported model tokens">{number.format(job.usage.input_tokens + job.usage.output_tokens)} tokens</span>}
      {active && job?.state !== "paused" && !(job?.state === "waiting" && decisionReasons.includes(job.waiting_reason || "")) &&
        <button type="button" className="button small ghost" disabled={!!busy} onClick={() => void act("pause")}><Pause size={13} />Pause</button>}
      {(job?.state === "paused" || (job?.state === "waiting" && decisionReasons.includes(job.waiting_reason || ""))) &&
        <button type="button" className="button small primary" disabled={!!busy} onClick={() => void act("resume")}><Play size={13} />{job.state === "paused" ? "Resume" : "Continue"}</button>}

    </footer>
    {itemsOpen && <JobItemsModal view={view} onClose={() => setItemsOpen(false)} />}
  </section>;
}

/** Questions and approvals from workers, answered on each worker's run. */
function JobPending({ view, onChanged }: { view: JobView; onChanged: (view: JobView) => void }) {
  const [error, setError] = useState("");
  const answer = async (runId: string, interaction: Interaction, intent: string, content: string) => {
    setError("");
    try {
      await api.respond(runId, interaction.id, intent, content);
      onChanged(await api.job(view.id));
    } catch (caught) { setError(errorMessage(caught)); }
  };
  return <div className="job-pending" aria-label="Workers waiting for you">
    {view.pending!.map(({ run_id, interaction }) => <div key={interaction.id} className="job-pending-item">
      {interaction.interaction_kind === "request_user_input"
        ? <QuestionCard interaction={interaction} onAnswer={text => void answer(run_id, interaction, "reply", text)} />
        : interaction.interaction_kind === "approval_request"
          ? <ApprovalCard interaction={interaction} onRespond={(intent, content) => answer(run_id, interaction, intent, content || "")} />
          : <p className="job-note">{interaction.title || "A worker needs you"}{interaction.summary ? `: ${interaction.summary}` : ""}</p>}
    </div>)}
    {error && <p className="job-note danger-text">{error}</p>}
  </div>;
}

/** A worker's step in the same words the chat uses. */
function workerStepLabel(step: JobWorker["steps"][number]): string {
  return stepLabel({ kind: "tool", id: "", tool_name: step.tool_name, args_text: step.args_text, result: "", error: step.error || "", status: "completed", started_at: step.created_at, order: 0 } as RunToolCall);
}

/** One row per running worker: its batch, progress and latest step. */
export function JobWorkers({ workers }: { workers: JobWorker[] }) {
  const [open, setOpen] = useState<string>("");
  return <ul className="job-workers" aria-label="Workers">
    {workers.map((worker, index) => {
      const steps = worker.steps.filter(step => !isControlTool(step.tool_name));
      const latest = steps[0];
      const expanded = open === worker.run_id;
      return <li key={worker.run_id} className="job-worker">
        <button type="button" className="job-worker-row" aria-expanded={expanded} onClick={() => setOpen(expanded ? "" : worker.run_id)}>
          {expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
          <LoaderCircle className={worker.recovery_reason || worker.status === "paused" ? "" : "spin"} size={12} />
          <strong>Worker {index + 1}</strong>
          <span className="muted">{worker.done}/{worker.items.length}</span>
          <span className="job-worker-step">{worker.status === "paused" ? (worker.pause_reason === "authentication" ? "Waiting for connection sign-in" : "Waiting for input") : worker.recovery_reason ? (worker.recovery_reason === "provider_busy" ? "Provider busy. Retrying automatically…" : "Waiting to reconnect. Will resume automatically…") : latest ? workerStepLabel(latest) : "Starting…"}</span>
          <small className="muted">{relativeTime(latest?.created_at || worker.started_at)}</small>
        </button>
        {expanded && <div className="job-worker-detail">
          <p className="muted small">Batch: {worker.items.map(item => item.label).join(", ")}</p>
          {worker.steering?.map(command => <p key={command.command_id} className="muted small">Instruction {command.revision}: {command.state === "accepted" ? "waiting for a safe boundary" : command.state === "applied" ? "applied" : "not applied"}</p>)}
          {steps.length > 0 && <ol className="job-worker-steps">
            {steps.map((step, stepIndex) => <li key={stepIndex} className={step.status === "failed" ? "danger-text" : ""}>
              {workerStepLabel(step)} <small className="muted">{relativeTime(step.created_at)}</small>
            </li>)}
          </ol>}
        </div>}
      </li>;
    })}
  </ul>;
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
  const [snapshot, setSnapshot] = useState<{ botId: string; jobs: JobView[] }>({ botId, jobs: [] });
  const revision = useRef(0);
  useEffect(() => {
    let stopped = false;
    const load = () => {
      const version = revision.current;
      return api.jobs(botId).then(jobs => {
        if (!stopped && version === revision.current) setSnapshot({ botId, jobs });
      }).catch(() => undefined);
    };
    void load();
    const timer = window.setInterval(load, 3000);
    return () => { stopped = true; window.clearInterval(timer); };
  }, [botId]);
  const replace = (view: JobView) => {
    revision.current++;
    setSnapshot(current => current.botId !== view.bot_id ? current : {
      ...current, jobs: current.jobs.map(item => item.id === view.id ? { ...item, ...view } : item),
    });
  };
  return [snapshot.botId === botId ? snapshot.jobs : [], replace];
}

export function jobPausable(view: JobView): boolean {
  return jobActive(view) && (view.job?.state || view.state) !== "paused";
}

/** Pause each job independently so one failure does not hide other successful pauses. */
export function PauseJobsButton({ jobs, onChanged }: { jobs: JobView[]; onChanged: (view: JobView) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const active = jobs.filter(jobPausable);
  const pause = async () => {
    setBusy(true); setError("");
    const results = await Promise.allSettled(active.map(async view => onChanged(await api.jobAction(view.id, "pause"))));
    const failures = results.flatMap((result, i) => result.status === "rejected" ? [`${active[i].title}: ${errorMessage(result.reason)}`] : []);
    setError(failures.join("; ")); setBusy(false);
  };
  if (!active.length && !error) return null;
  return <div className="pause-jobs-control">
    {!!active.length && <button type="button" className="button small ghost" disabled={busy} onClick={() => void pause()} title="Pause jobs after their current batches finish">
      <Pause size={14} />{busy ? "Pausing…" : active.length === 1 ? "Pause job" : "Pause jobs"}
    </button>}
    {error && <span role="alert" className="danger-text small">{error}</span>}
  </div>;
}

function CompactJob({ view, bot, onChanged }: { view: JobView; bot: Bot; onChanged: (view: JobView) => void }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const phase = jobPhase(view);
  const paused = (view.job?.state || view.state) === "paused";
  const actionable = paused || jobPausable(view);
  const act = async () => {
    setBusy(true); setError("");
    try { onChanged(await api.jobAction(view.id, paused ? "resume" : "pause")); }
    catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); }
  };
  return <section className="job-compact" aria-label={`Job ${view.title}`}>
    <div className="job-compact-row">
      <button type="button" className="job-compact-details" onClick={() => setOpen(true)} aria-label={`View ${view.title}`}>
        <ListChecks size={15} />
        <strong title={view.title}>{view.title}</strong>
        <span className={`job-compact-phase ${phase.tone}`}>{phase.label}</span>
        <span className="job-compact-count">{jobFraction(view)}</span>
        <ChevronRight size={14} />
      </button>
      {actionable && <button type="button" className="button small ghost" disabled={busy} onClick={() => void act()} aria-label={`${paused ? "Resume" : "Pause"} ${view.title}`} title={paused ? "Resume job" : "Pause after the current batch"}>
        {busy ? <LoaderCircle size={13} className="spin" /> : paused ? <Play size={13} /> : <Pause size={13} />}{paused ? "Resume" : "Pause"}
      </button>}
    </div>
    <JobProgress view={view} thin />
    {error && <p role="alert" className="danger-text small">{error}</p>}
    {open && <Modal title={view.title} onClose={() => setOpen(false)}><JobCard view={view} bot={bot} onChanged={onChanged} /></Modal>}
  </section>;
}

type ConversationJobProps = { bot: Bot; jobs: JobView[]; onChanged: (view: JobView) => void };

/** Only executing jobs occupy the bounded composer area. */
export function ConversationJobs({ bot, jobs, onChanged }: ConversationJobProps) {
  const active = jobs.filter(jobPausable);
  if (!active.length) return null;
  return <div className="conversation-jobs" aria-label="Active jobs">
    {active.map(view => <CompactJob key={view.id} view={view} bot={bot} onChanged={onChanged} />)}
  </div>;
}

/** Paused and finished jobs stay accessible in the scrolling transcript. */
export function ConversationJobHistory({ bot, jobs, onChanged }: ConversationJobProps) {
  const history = jobs.filter(view => !jobPausable(view));
  const paused = history.filter(jobActive).length;
  if (!history.length) return null;
  return <details className="conversation-job-history">
    <summary>Earlier jobs · {history.length}{paused > 0 ? ` · ${paused} paused` : ""}</summary>
    {history.map(view => <CompactJob key={view.id} view={view} bot={bot} onChanged={onChanged} />)}
  </details>;
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
