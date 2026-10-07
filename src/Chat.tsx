import { Fragment, type KeyboardEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AlarmClock, ArrowUp, KeyRound, Brain, CircleAlert, Eye, FileText, FolderOpen, Hand, MousePointerClick, Paperclip, RotateCcw, ShieldCheck, SlidersHorizontal, Square, X } from "lucide-react";
import { api } from "./api";
import { BrowserView } from "./BrowserView";
import { MemoryPanel, memoryHome, memoryWhereLabel } from "./MemoryPanel";
import { describeApproval, instructionChanges, looksLikePassword } from "./steps";
import { TurnView } from "./TurnView";
import { useLiveEvents } from "./useLiveEvents";
import type { AttentionItem, Bot, ConversationDetail, InstructionProposal, Interaction, MemoryOverview, RunEventEnvelope, Status, Turn, WorkspaceFile } from "./types";
import { Avatar, Markdown, errorMessage, relativeTime, signInToConnection } from "./ui";

type Attachment = { name: string; note: string };

function ApprovalCard({ interaction, onRespond }: { interaction: Interaction; onRespond: (intent: string, content?: string) => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const payload = interaction.request_payload || {};
  const toolName = String(payload.tool_name || "");
  const input = payload.input ?? payload.tool_input_preview;
  const respond = async (intent: string, content = "") => { setBusy(true); try { await onRespond(intent, content); } finally { setBusy(false); } };
  const plain = describeApproval(toolName, input);
  return <div className="approval-card">
    <div className="approval-head"><ShieldCheck size={16} /><div><strong>{plain ? plain.title : toolName ? `Allow “${toolName.split("__").at(-1)}”?` : interaction.title || "Approval needed"}</strong>{!plain && <small>{interaction.summary}</small>}</div></div>
    {plain ? <ul className="approval-list">{plain.lines.map(line => <li key={line}>{line}</li>)}</ul>
      : input !== undefined && <pre className="approval-input">{typeof input === "string" ? input : JSON.stringify(input, null, 2)}</pre>}
    <div className="approval-actions">
      <button type="button" className="button secondary" disabled={busy} onClick={() => respond("request_changes", "The user declined this action. Do not retry it; continue without it or ask what to do instead.")}>Decline</button>
      <button type="button" className="button primary" disabled={busy} onClick={() => respond("approve", "Approved")}>Allow</button>
    </div>
  </div>;
}

type Question = { question?: string; header?: string; options?: { label?: string; description?: string }[] };

// QuestionCard shows the bot's question with lettered options and a field
// for the user's own answer.
function QuestionCard({ interaction, onAnswer }: { interaction: Interaction; onAnswer: (text: string) => void }) {
  const questions = (interaction.request_payload?.questions as Question[] | undefined) || [];
  const [own, setOwn] = useState("");
  const submitOwn = () => { if (own.trim()) { onAnswer(own.trim()); setOwn(""); } };
  return <div className="question-card">
    {questions.length > 0 ? questions.map((question, index) => <div className="question" key={index}>
      {question.header && question.question && <p className="question-lead">{question.header}</p>}
      <div className="question-title"><Markdown>{question.question || question.header || ""}</Markdown></div>
      <small className="question-hint">{question.options?.length ? "Pick one, or just type what you have in mind." : "Type your answer below."}</small>
      {!!question.options?.length && <div className="question-options">{question.options.map((option, optionIndex) => option.label && <button type="button" className="question-option" key={optionIndex} onClick={() => onAnswer(option.label!)}>
        <span className="option-letter">{String.fromCharCode(65 + optionIndex)}</span>
        <span className="option-text"><strong>{option.label}</strong>{option.description && <small>{option.description}</small>}</span>
      </button>)}</div>}
    </div>) : <Markdown>{interaction.summary || ""}</Markdown>}
    <input className="question-own" value={own} onChange={event => setOwn(event.target.value)} onKeyDown={event => { if (event.key === "Enter") submitOwn(); }} placeholder="Type your own answer" />
  </div>;
}

// ReconnectCard appears when an MCP server needs the user to sign in again.
// After the sign-in Rex continues the run by itself.
function ReconnectCard({ interaction }: { interaction: Interaction }) {
  const [error, setError] = useState("");
  const [started, setStarted] = useState(false);
  const server = String(interaction.request_payload?.server_name || "this connection");
  const serverId = String(interaction.request_payload?.server_id || "");
  const signIn = async () => { setError(""); try { await signInToConnection(serverId); setStarted(true); } catch (caught) { setError(errorMessage(caught)); } };
  return <div className="approval-card missed-card">
    <div className="approval-head"><KeyRound size={16} /><div><strong>Sign in to {server} again</strong><small>{started ? "Finish signing in in your browser; the bot continues by itself." : "The bot's access expired or was revoked. It continues after you sign in."}</small></div></div>
    {error && <div className="inline-error">{error}</div>}
    <div className="approval-actions"><button type="button" className="button primary" disabled={!serverId} onClick={signIn}>Sign in</button></div>
  </div>;
}

// MissedCard asks whether to run a routine that was missed while the Mac
// was off. Only the latest missed run of each routine is offered.
function MissedCard({ item, onAnswered }: { item: AttentionItem; onAnswered: () => void }) {
  const [busy, setBusy] = useState(false);
  const answer = async (action: "run" | "skip") => { setBusy(true); try { await api.answerMissed(item.id, action); onAnswered(); } finally { setBusy(false); } };
  return <div className="approval-card missed-card">
    <div className="approval-head"><AlarmClock size={16} /><div><strong>“{item.title}” didn't run</strong><small>{item.detail}</small></div></div>
    <div className="approval-actions">
      <button type="button" className="button secondary" disabled={busy} onClick={() => answer("skip")}>Skip</button>
      <button type="button" className="button primary" disabled={busy} onClick={() => answer("run")}>Run now</button>
    </div>
  </div>;
}

// ProposalCard shows instructions a bot proposed for itself. Nothing
// changes until the user applies them.
function ProposalCard({ bot, proposal, onAnswered }: { bot: Bot; proposal: InstructionProposal; onAnswered: () => void }) {
  const [busy, setBusy] = useState(false);
  const [full, setFull] = useState(false);
  const changes = instructionChanges(bot.instructions, proposal.instructions);
  const answer = async (action: "apply" | "discard") => { setBusy(true); try { await api.answerProposal(proposal.id, action); onAnswered(); } finally { setBusy(false); } };
  return <div className="approval-card proposal-card">
    <div className="approval-head"><SlidersHorizontal size={16} /><div><strong>Update {bot.name}'s instructions?</strong><small>{proposal.summary}</small></div></div>
    {full ? <pre className="approval-input">{proposal.instructions}</pre>
      : <ul className="instruction-diff">{changes.map((change, index) => <li key={index} className={change.kind}>{change.kind === "add" ? "+" : "−"} {change.text}</li>)}{changes.length === 0 && <li>No visible changes.</li>}</ul>}
    <div className="approval-actions">
      <button type="button" className="link-button" onClick={() => setFull(value => !value)}>{full ? "Show changes" : "Show full instructions"}</button>
      <span className="spacer" />
      <button type="button" className="button secondary" disabled={busy} onClick={() => answer("discard")}>Discard</button>
      <button type="button" className="button primary" disabled={busy} onClick={() => answer("apply")}>Apply</button>
    </div>
  </div>;
}

function FilesDrawer({ bot, onClose, refreshKey }: { bot: Bot; onClose: () => void; refreshKey: number }) {
  const [files, setFiles] = useState<WorkspaceFile[]>([]);
  const [preview, setPreview] = useState<{ path: string; content?: string; binary?: boolean } | null>(null);
  const [error, setError] = useState("");
  useEffect(() => { api.files(bot.id).then(result => setFiles(result.files)).catch(caught => setError(errorMessage(caught))); }, [bot.id, refreshKey]);
  const open = async (path: string) => {
    try { setPreview({ path, ...(await api.readFile(bot.id, path)) }); } catch (caught) { setError(errorMessage(caught)); }
  };
  return <aside className="drawer">
    <header className="drawer-header">
      <div><small className="eyebrow">Workspace</small><h3 title={bot.workspace_dir}>{bot.workspace_dir.split("/").slice(-2).join("/")}</h3></div>
      <div className="row">
        <button type="button" className="icon-button ghost" title="Show in Finder" onClick={() => api.reveal(bot.id)}><FolderOpen size={16} /></button>
        <button type="button" className="icon-button ghost" onClick={onClose} aria-label="Close files"><X size={16} /></button>
      </div>
    </header>
    {error && <div className="inline-error">{error}</div>}
    {preview ? <div className="file-preview">
      <div className="row between"><button type="button" className="link-button" onClick={() => setPreview(null)}>← All files</button><button type="button" className="link-button" onClick={() => api.reveal(bot.id, preview.path)}>Reveal</button></div>
      <h4>{preview.path}</h4>
      {preview.binary ? <p className="muted">This file can't be previewed here.</p> : preview.path.endsWith(".md") ? <Markdown>{preview.content || ""}</Markdown> : <pre>{preview.content}</pre>}
    </div> : <ul className="file-list">
      {files.length === 0 && <li className="muted">No files yet. Attach a file or ask the bot to work with one.</li>}
      {files.map(file => <li key={file.path}><button type="button" onClick={() => open(file.path)}><FileText size={14} /><span>{file.path}</span><small>{relativeTime(file.modified)}</small></button></li>)}
    </ul>}
  </aside>;
}

// A paused conversation run holds many turns. Each turn records the run's
// last event sequence when it started, so its events are those after that
// point and up to the next turn's start in the same run.
function turnEvents(turn: Turn, next: Turn | undefined, stored: RunEventEnvelope[] | undefined, live?: RunEventEnvelope[]): RunEventEnvelope[] {
  const upper = next && next.run_id === turn.run_id ? next.start_sequence : Number.POSITIVE_INFINITY;
  const inTurn = (event: RunEventEnvelope) => {
    const sequence = event.sequence_no ?? 0;
    // Live deltas carry no sequence number; they belong to the working turn.
    if (!sequence) return !!live;
    if (turn.start_sequence > 0 || upper !== Number.POSITIVE_INFINITY) return sequence > turn.start_sequence && sequence <= upper;
    // Turns recorded before sequence tracking: fall back to timestamps.
    const at = Date.parse(event.sent_at || "");
    return !Number.isFinite(at) || (at >= Date.parse(turn.started_at) - 1500 && (!next || at < Date.parse(next.started_at)));
  };
  // The live stream first replays the stored history in order, then sends
  // new events as they happen, so the working turn reads it alone once it
  // has started; other turns read stored events.
  const source = live && live.length > 0 ? live : stored ?? [];
  return source.filter(inTurn);
}

// Finished turns keep the same events array between renders, so their
// transcript is not rebuilt while another turn streams.
function useTurnEvents(turns: Turn[], timelines: Record<string, { events: RunEventEnvelope[] }>, liveTurnId: string | undefined, live: RunEventEnvelope[]) {
  const cache = useRef<Record<string, { source: RunEventEnvelope[]; start: number; next?: string; events: RunEventEnvelope[] }>>({});
  return turns.map((turn, index) => {
    const next = turns[index + 1];
    if (turn.id === liveTurnId) return turnEvents(turn, next, timelines[turn.run_id]?.events, live);
    const source = timelines[turn.run_id]?.events ?? [];
    const cached = cache.current[turn.id];
    if (cached && cached.source === source && cached.start === turn.start_sequence && cached.next === next?.id) return cached.events;
    const events = turnEvents(turn, next, source);
    cache.current[turn.id] = { source, start: turn.start_sequence, next: next?.id, events };
    return events;
  });
}

// A bot's thread can grow long; it opens on its latest turns.
const TURNS_SHOWN = 30;

export function Chat({ bot, bots, conversationId, status, pending: waiting, onChanged, onEdit }: { bot: Bot; bots: Bot[]; conversationId: string; status: Status | null; pending: AttentionItem[]; onChanged: () => void; onEdit: () => void }) {
  const missed = waiting.filter(item => item.kind === "missed");
  const proposalKey = waiting.filter(item => item.kind === "proposal").map(item => item.id).join(",");
  const [proposals, setProposals] = useState<InstructionProposal[]>([]);
  useEffect(() => {
    if (!proposalKey) { setProposals([]); return; }
    api.proposals().then(list => setProposals(list.filter(item => item.bot_id === bot.id))).catch(() => undefined);
  }, [proposalKey, bot.id]);
  const [detail, setDetail] = useState<ConversationDetail | null>(null);
  // Each run's events, in sequence order, plus the cursor for the next fetch.
  const [timelines, setTimelines] = useState<Record<string, { events: RunEventEnvelope[]; next: number }>>({});
  const [draft, setDraft] = useState("");
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  // One panel beside the conversation at a time.
  const [panel, setPanel] = useState<"files" | "browser" | "memory" | null>(null);
  const [memoryFocus, setMemoryFocus] = useState<string | undefined>();
  const [overview, setOverview] = useState<MemoryOverview | null>(null);
  useEffect(() => { api.memory().then(setOverview).catch(() => undefined); }, [bot.memory_mode, panel]);
  const home = memoryHome(bot, overview, bots);
  const togglePanel = (next: "files" | "browser" | "memory") => { setMemoryFocus(undefined); setPanel(current => current === next ? null : next); };
  const showMemory = useCallback((documentId?: string) => { setMemoryFocus(documentId); setPanel("memory"); }, []);
  const [browserHeld, setBrowserHeld] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [shown, setShown] = useState(TURNS_SHOWN);
  const scrollRef = useRef<HTMLDivElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const timelineCursor = useRef<Record<string, number>>({});
  const timelineBusy = useRef<Record<string, boolean>>({});

  const fetchTimeline = useCallback(async (runId: string) => {
    if (timelineBusy.current[runId]) return;
    timelineBusy.current[runId] = true;
    try {
      const after = timelineCursor.current[runId] ?? 0;
      const page = await api.timeline(runId, after);
      if (page.next_sequence_no === after && after > 0) return;
      timelineCursor.current[runId] = page.next_sequence_no;
      setTimelines(current => {
        const existing = current[runId]?.events ?? [];
        return { ...current, [runId]: { events: [...existing, ...page.events], next: page.next_sequence_no } };
      });
    } catch { /* retried on the next poll */ } finally {
      timelineBusy.current[runId] = false;
    }
  }, []);

  const load = useCallback(async () => {
    try {
      setDetail(await api.conversation(conversationId));
    } catch (caught) { setError(errorMessage(caught)); }
  }, [conversationId]);

  useEffect(() => { setDetail(null); setTimelines({}); timelineCursor.current = {}; setError(""); setDraft(""); setAttachments([]); setShown(TURNS_SHOWN); void load(); }, [load]);

  // Read the events of the runs behind the turns on screen. Finished runs
  // are read once; the newest turns' runs are followed as they change.
  useEffect(() => {
    if (!detail) return;
    const visible = detail.turns.slice(-shown);
    const recent = new Set(visible.slice(-3).map(turn => turn.run_id));
    for (const runId of new Set(visible.map(turn => turn.run_id))) {
      if (runId && (!(runId in timelineCursor.current) || recent.has(runId))) void fetchTimeline(runId);
    }
  }, [detail, shown, fetchTimeline]);

  const turns = detail?.turns || [];
  const lastTurn = turns.at(-1);
  const activeRun = detail?.runs.find(run => run.id === detail.conversation.active_run_id);
  const working = !!lastTurn && lastTurn.status === "working" && (!activeRun || activeRun.status === "running" || activeRun.status === "queued");
  const pending = detail?.interactions || [];
  const approvals = pending.filter(item => item.interaction_kind !== "request_user_input" && item.interaction_kind !== "authentication");
  const signIns = pending.filter(item => item.interaction_kind === "authentication");
  const questions = pending.filter(item => item.interaction_kind === "request_user_input");

  // Poll while the bot works or memory is being written for the last turn.
  useEffect(() => {
    const busy = working || pending.length > 0 || lastTurn?.memory_status === "pending";
    if (!busy) return;
    const timer = window.setInterval(() => void load(), working ? 2500 : 4000);
    return () => window.clearInterval(timer);
  }, [working, pending.length, lastTurn?.memory_status, load]);

  // The working turn streams from the run's live events.
  const liveEvents = useLiveEvents(working ? activeRun?.id : undefined);
  const eventsByTurn = useTurnEvents(turns, timelines, working ? lastTurn?.id : undefined, liveEvents);

  useEffect(() => {
    const element = scrollRef.current;
    if (element) element.scrollTop = element.scrollHeight;
  }, [turns.length, pending.length, conversationId]);

  const send = async (text?: string) => {
    const body = (text ?? draft).trim();
    if (!body && !attachments.length) return;
    // Chat is not a safe place for passwords: they reach the model and stay
    // in the conversation. Logins keep them in the Keychain instead.
    if (looksLikePassword(body) && !window.confirm(`This looks like a password. Anything typed here reaches the model and stays in the conversation.\n\nTo let ${bot.name} sign in safely, cancel and add a login under Edit bot → Logins instead.\n\nSend it anyway?`)) return;
    const content = [body, ...attachments.map(item => item.note)].filter(Boolean).join("\n\n");
    setSending(true); setError("");
    try {
      await api.send(conversationId, content);
      setDraft(""); setAttachments([]);
      await load();
      onChanged();
    } catch (caught) { setError(errorMessage(caught)); } finally { setSending(false); }
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void send(); }
  };

  const attach = async (files: FileList | null) => {
    if (!files) return;
    for (const file of Array.from(files)) {
      try {
        if (bot.capabilities.files_read) {
          const saved = await api.upload(bot.id, file);
          setAttachments(current => [...current, { name: file.name, note: `Attached file: ${saved.path}` }]);
          setRefreshKey(value => value + 1);
        } else if (file.size < 100_000 && (file.type.startsWith("text/") || /\.(md|txt|csv|json|log|ya?ml)$/i.test(file.name))) {
          const text = await file.text();
          setAttachments(current => [...current, { name: file.name, note: `Attached file ${file.name}:\n\`\`\`\n${text}\n\`\`\`` }]);
        } else {
          setError(`${bot.name} has no file access. Turn on “Read files” in the bot settings to attach ${file.name}.`);
        }
      } catch (caught) { setError(errorMessage(caught)); }
    }
  };

  const toggleBrowser = async () => {
    try {
      if (browserHeld) { await api.releaseBrowser(bot.id); setBrowserHeld(false); }
      else { await api.takeOverBrowser(bot.id); setBrowserHeld(true); }
    } catch (caught) { setError(errorMessage(caught)); }
  };

  const startFresh = async () => {
    try { await api.startFresh(conversationId); await load(); onChanged(); } catch (caught) { setError(errorMessage(caught)); }
  };

  const respond = async (interaction: Interaction, intent: string, content = "") => {
    try { await api.respond(interaction.run_id, interaction.id, intent, content); await load(); } catch (caught) { setError(errorMessage(caught)); }
  };

  const capabilityBadges = useMemo(() => {
    const badges: string[] = [];
    if (bot.capabilities.files_write) badges.push("Edits files"); else if (bot.capabilities.files_read) badges.push("Reads files");
    if (bot.capabilities.web) badges.push("Web");
    if (bot.capabilities.browser) badges.push("Browser");
    if (bot.memory_mode !== "off") badges.push(bot.memory_mode === "shared" ? "Shared memory" : "Memory");
    return badges;
  }, [bot]);

  return <div className={`chat ${panel === "files" || panel === "memory" ? "with-drawer" : ""} ${panel === "browser" ? "with-browser" : ""}`}>
    <div className="chat-main">
      <header className="chat-header">
        <div className="chat-title">
          <Avatar bot={bot} size={bot.role === "main" ? 50 : 40} working={working} />
          <div><h2>{bot.name}</h2><div className="badges">{capabilityBadges.map(badge => <span className="badge" key={badge}>{badge}</span>)}</div></div>
        </div>
        <div className="row">
          {bot.capabilities.browser && <button type="button" className={`icon-button ${panel === "browser" ? "active" : ""}`} title="Watch the bot's browser" onClick={() => togglePanel("browser")}><Eye size={16} /></button>}
          {bot.capabilities.browser && <button type="button" className={`button ${browserHeld ? "warning" : "secondary"} small`} onClick={toggleBrowser} title="Take control of the bot's browser, for example to sign in"><MousePointerClick size={14} />{browserHeld ? "Hand back browser" : "Take over browser"}</button>}
          {bot.capabilities.files_read && <button type="button" className={`icon-button ${panel === "files" ? "active" : ""}`} title="Workspace files" onClick={() => togglePanel("files")}><FolderOpen size={16} /></button>}
          {home && <button type="button" className={`icon-button ${panel === "memory" ? "active" : ""}`} title={`Memory · ${memoryWhereLabel(home, bot)}`} aria-label="Memory" onClick={() => togglePanel("memory")}><Brain size={16} /></button>}
          <button type="button" className="icon-button" title="Start fresh: keep this conversation on screen, but don't carry it into what the bot remembers right now" aria-label="Start fresh" onClick={startFresh} disabled={working || turns.length === 0}><RotateCcw size={16} /></button>
          <button type="button" className="icon-button" title="Edit bot" aria-label="Edit bot" onClick={onEdit}><SlidersHorizontal size={16} /></button>
        </div>
      </header>
      {status && !status.runtime_ready && <div className="banner">Starting the bot engine…</div>}
      {browserHeld && <div className="banner warning">You have control of {bot.name}'s browser. The bot waits until you hand it back.</div>}
      <div className="transcript" ref={scrollRef}>
        {detail && turns.length === 0 && <div className="chat-empty">
          <Avatar bot={bot} size={84} />
          <h3>{bot.name}</h3>
          <p>{bot.instructions ? bot.instructions.slice(0, 180) : "Ask me anything."}</p>
        </div>}
        {turns.length > shown && <button type="button" className="show-earlier" onClick={() => setShown(value => value + TURNS_SHOWN)}>Show earlier messages</button>}
        {turns.map((turn, index) => index < turns.length - shown ? null : <Fragment key={turn.id}>
          {turn.section && <div className="section-divider"><span>{turn.section} · {new Date(turn.started_at).toLocaleDateString(undefined, { day: "numeric", month: "short" })}</span></div>}
          <TurnView turn={turn} bot={bot} events={eventsByTurn[index]} working={index === turns.length - 1 && working}
            memoryWhere={memoryWhereLabel(home, bot)} overview={overview} onShowMemory={showMemory} />
        </Fragment>)}
        {detail?.conversation.context_start && lastTurn && lastTurn.started_at < detail.conversation.context_start && <p className="fresh-note">Fresh start. {bot.name} won't carry the conversation above into new messages; memories still apply.</p>}
        {proposals.map(proposal => <ProposalCard key={proposal.id} bot={bot} proposal={proposal} onAnswered={() => { setProposals(current => current.filter(item => item.id !== proposal.id)); onChanged(); }} />)}
        {missed.map(item => <MissedCard key={item.id} item={item} onAnswered={() => { void load(); onChanged(); }} />)}
        {signIns.map(interaction => <ReconnectCard key={interaction.id} interaction={interaction} />)}
        {approvals.map(interaction => <ApprovalCard key={interaction.id} interaction={interaction} onRespond={(intent, content) => respond(interaction, intent, content)} />)}
        {questions.map(interaction => <QuestionCard key={interaction.id} interaction={interaction} onAnswer={text => void send(text)} />)}
      </div>
      <footer className="composer-wrap">
        {error && <div className="inline-error"><CircleAlert size={14} /><span>{error}</span><button type="button" className="icon-button ghost tiny" onClick={() => setError("")}><X size={12} /></button></div>}
        {attachments.length > 0 && <div className="attachments">{attachments.map((item, index) => <span className="attachment" key={index}><Paperclip size={12} />{item.name}<button type="button" onClick={() => setAttachments(current => current.filter((_, i) => i !== index))} aria-label={`Remove ${item.name}`}><X size={11} /></button></span>)}</div>}
        <div className="composer">
          <button type="button" className="icon-button ghost" title="Attach files" onClick={() => fileInput.current?.click()}><Paperclip size={17} /></button>
          <input ref={fileInput} type="file" multiple hidden onChange={event => { void attach(event.target.files); event.target.value = ""; }} />
          <textarea value={draft} onChange={event => setDraft(event.target.value)} onKeyDown={onKeyDown} rows={1}
            placeholder={questions.length ? "Answer the bot…" : approvals.length ? "Answer the approval above first" : `Message ${bot.name}`}
            disabled={approvals.length > 0} />
          {working ? <button type="button" className="send stop" title="Stop" onClick={() => activeRun && api.cancel(activeRun.id).then(load)}><Square size={14} /></button>
            : <button type="button" className="send" title="Send" disabled={sending || (!draft.trim() && !attachments.length) || approvals.length > 0} onClick={() => void send()}><ArrowUp size={17} /></button>}
        </div>
        <div className="composer-hint">{bot.model || "default model"} · Enter to send, Shift+Enter for a new line</div>
      </footer>
    </div>
    {panel === "files" && <FilesDrawer bot={bot} onClose={() => setPanel(null)} refreshKey={refreshKey} />}
    {panel === "browser" && <BrowserView bot={bot} onClose={() => setPanel(null)} />}
    {panel === "memory" && home && <MemoryPanel bot={bot} home={home} focusDocument={memoryFocus} onClose={() => setPanel(null)} />}
  </div>;
}
