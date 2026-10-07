import { useCallback, useEffect, useState } from "react";
import { Select } from "./Select";
import { AlarmClock, CircleAlert, Play, Plus, Trash2 } from "lucide-react";
import { api } from "./api";
import { SideList } from "./SideList";
import type { Bot, Routine, RoutineRun } from "./types";
import { type Repeat, type ScheduleDraft, describeDraft, describeRoutine, draftFromRoutine, scheduleFields, weekdayNames } from "./schedule";
import { Avatar, Empty, Field, Modal, Toggle, errorMessage, relativeTime } from "./ui";

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
    <p className="muted small">Routines run while your Mac is awake and Rex is open. If a run is missed while the Mac was off, Rex asks whether to run it when you're back, once, for the latest missed run only. Failed runs retry up to twice; five failures in a row pause the routine.</p>
  </Modal>;
}

export function RoutinesView({ bots, onOpenConversation }: { bots: Bot[]; onOpenConversation: (conversationId: string) => void }) {
  const [routines, setRoutines] = useState<Routine[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [runs, setRuns] = useState<RoutineRun[]>([]);
  const [editing, setEditing] = useState<Partial<Routine> | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(() => api.routines().then(list => { setRoutines(list); setSelected(current => current || list[0]?.id || ""); }).catch(caught => setError(errorMessage(caught))), []);
  useEffect(() => { void load(); }, [load]);
  const loadRuns = useCallback(() => { if (selected) api.routineRuns(selected).then(setRuns).catch(() => undefined); }, [selected]);
  useEffect(() => { loadRuns(); const timer = window.setInterval(() => { loadRuns(); void load(); }, 5000); return () => window.clearInterval(timer); }, [loadRuns, load]);

  const routine = routines.find(item => item.id === selected);
  const bot = bots.find(item => item.id === routine?.bot_id);
  const runNow = async () => { try { await api.runRoutine(selected); loadRuns(); } catch (caught) { setError(errorMessage(caught)); } };
  const remove = async () => { if (routine && window.confirm(`Delete “${routine.title}”?`)) { await api.deleteRoutine(routine.id); setSelected(""); void load(); } };

  return <div className="page">
    <SideList>
      <header className="page-list-header row between"><div><h2>Routines</h2><small className="subtitle">Scheduled tasks</small></div>
        <button type="button" className="icon-button" title="New routine" disabled={!bots.length} onClick={() => setEditing({ bot_id: bots[0]?.id, schedule_kind: "daily", daily_time: "08:00", interval_minutes: 60, enabled: true })}><Plus size={16} /></button></header>
      <div className="callout small">Routines run only while your Mac is awake and Rex is running (it keeps running in the menu bar when you close the window).</div>
      <nav className="list">{routines.map(item => {
        const owner = bots.find(candidate => candidate.id === item.bot_id);
        return <button type="button" key={item.id} className={`list-item ${selected === item.id ? "selected" : ""}`} onClick={() => setSelected(item.id)}>
          {owner ? <Avatar bot={owner} size={34} /> : <span className="list-icon"><AlarmClock size={15} /></span>}
          <span className="list-text"><strong>{item.title}</strong><small>{item.enabled ? `${describeRoutine(item)} · next ${relativeTime(item.next_run_at)}` : item.paused_reason ? "Paused" : "Off"}</small></span>
        </button>;
      })}</nav>
    </SideList>
    <section className="page-content">
      {error && <div className="inline-error">{error}<button type="button" className="link-button" onClick={() => setError("")}>Dismiss</button></div>}
      {!routine ? <Empty icon={<AlarmClock size={22} />} title="No routine selected">Create a routine to have a bot do something on a schedule, such as a morning briefing.</Empty> : <>
        <header className="content-header">
          <div><h1>{routine.title}</h1><p className="muted">{bot ? `${bot.name} · ` : ""}{describeRoutine(routine)}{routine.enabled && routine.next_run_at ? ` · next run ${relativeTime(routine.next_run_at)}` : ""}</p></div>
          <div className="row">
            <button type="button" className="button secondary small" onClick={() => setEditing(routine)}>Edit</button>
            <button type="button" className="button primary small" onClick={runNow}><Play size={13} />Run now</button>
            <button type="button" className="icon-button ghost" title="Delete" onClick={remove}><Trash2 size={15} /></button>
          </div>
        </header>
        {routine.paused_reason && <div className="callout warning"><CircleAlert size={15} />{routine.paused_reason}</div>}
        {routine.retry_at && <div className="callout">A retry is scheduled {relativeTime(routine.retry_at)}.</div>}
        <div className="card"><h3 className="section-title">Instructions</h3><p className="pre">{routine.prompt}</p></div>
        <div className="card">
          <div className="row between"><h3 className="section-title">History</h3>{routine.conversation_id && <button type="button" className="link-button" onClick={() => onOpenConversation(routine.conversation_id)}>Open conversation</button>}</div>
          {runs.length === 0 ? <p className="muted">No runs yet.</p> : <table className="history">
            <thead><tr><th>Scheduled</th><th>Status</th><th>Attempt</th><th>Details</th></tr></thead>
            <tbody>{runs.map(run => <tr key={run.id}>
              <td className="nowrap">{new Date(run.scheduled_for).toLocaleString(undefined, { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })}</td>
              <td><span className={`status-pill ${run.status}`}>{statusLabels[run.status] || run.status}</span></td>
              <td>{run.attempt}</td>
              <td className="muted small" title={run.error || undefined}>{run.error ? runError(run.error) : run.finished_at ? `Finished ${relativeTime(run.finished_at)}` : ""}</td>
            </tr>)}</tbody>
          </table>}
        </div>
      </>}
    </section>
    {editing && <RoutineEditor initial={editing} bots={bots} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); void load(); }} />}
  </div>;
}
