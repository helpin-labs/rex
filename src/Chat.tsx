import { ConversationJobs, ConversationJobHistory, PauseJobsButton, jobPausable, useBotJobs } from "./Jobs";
import { Fragment, type KeyboardEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { JsonBlock, TextWithJson } from "./JsonBlock";
import { AlarmClock, ArrowDown, ArrowUp, Search, BookOpen, Brain, CircleAlert, ExternalLink, Eye, FileCode2, FileText, FolderOpen, Hand, KeyRound, MousePointerClick, Paperclip, RotateCcw, ShieldCheck, SlidersHorizontal, Square, X } from "lucide-react";
import { api } from "./api";
import { BrowserView } from "./BrowserView";
import { MemoryPanel, memoryHome, memoryWhereLabel } from "./MemoryPanel";
import { searchTurns, starterPrompts } from "./chatHelpers";
import { ChatSearch } from "./ChatSearch";
import { describeApproval, instructionChanges, looksLikePassword } from "./steps";
import { TurnView } from "./TurnView";
import { useLiveEvents } from "./useLiveEvents";
import type { AttentionItem, Bot, ConversationDetail, InstructionProposal, Interaction, MemoryOverview, RunEventEnvelope, Status, Turn, WorkspaceFile } from "./types";
import { Avatar, Markdown, errorMessage, relativeTime, signInToConnection, openExternal } from "./ui";

type Attachment = { name: string; note: string };

// The opening of a bot's instructions, cut at a sentence or word.
function intro(instructions: string): string {
  const text = instructions.replace(/^#+\s.*$/gm, "").replace(/\s+/g, " ").trim();
  if (text.length <= 200) return text;
  const cut = text.slice(0, 200);
  const sentence = cut.lastIndexOf(". ");
  return sentence > 80 ? cut.slice(0, sentence + 1) : `${cut.slice(0, cut.lastIndexOf(" "))}…`;
}

export function ApprovalCard({ interaction, onRespond }: { interaction: Interaction; onRespond: (intent: string, content?: string) => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const payload = interaction.request_payload || {};
  const toolName = String(payload.tool_name || "");
  const input = payload.input ?? payload.tool_input_preview;
  const respond = async (intent: string, content = "") => { setBusy(true); try { await onRespond(intent, content); } finally { setBusy(false); } };
  const plain = describeApproval(toolName, input);
  return <div className="approval-card">
    <div className="approval-head"><ShieldCheck size={16} /><div><strong>{plain ? plain.title : toolName ? `Allow “${toolName.split("__").at(-1)}”?` : interaction.title || "Approval needed"}</strong>{!plain && <small>{interaction.summary}</small>}</div></div>
    {plain ? <ul className="approval-list">{plain.lines.map(line => <li key={line}>{line}</li>)}</ul>
      : input !== undefined && (typeof input === "string" ? <pre className="approval-input"><TextWithJson text={input} /></pre> : <JsonBlock value={input} />)}
    <div className="approval-actions">
      <button type="button" className="button secondary" disabled={busy} onClick={() => respond("request_changes", "The user declined this action. Do not retry it; continue without it or ask what to do instead.")}>Decline</button>
      <button type="button" className="button primary" disabled={busy} onClick={() => respond("approve", "Approved")}>Allow</button>
    </div>
  </div>;
}

type Question = { question?: string; header?: string; options?: { label?: string; description?: string }[] };

// QuestionCard shows the bot's question with lettered options and a field
// for the user's own answer.
export function QuestionCard({ interaction, onAnswer }: { interaction: Interaction; onAnswer: (text: string) => void }) {
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

// MissedCard asks whether to run a routine that was missed while the computer
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
function ProposalCard({ bot, proposal, onAnswered, onOpenApps }: { bot: Bot; proposal: InstructionProposal; onAnswered: () => void; onOpenApps?: (appId: string) => void }) {
 if (proposal.kind === "connect_mcp") return <MCPProposalCard bot={bot} proposal={proposal} onAnswered={onAnswered} onOpenApps={onOpenApps} />;
  if (proposal.kind === "install_skill") return <SkillProposalCard bot={bot} proposal={proposal} onAnswered={onAnswered} />;
  return <InstructionProposalCard bot={bot} proposal={proposal} onAnswered={onAnswered} />;
}

function ProposalProof({ bot, proposal }: { bot: Bot; proposal: InstructionProposal }) {
  const [source, setSource] = useState<import("./types").HistoryExcerpt | null>(null);
  const [error, setError] = useState("");
  const read = async (id: string, offset = 0) => { try { setError(""); setSource(await api.historyRead(bot.memory_mode === "shared" ? "shared" : `bot:${bot.id}`, id, offset)); } catch (caught) { setError(errorMessage(caught)); } };
  const evidence = proposal.evidence;
  if (!evidence?.gap) return null;
  return <details><summary>Evidence and remedies reviewed</summary><p>{evidence.gap}</p><p>Memory: {evidence.memory_remedy}. {evidence.memory_insufficient}</p><p>Current capabilities: {evidence.capability_remedy}. {evidence.capability_insufficient}</p><p>{evidence.expected}</p><p>{evidence.access} · {evidence.setup_cost}</p>{evidence.sources?.map(id => <button type="button" className="link-button" key={id} onClick={() => void read(id)}>Read task evidence</button>)}{evidence.documentation?.map(url => <button type="button" key={url} className="link-button" onClick={() => openExternal(url)}>Documentation</button>)}{source && <div className="memory-evidence"><pre>{source.text}</pre>{source.truncated && <button type="button" className="link-button" onClick={() => void read(source.source_id, source.next_offset)}>Read next part</button>}</div>}{error && <p className="inline-error">{error}</p>}</details>;
}

function MCPProposalCard({ bot, proposal, onAnswered, onOpenApps }: { bot: Bot; proposal: InstructionProposal; onAnswered: () => void; onOpenApps?: (appId: string) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const answer = async (action: "review" | "discard") => { setBusy(true); try { await api.answerProposal(proposal.id, action); onAnswered(); if (action === "review") onOpenApps?.(proposal.catalog_id || ""); } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); } };
  return <div className="approval-card proposal-card"><strong>{proposal.summary}</strong><p>{proposal.reason}</p><p>{proposal.app?.description}</p><ProposalProof bot={bot} proposal={proposal} /><small className="muted">Review the connection, sign in and select its bots in Connect apps.</small>{error && <p className="inline-error">{error}</p>}<div className="approval-actions"><span className="spacer" /><button type="button" className="button secondary" disabled={busy} onClick={() => void answer("discard")}>No thanks</button><button type="button" className="button primary" disabled={busy || !onOpenApps || !proposal.app} onClick={() => void answer("review")}>Review connection</button></div></div>;
}

// A catalog skill a bot (or a look-back) suggests installing. Installing
// copies it at the indexed version and turns it on for the bot.
function SkillProposalCard({ bot, proposal, onAnswered }: { bot: Bot; proposal: InstructionProposal; onAnswered: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const skill = proposal.skill;
  const answer = async (action: "apply" | "discard") => {
    setBusy(true); setError("");
    try { await api.answerProposal(proposal.id, action); onAnswered(); } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); }
  };
  return <div className="approval-card proposal-card">
    <div className="approval-head"><BookOpen size={16} /><div>
      <strong>{proposal.source === "retrospective" ? `${bot.name} looked back and suggests a skill` : `${bot.name} suggests a skill`}</strong>
      <small>{skill ? `${skill.name} · from ${skill.repo}` : proposal.summary}</small>
    </div></div>
    {proposal.reason && <p className="proposal-reason">{proposal.reason}</p>}<ProposalProof bot={bot} proposal={proposal} />
    {skill ? <div className="skill-suggestion">
      <p>{skill.description}</p>
      <div className="catalog-facts">
        <span>{skill.license ? `Licence: ${skill.license}` : "No licence stated"}</span>
        <span>{skill.files.length} file{skill.files.length === 1 ? "" : "s"}</span>
        {skill.has_scripts && <span className="catalog-warning"><FileCode2 size={13} /> Contains scripts</span>}
        <button type="button" className="link-button" onClick={() => openExternal(skill.url)}>View on GitHub <ExternalLink size={11} /></button>
      </div>
    </div> : <p className="muted small">This skill is no longer in the catalog (its source may be off).</p>}
    {error && <div className="inline-error">{error}</div>}
    <div className="approval-actions">
      <span className="spacer" />
      <button type="button" className="button secondary" disabled={busy} onClick={() => answer("discard")}>No thanks</button>
      <button type="button" className="button primary" disabled={busy || !skill} onClick={() => answer("apply")}>{busy ? "Installing…" : `Install for ${bot.name}`}</button>
    </div>
  </div>;
}

function InstructionProposalCard({ bot, proposal, onAnswered }: { bot: Bot; proposal: InstructionProposal; onAnswered: () => void }) {
  const [busy, setBusy] = useState(false);
  const [full, setFull] = useState(false);
  const changes = instructionChanges(bot.instructions, proposal.instructions);
  const answer = async (action: "apply" | "discard") => { setBusy(true); try { await api.answerProposal(proposal.id, action); onAnswered(); } finally { setBusy(false); } };
  return <div className="approval-card proposal-card">
    <div className="approval-head"><SlidersHorizontal size={16} /><div>
      <strong>{proposal.source === "retrospective" ? `${bot.name} looked back at recent work` : `Update ${bot.name}'s instructions?`}</strong>
      <small>{proposal.summary}</small>
    </div></div>
    {proposal.reason && <p className="proposal-reason">{proposal.reason}</p>}<ProposalProof bot={bot} proposal={proposal} />
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
  const [total, setTotal] = useState(0);
  const [filter, setFilter] = useState("");
  const [preview, setPreview] = useState<{ path: string; content?: string; binary?: boolean } | null>(null);
  const [error, setError] = useState("");
  useEffect(() => { api.files(bot.id).then(result => { setFiles(result.files); setTotal(result.total ?? result.files.length); }).catch(caught => setError(errorMessage(caught))); }, [bot.id, refreshKey]);
  const shown = useMemo(() => {
    const words = filter.toLowerCase().split(/\s+/).filter(Boolean);
    return words.length ? files.filter(file => words.every(word => file.path.toLowerCase().includes(word))) : files;
  }, [files, filter]);
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
    </div> : <>
      {files.length > 8 && <div className="file-filter"><input value={filter} onChange={event => setFilter(event.target.value)} placeholder="Filter files" aria-label="Filter files" /></div>}
      <ul className="file-list">
        {files.length === 0 && <li className="muted">No files yet. Attach a file or ask the bot to work with one.</li>}
        {files.length > 0 && shown.length === 0 && <li className="muted">No files match.</li>}
        {shown.map(file => {
          const slash = file.path.lastIndexOf("/");
          return <li key={file.path}><button type="button" onClick={() => open(file.path)} title={file.path}>
            <FileText size={14} />
            <span className="file-name"><span>{file.path.slice(slash + 1)}</span>{slash > 0 && <small className="file-dir">{file.path.slice(0, slash)}</small>}</span>
            <small>{relativeTime(file.modified)}</small>
          </button></li>;
        })}
        {total > files.length && <li className="muted file-more">Showing the {files.length} newest of {total} files. Filter or open the folder in Finder for the rest.</li>}
      </ul>
    </>}
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

export function Chat({ bot, bots, conversationId, status, pending: waiting, defaultModel, onChanged, onEdit, onOpenSettings, onOpenApps, onTyping }: { bot: Bot; bots: Bot[]; conversationId: string; status: Status | null; pending: AttentionItem[]; defaultModel?: string; onChanged: () => void; onEdit: () => void; onOpenSettings?: (tab: "memory") => void; onOpenApps?: (appId: string) => void; onTyping?: (botId: string | null) => void }) {
  const [jobs, replaceJob] = useBotJobs(bot.id);
  const hasRunningJobs = jobs.some(jobPausable);
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
  useEffect(() => {
    setBrowserHeld(false);
    if (!bot.capabilities.browser || (bot.browser_backend || (status?.cloud_browsers ? "cloud" : "extension")) !== "extension") return;
    let cancelled = false;
    const refresh = () => api.browserLive(bot.id).then(live => { if (!cancelled) setBrowserHeld(live.held); }).catch(() => undefined);
    void refresh();
    const timer = window.setInterval(refresh, 3000);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [bot.id, bot.browser_backend, bot.capabilities.browser, status?.cloud_browsers]);
  const [refreshKey, setRefreshKey] = useState(0);
  const [shown, setShown] = useState(TURNS_SHOWN);
  const scrollRef = useRef<HTMLDivElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const [showJump, setShowJump] = useState(false);
  const [searching, setSearching] = useState(false);
  const [query, setQuery] = useState("");
  const [matchIndex, setMatchIndex] = useState(0);
  const [dragging, setDragging] = useState(false);
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
  const matchTurns = useMemo(() => searchTurns(turns, query).map(index => turns[index].id), [turns, query]);
  // Matches run newest first, as people usually look for something recent.
  const matches = useMemo(() => [...matchTurns].reverse(), [matchTurns]);
  useEffect(() => { setMatchIndex(0); }, [query]);
  const moveMatch = (step: 1 | -1) => setMatchIndex(current => matches.length ? (current + step + matches.length) % matches.length : 0);
  // A match in an older, folded message loads enough of the conversation.
  useEffect(() => {
    const id = matches[matchIndex];
    if (!id) return;
    const index = turns.findIndex(turn => turn.id === id);
    if (index >= 0 && index < turns.length - shown) setShown(turns.length - index + 5);
  }, [matches, matchIndex, turns, shown]);
  const closeSearch = () => { setSearching(false); setQuery(""); };
  useEffect(() => {
    const onKey = (event: globalThis.KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "f") { event.preventDefault(); setSearching(true); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const activeRun = detail?.runs.find(run => run.id === detail.conversation.active_run_id);
  // The sidebar shows the bot's face, which talks while its reply is written.
  const reportTyping = useCallback((typing: boolean) => onTyping?.(typing ? bot.id : null), [onTyping, bot.id]);
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
  // When the stream says the run stopped, refresh now rather than at the
  // next poll, so the turn settles as soon as the answer is complete.
  const stopped = liveEvents.some(event => event.type === "run.paused" || event.type === "run.completed" || event.type === "run.failed" || event.type === "run.cancelled");
  useEffect(() => { if (stopped) void load(); }, [stopped, load]);

  // The transcript stays at its latest message while the reader is there:
  // steps, answers and cards load after the turns, and each one would
  // otherwise leave the view short of the bottom. Scrolling up unpins it.
  const contentRef = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const clicked = useRef(0);
  const toBottom = useCallback(() => {
    const element = scrollRef.current;
    if (element) element.scrollTo({ top: element.scrollHeight, behavior: "instant" });
  }, []);
  useEffect(() => { pinned.current = true; toBottom(); }, [conversationId, toBottom]);
  useEffect(() => { if (pinned.current) toBottom(); }, [turns.length, pending.length, toBottom]);
  useEffect(() => {
    const content = contentRef.current;
    if (!content) return;
    // Growth right after a click is the reader opening something; leave
    // the view where it is.
    const observer = new ResizeObserver(() => { if (pinned.current && Date.now() - clicked.current > 800) toBottom(); });
    observer.observe(content);
    return () => observer.disconnect();
  }, [toBottom]);
  const onScroll = () => {
    const element = scrollRef.current;
    if (!element) return;
    pinned.current = element.scrollHeight - element.scrollTop - element.clientHeight < 80;
    setShowJump(!pinned.current && element.scrollHeight - element.scrollTop - element.clientHeight > 400);
  };
  const jumpToLatest = () => { pinned.current = true; setShowJump(false); scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: "smooth" }); };

  const send = async (text?: string) => {
    const body = (text ?? draft).trim();
    if (!body && !attachments.length) return;
    // Chat is not a safe place for passwords: they reach the model and stay
    // in the conversation. Logins keep them in the Keychain instead.
    if (looksLikePassword(body) && !window.confirm(`This looks like a password. Anything typed here reaches the model and stays in the conversation.\n\nTo let ${bot.name} sign in safely, cancel and add a login under Edit bot → Logins instead.\n\nSend it anyway?`)) return;
    const content = [body, ...attachments.map(item => item.note)].filter(Boolean).join("\n\n");
    setSending(true); setError(""); pinned.current = true;
    try {
      await api.send(conversationId, content);
      setDraft(""); setAttachments([]);
      await load();
      onChanged();
    } catch (caught) { setError(errorMessage(caught)); } finally { setSending(false); }
  };

  // Turns are memoised, so the retry they get must not change every render.
  const sendRef = useRef(send);
  sendRef.current = send;
  const retry = useCallback((text: string) => { void sendRef.current(text); }, []);
  const editMessage = useCallback(async (turnId: string, text: string) => {
    setError(""); pinned.current = true;
    try { await api.editMessage(conversationId, turnId, text); await loadRef.current(); onChangedRef.current(); } catch (caught) { setError(errorMessage(caught)); throw caught; }
  }, [conversationId]);
  const loadRef = useRef(load);
  loadRef.current = load;
  const onChangedRef = useRef(onChanged);
  onChangedRef.current = onChanged;
  const suggest = (prompt: string) => { setDraft(prompt); window.setTimeout(() => { const box = composerRef.current; if (box) { box.focus(); box.setSelectionRange(prompt.length, prompt.length); } }, 0); };

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
    <div className={`chat-main ${dragging ? "dragging" : ""} ${searching ? "searching" : ""}`}
      onDragOver={event => { if (event.dataTransfer.types.includes("Files")) { event.preventDefault(); setDragging(true); } }}
      onDragLeave={event => { if (event.currentTarget === event.target || !event.currentTarget.contains(event.relatedTarget as Node)) setDragging(false); }}
      onDrop={event => { if (event.dataTransfer.files.length) { event.preventDefault(); setDragging(false); void attach(event.dataTransfer.files); } }}>
      {dragging && <div className="drop-overlay"><Paperclip size={20} /><strong>Drop to attach</strong><small>{bot.capabilities.files_read ? `Saved to ${bot.name}'s workspace` : "Small text files are added to the message"}</small></div>}
      <header className="chat-header">
        <div className="chat-title">
          <div><h2>{bot.name}</h2><div className="badges">{capabilityBadges.map(badge => <span className="badge" key={badge}>{badge}</span>)}</div></div>
        </div>
        <div className="row chat-tools">
          {bot.capabilities.browser && bot.browser_backend !== "cloud" && (!!bot.browser_backend || !status?.cloud_browsers) && <button type="button" className={`button ${browserHeld ? "warning" : "secondary"} small`} onClick={toggleBrowser} title={browserHeld ? "Hand the browser back to the bot" : "Take control of the bot's browser, for example to sign in"}><MousePointerClick size={14} /><span className="button-label">{browserHeld ? "Hand back browser" : "Take over browser"}</span></button>}
          {(bot.capabilities.browser || bot.capabilities.files_read || !!home) && <div className="tool-group" role="group" aria-label="Panels">
            {bot.capabilities.browser && <button type="button" className={`tool ${panel === "browser" ? "active" : ""}`} title="Watch the bot's browser" aria-label="Browser" aria-pressed={panel === "browser"} onClick={() => togglePanel("browser")}><Eye size={15} /></button>}
            {bot.capabilities.files_read && <button type="button" className={`tool ${panel === "files" ? "active" : ""}`} title="Workspace files" aria-label="Files" aria-pressed={panel === "files"} onClick={() => togglePanel("files")}><FolderOpen size={15} /></button>}
            {home && <button type="button" className={`tool ${panel === "memory" ? "active" : ""}`} title={`Memory · ${memoryWhereLabel(home, bot)}`} aria-label="Memory" aria-pressed={panel === "memory"} onClick={() => togglePanel("memory")}><Brain size={15} /></button>}
          </div>}
          <div className="tool-group" role="group" aria-label="Conversation">
            <button type="button" className={`tool ${searching ? "active" : ""}`} title="Find in conversation (⌘F)" aria-label="Find in conversation" aria-pressed={searching} onClick={() => searching ? closeSearch() : setSearching(true)} disabled={!turns.length}><Search size={15} /></button>
            <button type="button" className="tool" title="Start fresh: keep this conversation on screen, but don't carry it into what the bot remembers right now" aria-label="Start fresh" onClick={startFresh} disabled={working || turns.length === 0}><RotateCcw size={15} /></button>
            <button type="button" className="tool" title="Bot settings" aria-label="Edit bot" onClick={onEdit}><SlidersHorizontal size={15} /></button>
          </div>
        </div>
      </header>
      {searching && <ChatSearch query={query} onQuery={setQuery} matches={matches} current={matchIndex} onMove={moveMatch} onClose={closeSearch} container={contentRef.current} renderKey={`${shown}:${turns.length}`} />}
      {status && !status.runtime_ready && <div className="banner">{status.runtime_waiting ? "Waiting for the previous bot engine to exit…" : "Starting the bot engine…"}</div>}
      {browserHeld && <div className="banner warning">You have control of {bot.name}'s browser. The bot waits until you hand it back.</div>}
      <div className="transcript" ref={scrollRef} onScroll={onScroll} onPointerDown={() => { clicked.current = Date.now(); }}><div className="transcript-content" ref={contentRef}>
        {detail && turns.length === 0 && <div className="chat-empty">
          <Avatar bot={bot} size={84} />
          <h3>{bot.name}</h3>
          <p>{bot.instructions ? intro(bot.instructions) : "Ask me anything."}</p>
          <div className="starters">{starterPrompts(bot).map(prompt => <button type="button" key={prompt} className="starter" onClick={() => suggest(prompt)}>{prompt.trim().replace(/:$/, "")}{prompt.endsWith(" ") ? "…" : ""}</button>)}</div>
        </div>}
        {turns.length > shown && <button type="button" className="show-earlier" onClick={() => setShown(value => value + TURNS_SHOWN)}>Show earlier messages</button>}
        {turns.map((turn, index) => index < turns.length - shown ? null : <Fragment key={turn.id}>
          {turn.section && <div className="section-divider"><span>{turn.section} · {new Date(turn.started_at).toLocaleDateString(undefined, { day: "numeric", month: "short" })}</span></div>}
          <TurnView turn={turn} bot={bot} events={eventsByTurn[index]} working={index === turns.length - 1 && working}
            memoryWhere={memoryWhereLabel(home, bot)} overview={overview} onShowMemory={showMemory}
            error={turn.status === "failed" ? detail?.runs.find(run => run.id === turn.run_id)?.error_message : undefined} onRetry={retry}
            onTyping={index === turns.length - 1 ? reportTyping : undefined}
            onEditMessage={index === turns.length - 1 && !working && pending.length === 0 && !turn.origin && turn.user_text ? editMessage : undefined} />
        </Fragment>)}
        {detail?.conversation.context_start && lastTurn && lastTurn.started_at < detail.conversation.context_start && <p className="fresh-note">Fresh start. {bot.name} won't carry the conversation above into new messages; memories still apply.</p>}
        <ConversationJobHistory key={bot.id} bot={bot} jobs={jobs} onChanged={replaceJob} />
        {proposals.map(proposal => <ProposalCard key={proposal.id} bot={bot} proposal={proposal} onOpenApps={onOpenApps} onAnswered={() => { setProposals(current => current.filter(item => item.id !== proposal.id)); onChanged(); }} />)}
        {missed.map(item => <MissedCard key={item.id} item={item} onAnswered={() => { void load(); onChanged(); }} />)}
        {signIns.map(interaction => <ReconnectCard key={interaction.id} interaction={interaction} />)}
        {approvals.map(interaction => <ApprovalCard key={interaction.id} interaction={interaction} onRespond={(intent, content) => respond(interaction, intent, content)} />)}
        {questions.map(interaction => <QuestionCard key={interaction.id} interaction={interaction} onAnswer={text => void send(text)} />)}
      </div></div>
      {showJump && <button type="button" className="jump-latest" onClick={jumpToLatest}><ArrowDown size={14} />Jump to latest</button>}
      <footer className="composer-wrap">
        <ConversationJobs key={bot.id} bot={bot} jobs={jobs} onChanged={replaceJob} />
        {error && <div className="inline-error"><CircleAlert size={14} /><span>{error}</span><button type="button" className="icon-button ghost tiny" onClick={() => setError("")}><X size={12} /></button></div>}
        {attachments.length > 0 && <div className="attachments">{attachments.map((item, index) => <span className="attachment" key={index}><Paperclip size={12} />{item.name}<button type="button" onClick={() => setAttachments(current => current.filter((_, i) => i !== index))} aria-label={`Remove ${item.name}`}><X size={11} /></button></span>)}</div>}
        <div className="composer">
          <button type="button" className="icon-button ghost" title="Attach files" onClick={() => fileInput.current?.click()}><Paperclip size={17} /></button>
          <input ref={fileInput} type="file" multiple hidden onChange={event => { void attach(event.target.files); event.target.value = ""; }} />
          <textarea ref={composerRef} value={draft} onChange={event => setDraft(event.target.value)} onKeyDown={onKeyDown} rows={1}
            placeholder={questions.length ? "Answer the bot…" : approvals.length ? "Answer the approval above first" : `Message ${bot.name}`}
            disabled={approvals.length > 0} />
          <PauseJobsButton key={bot.id} jobs={jobs} onChanged={replaceJob} />
          {working && !hasRunningJobs ? <button type="button" className="send stop" title="Stop" onClick={() => activeRun && api.cancel(activeRun.id).then(load).catch(caught => setError(errorMessage(caught)))}><Square size={14} /></button>
            : <button type="button" className="send" title="Send" disabled={working || sending || (!draft.trim() && !attachments.length) || approvals.length > 0} onClick={() => void send()}><ArrowUp size={17} /></button>}
        </div>
        <div className="composer-hint"><span className="model-name">{bot.model || defaultModel || "provider default"}</span> · Enter to send, Shift+Enter for a new line · drop files to attach</div>
      </footer>
    </div>
    {panel === "files" && <FilesDrawer bot={bot} onClose={() => setPanel(null)} refreshKey={refreshKey} />}
    {panel === "browser" && <BrowserView bot={bot} onClose={() => setPanel(null)} />}
    {panel === "memory" && home && <MemoryPanel bot={bot} home={home} focusDocument={memoryFocus} onClose={() => setPanel(null)} onOpenSettings={onOpenSettings && (() => onOpenSettings("memory"))} />}
  </div>;
}
