import { useCallback, useEffect, useState } from "react";
import { Select } from "./Select";
import { AlarmClock, BookMarked, Binoculars, CircleAlert, Eye, MessagesSquare, Pencil, Play, Plus, Sunrise, Trash2 } from "lucide-react";
import { api } from "./api";
import { SideList } from "./SideList";
import type { Bot, Routine, RoutineRun } from "./types";
import { type Repeat, type ScheduleDraft, describeDraft, describeRoutine, draftFromRoutine, scheduleFields, weekdayNames } from "./schedule";
import { routineTemplates } from "./routineTemplates";
import { Avatar, Field, Modal, PageHeader, TileMark, Toggle, errorMessage, relativeTime } from "./ui";

// Network failures read as what happened, with the raw error on hover.
function runError(error: string): string {
  const host = /(?:lookup|dial tcp) ([a-z0-9.-]+\.[a-z]{2,})/i.exec(error)?.[1] || /https?:\/\/([^/"\s]+)/.exec(error)?.[1];
  if (/no such host|network is unreachable|connection refused|i\/o timeout|dial tcp/i.test(error)) return `Couldn't reach ${host || "the model provider"}. The Mac may have been offline; it retries by itself.`;
  return error;
}

const statusLabels: Record<string, string> = {
  succeeded: "Done", failed: "Failed", running: "Running", claimed: "Starting", missed: "Missed · waiting for you", skipped: "Missed · skipped", caught_up: "Missed · ran later", interrupted: "Interrupted", cancelled: "Cancelled", needs_input: "Needs your input",
};

// ScheduleBuilder picks a schedule from plain choices and shows what it
// means and when it will next run.
function ScheduleBuilder({ routine, onChange }: { routine: Partial<Routine>; onChange: (fields: Partial<Routine>) => void }) {
  const [draft, setDraft] = useState<ScheduleDraft>(() => draftFromRoutine(routine));
  const [preview, setPreview] = useState<{ next_runs: string[]; error?: string } | null>(null);
  const update = (patch: Partial<ScheduleDraft>) => {
    const next = { ...draft, ...patch };
    if (patch.repeat === "custom" && !next.cron.trim()) next.cron = scheduleFields({ ...draft, repeat: draft.repeat === "every" ? "daily" : draft.repeat }).cron || "0 8 * * *";
    setDraft(next);
    onChange(scheduleFields(next));
  };
  const fields = scheduleFields(draft);
  useEffect(() => {
    const timer = window.setTimeout(() => { api.previewSchedule(fields).then(setPreview).catch(() => setPreview(null)); }, 250);
    return () => window.clearTimeout(timer);
  }, [fields.schedule_kind, fields.cron, fields.interval_minutes]); // eslint-disable-line react-hooks/exhaustive-deps
  const choices: [Repeat, string][] = [["daily", "Daily"], ["weekdays", "Weekdays"], ["weekly", "Weekly"], ["monthly", "Monthly"], ["every", "Repeating"], ["custom", "Custom"]];
  return <div className="schedule-builder">
    <span className="field-label">Schedule</span>
    <div className="segmented wide">{choices.map(([value, label]) => <button type="button" key={value} className={draft.repeat === value ? "active" : ""} onClick={() => update({ repeat: value })}>{label}</button>)}</div>
    {draft.repeat === "weekly" && <div className="day-chips">{[1, 2, 3, 4, 5, 6, 0].map(day => <button type="button" key={day} className={`day-chip ${draft.days.includes(day) ? "active" : ""}`}
      onClick={() => update({ days: draft.days.includes(day) ? draft.days.filter(item => item !== day) : [...draft.days, day] })}>{weekdayNames[day]}</button>)}</div>}
    <div className="schedule-row">
      {draft.repeat === "every" && <>
        <span>Every</span>
        <Select value={draft.everyMinutes} onChange={value => update({ everyMinutes: Number(value) })}>
          {[15, 30, 45, 60, 120, 180, 240, 360, 720].map(minutes => <option key={minutes} value={minutes}>{minutes < 60 ? `${minutes} minutes` : minutes === 60 ? "1 hour" : `${minutes / 60} hours`}</option>)}
        </Select>
      </>}
      {draft.repeat === "monthly" && <>
        <span>On day</span>
        <Select value={draft.dayOfMonth} onChange={value => update({ dayOfMonth: Number(value) })}>{Array.from({ length: 28 }, (_, index) => index + 1).map(day => <option key={day} value={day}>{day}</option>)}</Select>
      </>}
      {draft.repeat !== "every" && draft.repeat !== "custom" && <><span>at</span><input type="time" value={draft.time} onChange={event => update({ time: event.target.value })} /></>}
      {draft.repeat === "custom" && <input className="cron-input" value={draft.cron} onChange={event => update({ cron: event.target.value })} placeholder="0 8 * * 1-5" spellCheck={false} />}
    </div>
    {draft.repeat === "custom" && <small className="field-hint">Cron: minute hour day-of-month month weekday, in your local time. For example “0 9-17 * * 1-5” runs on the hour from 9 to 5 on weekdays.</small>}
    <div className={`schedule-summary ${preview?.error ? "error" : ""}`}>
      <strong>{preview?.error || describeDraft(draft)}</strong>
      {!preview?.error && !!preview?.next_runs.length && <small>Next: {preview.next_runs.slice(0, 3).map(at => new Date(at).toLocaleString(undefined, { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })).join(" · ")}</small>}
    </div>
  </div>;
}

function RoutineEditor({ initial, bots, onClose, onSaved }: { initial: Partial<Routine>; bots: Bot[]; onClose: () => void; onSaved: () => void }) {
  const [routine, setRoutine] = useState<Partial<Routine>>(() => ({ ...initial, ...scheduleFields(draftFromRoutine(initial)) }));
  const [error, setError] = useState("");
  const update = (patch: Partial<Routine>) => setRoutine(current => ({ ...current, ...patch }));
  const save = async () => {
    try { await api.saveRoutine(routine); onSaved(); } catch (caught) { setError(errorMessage(caught)); }
  };
  return <Modal title={routine.id ? "Edit routine" : "New routine"} subtitle="Routines" onClose={onClose}
    footer={<><span className="spacer" /><button type="button" className="button secondary" onClick={onClose}>Cancel</button><button type="button" className="button primary" onClick={save}>Save</button></>}>
    {error && <div className="inline-error">{error}</div>}
    <Field label="Title"><input value={routine.title || ""} onChange={event => update({ title: event.target.value })} placeholder="Morning briefing" autoFocus /></Field>
    <Field label="Bot"><Select value={routine.bot_id || ""} onChange={value => update({ bot_id: value })}>{bots.map(bot => <option key={bot.id} value={bot.id}>{bot.name}</option>)}</Select></Field>
    <Field label="What should it do?"><textarea rows={4} value={routine.prompt || ""} onChange={event => update({ prompt: event.target.value })} placeholder="Summarise the top three AI news stories from today with links." /></Field>
    <ScheduleBuilder routine={routine} onChange={update} />
    <Toggle label="Enabled" checked={routine.enabled ?? true} onChange={enabled => update({ enabled })} />
    <p className="muted small">Routines run while your computer is awake and Rex is open. When you're back, the latest missed run starts automatically. Temporary connection failures wait and resume from saved progress. Other failed runs retry up to twice; five failures in a row stop those retries until the next scheduled time. A successful Run now clears the failure pause.</p>
  </Modal>;
}

type Filter = "all" | "active" | "off";

const templateIcons = { sunrise: Sunrise, binoculars: Binoculars, bookmarked: BookMarked, eye: Eye };

// Failure cooldowns keep their next scheduled run.
function isActive(routine: Routine) {
  return routine.enabled;
}

function routineStatus(routine: Routine): { label: string; tone: "ok" | "warn" | "" } {
  if (routine.paused_reason) return { label: routine.next_run_at ? `Retry ${relativeTime(routine.next_run_at)}` : "Paused", tone: "warn" };
  if (!routine.enabled) return { label: "Off", tone: "" };
  return { label: routine.next_run_at ? `Next ${relativeTime(routine.next_run_at)}` : "On", tone: "ok" };
}

const when = (at: string) => new Date(at).toLocaleString(undefined, { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });

export function RoutinesView({ bots, onOpenConversation }: { bots: Bot[]; onOpenConversation: (conversationId: string) => void }) {
  const [routines, setRoutines] = useState<Routine[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [filter, setFilter] = useState<Filter>("all");
  const [selected, setSelected] = useState<string>("");
  const [runs, setRuns] = useState<RoutineRun[]>([]);
  const [editing, setEditing] = useState<Partial<Routine> | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState("");
  const load = useCallback(() => api.routines().then(list => { setRoutines(list); setLoaded(true); }).catch(caught => setError(errorMessage(caught))), []);
  useEffect(() => { void load(); }, [load]);
  const loadRuns = useCallback(() => { if (selected) api.routineRuns(selected).then(setRuns).catch(() => undefined); }, [selected]);
  useEffect(() => { setRuns([]); setConfirmDelete(false); loadRuns(); const timer = window.setInterval(() => { loadRuns(); void load(); }, 5000); return () => window.clearInterval(timer); }, [loadRuns, load]);

  const routine = routines.find(item => item.id === selected);
  const bot = bots.find(item => item.id === routine?.bot_id);
  const defaultBot = (bots.find(item => item.role === "main") || bots[0])?.id;
  const create = (fields: Partial<Routine> = {}) => setEditing({ bot_id: defaultBot, schedule_kind: "cron", cron: "0 8 * * *", daily_time: "08:00", interval_minutes: 60, enabled: true, ...fields });
  const runNow = async () => { try { await api.runRoutine(selected); loadRuns(); } catch (caught) { setError(errorMessage(caught)); } };
  const remove = async () => {
    if (!routine) return;
    try { await api.deleteRoutine(routine.id); setSelected(""); void load(); } catch (caught) { setError(errorMessage(caught)); }
  };
  const shown = routines.filter(item => filter === "all" || (filter === "active" ? isActive(item) : !isActive(item)));
  const counts = { all: routines.length, active: routines.filter(isActive).length, off: routines.filter(item => !isActive(item)).length };
  const note = "Routines run while your computer is awake and Rex is running. Closing the window keeps Rex running in the background.";

  const card = (item: Routine) => {
    const owner = bots.find(candidate => candidate.id === item.bot_id);
    const status = routineStatus(item);
    return <article key={item.id} className="gallery-card">
      <button type="button" className="gallery-card-main" onClick={() => setSelected(item.id)}>
        {owner ? <Avatar bot={owner} size={40} /> : <TileMark seed={item.id} icon={<AlarmClock size={18} />} />}
        <span className="gallery-card-text"><strong>{item.title}</strong><small>{describeRoutine(item)}{owner ? ` · ${owner.name}` : ""}</small></span>
      </button>
      <footer className="gallery-card-footer">
        <span className={`chip ${status.tone}`}>{status.label}</span>
        <span className="spacer" />
        <span className="muted small">{item.last_run_at ? `Ran ${relativeTime(item.last_run_at)}` : "Not run yet"}</span>
      </footer>
    </article>;
  };

  const templates = <section className="gallery-section">
    <h3 className="gallery-section-title">Start from a template</h3>
    <div className="gallery-grid">{routineTemplates.map(template => {
      const Icon = templateIcons[template.icon];
      return <article key={template.id} className="gallery-card">
        <button type="button" className="gallery-card-main" onClick={() => create(template.fields)}>
          <TileMark seed={template.id} icon={<Icon size={18} />} />
          <span className="gallery-card-text"><strong>{template.fields.title}</strong><small className="clamp">{template.fields.prompt}</small></span>
        </button>
        <footer className="gallery-card-footer"><span className="muted small">{describeRoutine(template.fields as Routine)}</span><span className="spacer" />
          <button type="button" className="button secondary small" disabled={!bots.length} onClick={() => create(template.fields)}>Use</button></footer>
      </article>;
    })}</div>
  </section>;

  return <div className="page">
    <SideList>
      <header className="page-list-header"><h2>Routines</h2><small className="subtitle">Tasks that run on a schedule</small></header>
      <nav className="list">
        {(["all", "active", "off"] as Filter[]).map(item => <button type="button" key={item} className={`list-item compact ${!routine && filter === item ? "selected" : ""}`} onClick={() => { setSelected(""); setFilter(item); }}>
          <span className="list-text"><strong>{item === "all" ? "All routines" : item === "active" ? "Active" : "Paused or off"}</strong></span>
          {counts[item] > 0 && <span className="count">{counts[item]}</span>}
        </button>)}
      </nav>
    </SideList>
    <section className="page-content gallery-page">
      {error && <div className="inline-error"><span>{error}</span><button type="button" className="link-button" onClick={() => setError("")}>Dismiss</button></div>}
      {routine ? <div className="detail-page">
        <PageHeader back={{ label: "Routines", onClick: () => setSelected("") }} title={routine.title}
          description={<>{describeRoutine(routine)}{bot ? ` · ${bot.name}` : ""}</>}
          actions={<>
            {confirmDelete ? <><span className="small">Delete this routine?</span><button type="button" className="button danger-ghost small" onClick={remove}>Delete</button><button type="button" className="link-button" onClick={() => setConfirmDelete(false)}>Keep</button></>
              : <button type="button" className="icon-button ghost" title="Delete" aria-label="Delete routine" onClick={() => setConfirmDelete(true)}><Trash2 size={15} /></button>}
            <button type="button" className="button secondary small" onClick={() => setEditing(routine)}><Pencil size={13} />Edit</button>
            <button type="button" className="button primary small" onClick={runNow}><Play size={13} />Run now</button>
          </>} />
        {routine.paused_reason && <div className="callout warning"><CircleAlert size={15} /><span>{routine.paused_reason}</span></div>}
        {routine.retry_at && <div className="callout">A retry is scheduled {relativeTime(routine.retry_at)}.</div>}
        <div className="stat-row">
          <div className="stat"><small>Status</small><strong><span className={`chip ${routineStatus(routine).tone}`}>{routineStatus(routine).label.startsWith("Next") ? "On" : routineStatus(routine).label}</span></strong></div>
          <div className="stat"><small>Next run</small><strong>{isActive(routine) && routine.next_run_at ? when(routine.next_run_at) : "—"}</strong></div>
          <div className="stat"><small>Last run</small><strong>{routine.last_run_at ? when(routine.last_run_at) : "Not yet"}</strong></div>
          <div className="stat"><small>Bot</small><strong>{bot ? <><Avatar bot={bot} size={20} />{bot.name}</> : "—"}</strong></div>
        </div>
        <div className="card"><h3 className="section-title">Instructions</h3><p className="pre">{routine.prompt}</p></div>
        <div className="card">
          <div className="row between"><h3 className="section-title">History</h3>{routine.conversation_id && <button type="button" className="button secondary small" onClick={() => onOpenConversation(routine.conversation_id)}><MessagesSquare size={13} />Open conversation</button>}</div>
          {runs.length === 0 ? <p className="muted small">No runs yet. Use Run now to try it.</p> : <table className="history">
            <thead><tr><th>Scheduled</th><th>Status</th><th>Attempt</th><th>Details</th></tr></thead>
            <tbody>{runs.map(run => <tr key={run.id}>
              <td className="nowrap">{when(run.scheduled_for)}</td>
              <td><span className={`status-pill ${run.status}`}>{statusLabels[run.status] || run.status}</span></td>
              <td>{run.attempt}</td>
              <td className="muted small" title={run.error || undefined}>{run.error ? runError(run.error) : run.finished_at ? `Finished ${relativeTime(run.finished_at)}` : ""}</td>
            </tr>)}</tbody>
          </table>}
        </div>
      </div> : <>
        <PageHeader title={filter === "all" ? "Routines" : filter === "active" ? "Active routines" : "Paused or off"} description={`Have a bot do something on a schedule, like a morning briefing or a weekly report. ${note}`}
          actions={<button type="button" className="button primary small" disabled={!bots.length} onClick={() => create()}><Plus size={14} />New routine</button>} />
        {!loaded ? null : shown.length > 0 ? <div className="gallery-grid">{shown.map(card)}</div>
          : <div className="gallery-empty"><AlarmClock size={22} /><strong>{filter === "all" ? "No routines yet" : filter === "active" ? "No active routines" : "Nothing paused or off"}</strong>
            {filter === "all" && <span className="muted small">Create one, or start from a template below.</span>}</div>}
        {filter === "all" && templates}
      </>}
    </section>
    {editing && <RoutineEditor initial={editing} bots={bots} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); void load(); }} />}
  </div>;
}
