import { useCallback, useEffect, useRef, useState } from "react";
import { AlarmClock, BookOpen, LoaderCircle, MessagesSquare, Plus, Settings as SettingsIcon } from "lucide-react";
import { api } from "./api";
import { useAttention, waitingConversations } from "./attention";
import { BotEditor, newBot } from "./BotEditor";
import { NewBotDialog } from "./NewBotDialog";
import { Chat } from "./Chat";
import { Onboarding } from "./Onboarding";
import { RoutinesView } from "./RoutinesView";
import { SettingsView } from "./SettingsView";
import { SideSlot } from "./SideList";
import { SkillsView } from "./SkillsView";
import type { Bot, Conversation, Settings, Status } from "./types";
import { Avatar, Empty, errorMessage, relativeTime } from "./ui";
import { AppLogo } from "./AppLogo";

// Each bot has one ongoing conversation, so the sidebar lists bots with
// their latest message, newest first, and opening a bot opens its thread.

// Memory lives in each bot's conversation (the memory panel), so there is
// no separate Memory page.
type View = { kind: "bot"; botId: string } | { kind: "routines" } | { kind: "skills" } | { kind: "settings" };
const viewKinds = ["bot", "routines", "skills", "settings"];

function loadView(): View | null {
  try { const raw = localStorage.getItem("rex.view"); const view = raw ? JSON.parse(raw) as View : null; return view && viewKinds.includes(view.kind) ? view : null; } catch { return null; }
}

export default function App() {
  const [status, setStatus] = useState<Status | null>(null);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [bots, setBots] = useState<Bot[]>([]);
  const [threads, setThreads] = useState<Conversation[]>([]);
  const [view, setView] = useState<View | null>(loadView);
  const [editing, setEditing] = useState<Bot | null>(null);
  const [naming, setNaming] = useState(false);
  // Where Routines, Skills and Settings put their lists.
  const [sideSlot, setSideSlot] = useState<HTMLDivElement | null>(null);
  const lastBotId = useRef<string | undefined>(undefined);
  const [error, setError] = useState("");
  const [booting, setBooting] = useState(true);

  const refreshStatus = useCallback(() => api.status().then(setStatus).catch(caught => setError(errorMessage(caught))), []);
  const refreshBots = useCallback(async () => {
    const list = await api.bots();
    // Keep the same array when nothing changed, so polling doesn't re-render.
    setBots(current => JSON.stringify(current) === JSON.stringify(list) ? current : list);
    return list;
  }, []);
  const refreshThreads = useCallback(() => api.conversations().then(setThreads).catch(() => undefined), []);

  useEffect(() => {
    (async () => {
      try {
        const [nextStatus, nextSettings] = await Promise.all([api.status(), api.settings()]);
        setStatus(nextStatus); setSettings(nextSettings);
        const list = await refreshBots();
        await refreshThreads();
        setView(current => current && (current.kind !== "bot" || list.some(bot => bot.id === current.botId)) ? current : list[0] ? { kind: "bot", botId: list[0].id } : { kind: "settings" });
      } catch (caught) { setError(errorMessage(caught)); } finally { setBooting(false); }
    })();
  }, [refreshBots, refreshThreads]);

  useEffect(() => {
    // Bots also change outside this window: Rex creates them from chat, and
    // a new bot renames and sets itself up in its own conversation.
    const timer = window.setInterval(() => { void refreshStatus(); void refreshThreads(); void refreshBots().catch(() => undefined); }, 5000);
    return () => window.clearInterval(timer);
  }, [refreshStatus, refreshThreads, refreshBots]);

  useEffect(() => {
    try { if (view) localStorage.setItem("rex.view", JSON.stringify(view)); } catch { /* ignore */ }
    if (view?.kind === "bot") lastBotId.current = view.botId;
  }, [view]);

  const openConversation = async (conversationId: string) => {
    const thread = threads.find(item => item.id === conversationId) || (await api.conversation(conversationId)).conversation;
    setView({ kind: "bot", botId: thread.bot_id });
  };

  // A new bot starts in its own conversation and sets itself up by asking
  // the user what it should do. The form stays available under "Edit bot".
  const createBot = async (name: string, face: string, color: string) => {
    try {
      const draft = await api.draftBot(name, face, color);
      setNaming(false);
      await refreshBots();
      await refreshThreads();
      setView({ kind: "bot", botId: draft.id });
    } catch (caught) { setError(errorMessage(caught)); }
  };

  const attention = useAttention({ bots, enabled: !settings?.notifications_off, onOpen: conversationId => void openConversation(conversationId).catch(() => undefined) });
  const waiting = waitingConversations(attention);

  if (booting) return <div className="boot"><AppLogo size={72} working /><p className="muted">Starting Rex…</p></div>;
  if (settings && !settings.onboarded) return <Onboarding initial={settings} onDone={async saved => { setSettings(saved); await refreshBots(); await refreshStatus(); }} />;

  const bot = view?.kind === "bot" ? bots.find(item => item.id === view.botId) : undefined;
  const threadOf = (botId: string) => threads.find(item => item.bot_id === botId);
  const thread = bot ? threadOf(bot.id) : undefined;
  // Most recent activity first; bots without messages keep their order.
  // Rex, the main bot, sits in the header; the others follow by latest
  // activity.
  const main = bots.find(item => item.role === "main");
  const mainThread = main ? threadOf(main.id) : undefined;
  const mainWaiting = !!mainThread && waiting.has(mainThread.id);
  const proposalFor = (botId: string) => attention.find(item => item.kind === "proposal" && item.bot_id === botId);
  const missedFor = (botId: string) => attention.find(item => item.kind === "missed" && item.bot_id === botId);
  const ordered = bots.filter(item => item.role !== "main")
    .sort((a, b) => (threadOf(b.id)?.last_at || "").localeCompare(threadOf(a.id)?.last_at || "") || a.position - b.position);

  // The left pane has tabs: Bots, and the pages that list things.
  const tab = (kind: View["kind"], label: string, icon: React.ReactNode) =>
    <button type="button" className={`side-tool ${view?.kind === kind ? "active" : ""}`} title={label} aria-label={label} aria-pressed={view?.kind === kind}
      onClick={() => {
        if (kind !== "bot") return setView({ kind } as View);
        const botId = [lastBotId.current, main?.id, bots[0]?.id].find(id => id && bots.some(item => item.id === id));
        if (botId) setView({ kind: "bot", botId });
      }}>{icon}</button>;

  return <SideSlot.Provider value={sideSlot}><div className="app">
    <nav className="bot-list" aria-label="Bots">
      <header className="bot-list-header">
        <div className={`bot-list-actions ${view?.kind === "bot" ? "" : "hidden"}`}>
          <button type="button" className="icon-button" title="New bot" aria-label="New bot" onClick={() => setNaming(true)}><Plus size={17} /></button>
        </div>
        {main ? <button type="button" className={`main-bot ${bot?.id === main.id ? "active" : ""}`} onClick={() => setView({ kind: "bot", botId: main.id })} title="Your main assistant">
          <span className="main-bot-logo"><Avatar bot={main} size={84} working={!!threadOf(main.id)?.working} />{mainWaiting && <span className="waiting-dot corner" title="Needs you" />}</span>
          <strong>{main.name}</strong>
          <small>{threadOf(main.id)?.working ? "Working…" : "Main assistant"}</small>
        </button> : <div className="brand"><AppLogo size={84} /><strong>Rex</strong></div>}
      </header>
      {view?.kind !== "bot" ? <div className="side-slot" ref={setSideSlot} /> : <div className="bot-rows">
        {ordered.map(item => {
          const itemThread = threadOf(item.id);
          const needsYou = !!itemThread && waiting.has(itemThread.id);
          return <button type="button" key={item.id} className={`bot-row ${bot?.id === item.id ? "active" : ""}`} onClick={() => setView({ kind: "bot", botId: item.id })}>
            <Avatar bot={item} size={46} working={!!itemThread?.working} />
            <span className="bot-row-text">
              <span className="bot-row-top"><strong>{item.name}</strong>{itemThread?.last_at && <small>{relativeTime(itemThread.last_at)}</small>}</span>
              <span className="bot-row-preview">
                {itemThread?.working ? <><LoaderCircle className="spin" size={11} /> Working…</>
                  : itemThread?.preview ? `${itemThread.preview_from === "user" ? "You: " : ""}${itemThread.preview}` : needsYou ? (missedFor(item.id) ? `Missed “${missedFor(item.id)!.title}”` : proposalFor(item.id) ? "Suggests new instructions" : "Has a question for you") : <span className="muted">Say hello</span>}
              </span>
            </span>
            {needsYou && <span className="waiting-dot" title="Needs you" />}
          </button>;
        })}
      </div>}
      <footer className="bot-list-footer">
        {tab("bot", "Bots", <MessagesSquare size={18} />)}
        {tab("routines", "Routines", <AlarmClock size={18} />)}
        {tab("skills", "Skills", <BookOpen size={18} />)}
        {tab("settings", "Settings", <SettingsIcon size={18} />)}
        <span className="spacer" />
        <span className={`engine-dot ${status?.runtime_ready ? "ready" : ""}`} title={status?.runtime_ready ? "Bot engine running" : "Bot engine starting"} />
      </footer>
    </nav>

    {error && <div className="toast" role="alert"><span>{error}</span><button type="button" className="link-button" onClick={() => setError("")}>Dismiss</button></div>}

    {view?.kind === "bot" && bot && <main className="main">
      {thread ? <Chat key={thread.id} bot={bot} bots={bots} conversationId={thread.id} status={status} pending={attention.filter(item => (item.kind === "missed" || item.kind === "proposal") && item.bot_id === bot.id)} onEdit={() => setEditing(bot)} onChanged={() => void refreshThreads()} />
        : <Empty icon={<LoaderCircle className="spin" size={20} />} title={`Opening ${bot.name}…`} />}
    </main>}
    {view?.kind === "routines" && <main className="main wide"><RoutinesView bots={bots} onOpenConversation={async id => { await refreshThreads(); await openConversation(id); }} /></main>}
    {view?.kind === "skills" && <main className="main wide"><SkillsView bots={bots} workspaceRoot={status?.workspace_root || "~/Rex"} onBotsChanged={() => void refreshBots()} /></main>}
    {view?.kind === "settings" && <main className="main wide"><SettingsView status={status} onSaved={saved => { setSettings(saved); void refreshStatus(); }} /></main>}

    {naming && <NewBotDialog onCreate={createBot} onClose={() => setNaming(false)} onManual={(name, color) => { setNaming(false); setEditing({ ...newBot(settings), name, color }); }} />}
    {editing && <BotEditor initial={editing} settings={settings} status={status} onClose={() => setEditing(null)}
      onSaved={async saved => { setEditing(null); await refreshBots(); await refreshThreads(); setView({ kind: "bot", botId: saved.id }); }}
      onDeleted={async () => { setEditing(null); const list = await refreshBots(); await refreshThreads(); setView(list[0] ? { kind: "bot", botId: list[0].id } : { kind: "settings" }); }} />}
  </div></SideSlot.Provider>;
}
