import { memo, useEffect, useMemo, useRef, useState } from "react";
import { Brain, Check, ChevronRight, CircleAlert, Copy, LoaderCircle, Pencil, RotateCcw } from "lucide-react";
import { applyRunEvent, emptyRunStreamState } from "./runStream";
import { closeOpenFence, splitBlocks } from "./streaming";
import { useSmoothText, useStableText } from "./useSmoothText";
import { formatElapsed, isControlTool, stepLabel } from "./steps";
import { bankLabel } from "./MemoryPanel";
import type { Bot, MemoryOverview, RunEventEnvelope, RunTimelineSegment, RunToolCall, Turn } from "./types";
import { Avatar, Markdown } from "./ui";
import { TextWithJson } from "./JsonBlock";
import { turnFailure } from "./chatHelpers";

// TurnView renders one exchange in the style of Helpin's transcript: the
// user's message; the bot's work (thinking, tool steps, narration) collapsed
// into one "Worked for …" row once the answer arrives; then the answer.

function StepRow({ tool }: { tool: RunToolCall }) {
  const [open, setOpen] = useState(false);
  const clipText = (value: string) => (value.length > 6_000 ? `${value.slice(0, 6_000)}\n\n… truncated` : value);
  return <div className={`step ${tool.status}`}>
    <button type="button" className="step-head" onClick={() => setOpen(value => !value)} aria-expanded={open}>
      <span className="step-icon">{tool.status === "running" ? <LoaderCircle className="spin" size={12} /> : tool.status === "failed" ? <CircleAlert size={12} /> : <Check size={12} />}</span>
      <span className="step-label" title={stepLabel(tool)}>{stepLabel(tool)}</span>
      {tool.duration_ms !== undefined && <span className="step-meta">{formatElapsed(tool.duration_ms)}</span>}
      <ChevronRight className={`disclosure ${open ? "open" : ""}`} size={12} />
    </button>
    {open && <div className="step-detail">
      {tool.args_text && <><label>Input</label><pre>{clipText(tool.args_text)}</pre></>}
      {(tool.result || tool.error) && <><label>{tool.error ? "Error" : "Result"}</label><pre className={tool.error ? "tool-error" : ""}>{clipText(tool.error || tool.result)}</pre></>}
    </div>}
  </div>;
}

function WorkItem({ segment }: { segment: RunTimelineSegment }) {
  if (segment.kind === "tool") return <StepRow tool={segment} />;
  if (segment.kind === "reasoning" && segment.content.trim()) {
    return <div className="work-thought"><Brain size={12} /><div className="markdown-muted"><Markdown>{segment.content}</Markdown></div></div>;
  }
  if (segment.kind === "assistant" && segment.content.trim()) return <div className="work-note"><Markdown>{segment.content}</Markdown></div>;
  if (segment.kind === "plan" && segment.steps.length) {
    return <ol className="work-plan">{segment.steps.map((step, index) => <li key={index} className={step.status}>{step.step}</li>)}</ol>;
  }
  return null;
}

function useNow(active: boolean) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active]);
  return now;
}

type TurnViewProps = {
  turn: Turn; bot: Bot; events: RunEventEnvelope[]; working: boolean;
  // Where this bot's memories are kept, e.g. "Only Spain trip".
  memoryWhere: string; overview: MemoryOverview | null; onShowMemory: (documentId?: string) => void;
  // Why the turn's run failed, and how to send its message again.
  error?: string; onRetry?: (text: string) => void;
  // Set on the latest message the user may still edit.
  onEditMessage?: (turnId: string, text: string) => Promise<void>;
  // Told whether the reply is being written, so the bot's face can talk.
  onTyping?: (typing: boolean) => void;
};

// The conversation is polled every few seconds, which hands every turn a
// fresh but usually identical object; only real changes re-render a turn.
function sameTurn(a: TurnViewProps, b: TurnViewProps) {
  return a.events === b.events && a.working === b.working && a.bot === b.bot &&
    a.turn.id === b.turn.id && a.turn.status === b.turn.status && a.turn.assistant_text === b.turn.assistant_text &&
    a.turn.settled_at === b.turn.settled_at && a.turn.memory_status === b.turn.memory_status && a.turn.memory_facts === b.turn.memory_facts &&
    a.turn.run_id === b.turn.run_id && (a.turn.recalled?.length ?? 0) === (b.turn.recalled?.length ?? 0) &&
    a.memoryWhere === b.memoryWhere && a.overview === b.overview && a.onShowMemory === b.onShowMemory && a.error === b.error && a.onRetry === b.onRetry && a.onEditMessage === b.onEditMessage && a.onTyping === b.onTyping;
}

// UsedMemories lists what the bot remembered when it answered.
function UsedMemories({ recalled, overview }: { recalled: { text: string; bank: string }[]; overview: MemoryOverview | null }) {
  const [open, setOpen] = useState(false);
  const banks = [...new Set(recalled.map(item => bankLabel(item.bank, overview)))];
  return <div className="used-memories">
    <button type="button" className="memory-chip" onClick={() => setOpen(value => !value)} aria-expanded={open}>
      <Brain size={11} /> Used {recalled.length} {recalled.length === 1 ? "memory" : "memories"} · {banks.join(", ")}<ChevronRight className={`disclosure ${open ? "open" : ""}`} size={11} />
    </button>
    {open && <ul>{recalled.map((item, index) => <li key={index}>{item.text}{banks.length > 1 && <small> · {bankLabel(item.bank, overview)}</small>}</li>)}</ul>}
  </div>;
}

const clock = (at?: string) => at ? new Date(at).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }) : "";

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(text); setCopied(true); window.setTimeout(() => setCopied(false), 1400); } catch { /* clipboard unavailable */ }
  };
  return <button type="button" className="reply-action" onClick={copy} aria-label="Copy answer">{copied ? <Check size={12} /> : <Copy size={12} />}{copied ? "Copied" : "Copy"}</button>;
}

// The run's state from its events. While a turn streams, events only grow at
// the end, so only new ones are applied rather than all of them again.
function useRunState(events: RunEventEnvelope[]) {
  const memo = useRef<{ events: RunEventEnvelope[]; state: ReturnType<typeof emptyRunStreamState> } | null>(null);
  return useMemo(() => {
    const apply = (current: ReturnType<typeof emptyRunStreamState>, event: RunEventEnvelope) => applyRunEvent(current, event, event.sent_at || new Date().toISOString());
    const last = memo.current;
    const grows = !!last && events.length >= last.events.length && last.events.every((event, index) => events[index] === event);
    const state = grows ? events.slice(last!.events.length).reduce(apply, last!.state) : events.reduce(apply, emptyRunStreamState());
    memo.current = { events, state };
    return state;
  }, [events]);
}

// While text streams, finished paragraphs are parsed once; only the one
// still growing is parsed again, with any open code fence closed.
const MarkdownBlock = memo(function MarkdownBlock({ text }: { text: string }) { return <Markdown>{text}</Markdown>; });
function StreamingMarkdown({ text }: { text: string }) {
  const blocks = splitBlocks(text);
  return <div className="streaming-md">{blocks.map((block, index) => <MarkdownBlock key={index} text={index === blocks.length - 1 ? closeOpenFence(block) : block} />)}</div>;
}

// Long pasted messages fold to their first lines; the whole text is a click away.
const FOLD_LINES = 10;
function UserText({ text }: { text: string }) {
  const long = text.length > 900 || text.split("\n").length > FOLD_LINES + 2;
  const [open, setOpen] = useState(false);
  return <>
    <div className={`bubble-text ${long && !open ? "folded" : ""}`}><TextWithJson text={text} /></div>
    {long && <button type="button" className="bubble-more" onClick={() => setOpen(value => !value)}>{open ? "Show less" : "Show more"}</button>}
  </>;
}

export const TurnView = memo(function TurnView({ turn, bot, events, working, memoryWhere, overview, onShowMemory, error, onRetry, onEditMessage, onTyping }: TurnViewProps) {
  const [editing, setEditing] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const resend = async () => {
    if (!onEditMessage || !editing?.trim()) return;
    setSaving(true);
    try { await onEditMessage(turn.id, editing.trim()); setEditing(null); } finally { setSaving(false); }
  };
  // Events arrive in order (stored by sequence, live by arrival).
  const state = useRunState(events);
  const failure = error || state.error;
  const [open, setOpen] = useState(false);
  const now = useNow(working);

  const segments = state.timeline.filter(segment => segment.kind !== "tool" || !isControlTool(segment.tool_name));
  // The answer is the last piece of assistant text. While the bot still
  // works, text followed by more work is narration, not the answer.
  let answerIndex = -1;
  for (let index = segments.length - 1; index >= 0; index--) {
    const segment = segments[index];
    if (segment.kind === "assistant" && segment.content.trim()) { answerIndex = index; break; }
    if (working && segment.kind !== "assistant") break;
  }
  const answer = answerIndex >= 0 ? (segments[answerIndex] as Extract<RunTimelineSegment, { kind: "assistant" }>) : null;
  const work = segments.filter((_, index) => index !== answerIndex);
  const tools = work.filter((segment): segment is RunToolCall => segment.kind === "tool");
  const answerText = useStableText(answer?.content || (!working ? turn.assistant_text : ""), turn.status !== "failed" && turn.status !== "cancelled");
  const smooth = useSmoothText(answerText, working);
  const typing = smooth.animating && !!answerText;
  useEffect(() => {
    if (!onTyping) return;
    onTyping(typing);
    return () => onTyping(false);
  }, [onTyping, typing]);
  const elapsedMs = (turn.settled_at ? Date.parse(turn.settled_at) : now) - Date.parse(turn.started_at);
  const latest = [...tools].reverse()[0];
  const thinking = state.reasoning.some(item => item.status !== "completed");

  let workRow = null;
  if (working) {
    const label = latest && latest.status === "running" ? stepLabel(latest) : thinking ? "Thinking" : answer ? "Writing" : latest ? stepLabel(latest) : "Working";
    workRow = <div className="work-group active">
      <button type="button" className="work-head" onClick={() => setOpen(value => !value)} aria-expanded={open}>
        <LoaderCircle className="spin work-icon" size={13} />
        <span className="work-label">{label}</span>
        <span className="spacer" />
        <span className="work-meta">{formatElapsed(elapsedMs)}{tools.length > 1 ? ` · ${tools.length - 1} earlier ${tools.length === 2 ? "step" : "steps"}` : ""}</span>
        {work.length > 0 && <ChevronRight className={`disclosure ${open ? "open" : ""}`} size={13} />}
      </button>
      {open && <div className="work-body">{work.map(segment => <WorkItem key={segment.id} segment={segment} />)}</div>}
    </div>;
  } else if (tools.length >= 2 || (work.length > 0 && tools.length === 0 && work.some(segment => segment.kind === "reasoning"))) {
    workRow = <div className="work-group">
      <button type="button" className="work-head" onClick={() => setOpen(value => !value)} aria-expanded={open}>
        <Check className="work-icon done" size={13} />
        <span className="work-label">Worked for {formatElapsed(elapsedMs)}</span>
        <span className="spacer" />
        {tools.length > 0 && <span className="work-meta">{tools.length} {tools.length === 1 ? "step" : "steps"}</span>}
        <ChevronRight className={`disclosure ${open ? "open" : ""}`} size={13} />
      </button>
      {open && <div className="work-body">{work.map(segment => <WorkItem key={segment.id} segment={segment} />)}</div>}
    </div>;
  } else if (tools.length === 1) {
    workRow = <StepRow tool={tools[0]} />;
  }

  return <div className="turn" data-turn={turn.id}>
    {turn.user_text && (editing !== null ? <div className="bubble-edit">
      <textarea value={editing} autoFocus rows={Math.min(10, editing.split("\n").length + 1)} onChange={event => setEditing(event.target.value)}
        onKeyDown={event => { if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void resend(); } if (event.key === "Escape") setEditing(null); }} />
      <div className="row"><small className="muted">{bot.name} answers the edited message instead; the old answer is removed.</small><span className="spacer" />
        <button type="button" className="link-button" onClick={() => setEditing(null)}>Cancel</button>
        <button type="button" className="button primary small" disabled={saving || !editing.trim() || editing.trim() === turn.user_text.trim()} onClick={() => void resend()}>{saving ? "Sending…" : "Save and resend"}</button></div>
    </div> : <div className="user-row">
      {onEditMessage && <button type="button" className="reply-action edit-message" title="Edit and resend" aria-label="Edit message" onClick={() => setEditing(turn.user_text)}><Pencil size={12} /></button>}
      <div className={`bubble user ${turn.origin === "rex" ? "from-rex" : ""}`} title={clock(turn.started_at)}>
        {turn.origin === "rex" && <small className="bubble-from">From Rex</small>}
        <UserText text={turn.user_text} />
      </div>
    </div>)}
    <div className="bot-reply">
      <div className="reply-meta"><span className="reply-name"><Avatar bot={bot} size={18} typing={typing} /> {bot.name}</span>
        {turn.origin?.startsWith("report:") && <span className="reply-tag">Report from {turn.origin.slice("report:".length)}</span>}</div>
      {workRow}
      {answerText && <div className={`bubble bot ${smooth.animating ? "streaming" : ""}`}>{smooth.animating ? <StreamingMarkdown text={smooth.text} /> : <Markdown>{answerText}</Markdown>}</div>}
      {turn.status === "failed" && <div className="turn-failure" title={failure}>
        <CircleAlert size={15} />
        <div className="grow"><strong>{answerText ? "The bot stopped before finishing" : "This message didn't go through"}</strong><span>{turnFailure(failure)}</span></div>
        {onRetry && turn.user_text && <button type="button" className="button secondary small" onClick={() => onRetry(turn.user_text)}><RotateCcw size={12} />Try again</button>}
      </div>}
      {turn.status === "cancelled" && <div className="turn-note">Stopped.</div>}
      {!working && !smooth.animating && answerText && turn.status !== "failed" && <div className="reply-actions">
        <CopyButton text={answerText} />
        {turn.settled_at && <span className="reply-time">{clock(turn.settled_at)}</span>}
      </div>}
      <div className="memory-notes">
        {!!turn.recalled?.length && <UsedMemories recalled={turn.recalled} overview={overview} />}
        {turn.memory_status === "retained" && turn.memory_facts > 0 && <button type="button" className="memory-chip saved" title="Show what was remembered" onClick={() => onShowMemory(`turn/${turn.id}`)}>
          <Check size={11} /> Remembered {turn.memory_facts} {turn.memory_facts === 1 ? "fact" : "facts"}{memoryWhere && ` · ${memoryWhere}`}
        </button>}
      </div>
    </div>
  </div>;
}, sameTurn);
