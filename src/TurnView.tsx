import { memo, useEffect, useMemo, useState } from "react";
import { Brain, Check, ChevronRight, CircleAlert, LoaderCircle } from "lucide-react";
import { applyRunEvent, emptyRunStreamState } from "./runStream";
import { formatElapsed, isControlTool, stepLabel } from "./steps";
import { bankLabel } from "./MemoryPanel";
import type { Bot, MemoryOverview, RunEventEnvelope, RunTimelineSegment, RunToolCall, Turn } from "./types";
import { Markdown } from "./ui";
import { AppLogo } from "./AppLogo";
import { BotFace } from "./BotFace";

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
};

// The conversation is polled every few seconds, which hands every turn a
// fresh but usually identical object; only real changes re-render a turn.
function sameTurn(a: TurnViewProps, b: TurnViewProps) {
  return a.events === b.events && a.working === b.working && a.bot === b.bot &&
    a.turn.id === b.turn.id && a.turn.status === b.turn.status && a.turn.assistant_text === b.turn.assistant_text &&
    a.turn.settled_at === b.turn.settled_at && a.turn.memory_status === b.turn.memory_status && a.turn.memory_facts === b.turn.memory_facts &&
    a.turn.run_id === b.turn.run_id && (a.turn.recalled?.length ?? 0) === (b.turn.recalled?.length ?? 0) &&
    a.memoryWhere === b.memoryWhere && a.overview === b.overview && a.onShowMemory === b.onShowMemory;
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

export const TurnView = memo(function TurnView({ turn, bot, events, working, memoryWhere, overview, onShowMemory }: TurnViewProps) {
  // Events arrive in order (stored by sequence, live by arrival).
  const state = useMemo(() => events.reduce((current, event) => applyRunEvent(current, event, event.sent_at || new Date().toISOString()), emptyRunStreamState()), [events]);
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
  const answerText = answer?.content || (!working ? turn.assistant_text : "");
  const elapsedMs = (turn.settled_at ? Date.parse(turn.settled_at) : now) - Date.parse(turn.started_at);
  const latest = [...tools].reverse()[0];
  const thinking = state.reasoning.some(item => item.status !== "completed");

  let workRow = null;
  if (working) {
    const label = latest && latest.status === "running" ? stepLabel(latest) : thinking ? "Thinking" : answer ? "Writing" : latest ? stepLabel(latest) : "Working";
    workRow = <div className="work-group active">
      <button type="button" className="work-head" onClick={() => setOpen(value => !value)} aria-expanded={open}>
        <LoaderCircle className="spin work-icon" size={13} />
        <span className="work-label">{label} · {formatElapsed(elapsedMs)}</span>
        <span className="spacer" />
        {tools.length > 1 && <span className="work-meta">{tools.length - 1} previous</span>}
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

  return <div className="turn">
    {turn.user_text && <div className={`bubble user ${turn.origin === "rex" ? "from-rex" : ""}`}>
      {turn.origin === "rex" && <small className="bubble-from">From Rex</small>}
      <div className="bubble-text">{turn.user_text}</div>
    </div>}
    <div className="bot-reply">
      <div className="reply-meta"><span className="reply-name">{bot.role === "main" ? <AppLogo size={18} /> : <BotFace icon={bot.emoji} color={bot.color} seed={bot.id} size={18} />} {bot.name}</span>
        {turn.origin?.startsWith("report:") && <span className="reply-tag">Report from {turn.origin.slice("report:".length)}</span>}</div>
      {workRow}
      {answerText && <div className={`bubble bot ${turn.status === "failed" ? "failed" : ""}`}><Markdown>{answerText}</Markdown>{working && answer?.status !== "completed" && <span className="caret" />}</div>}
      {turn.status === "failed" && !answerText && <div className="bubble bot failed"><CircleAlert size={14} /> The bot could not finish this message.</div>}
      {turn.status === "cancelled" && <div className="turn-note">Stopped.</div>}
      <div className="memory-notes">
        {!!turn.recalled?.length && <UsedMemories recalled={turn.recalled} overview={overview} />}
        {turn.memory_status === "retained" && turn.memory_facts > 0 && <button type="button" className="memory-chip saved" title="Show what was remembered" onClick={() => onShowMemory(`turn/${turn.id}`)}>
          <Check size={11} /> Remembered {turn.memory_facts} {turn.memory_facts === 1 ? "fact" : "facts"}{memoryWhere && ` · ${memoryWhere}`}
        </button>}
      </div>
    </div>
  </div>;
}, sameTurn);
