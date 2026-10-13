import { useEffect, useState } from "react";
import { Select } from "./Select";
import { AppWindow, ArrowLeft, Blocks, BookOpen, Check, CircleOff, FileText, Globe, PenLine, SquareTerminal, Trash2, UserRound, Users } from "lucide-react";
import { api } from "./api";
import type { Bot, MCPConnection, ModelInfo, Provider, Settings, Skill, Status } from "./types";
import { Avatar, Field, TileMark, Toggle, errorMessage } from "./ui";
import { BotFace, botColors, faceJob, faces, suggestFace } from "./BotFace";
import { ColorPicker } from "./NewBotDialog";
import { Logins } from "./Logins";
import { AppMark } from "./AppsView";
import { appFor } from "./apps";
import { ModelPicker, ProviderPicker } from "./ModelPicker";
import { modelName } from "./models";
const providerNames: Record<Provider, string> = { local: "Local model server", anthropic: "Anthropic", openai: "OpenAI", openrouter: "OpenRouter", chatgpt: "ChatGPT subscription" };

// Whether a provider can be chosen, and what to do first if not.
function providerReady(provider: Provider, settings: Settings | null): string {
  if (!settings || provider === "local") return "";
  if (provider === "chatgpt") return settings.chatgpt?.connected ? "" : " (sign in in Settings)";
  return settings[provider].configured ? "" : " (add key in Settings)";
}

// Why a provider can't be picked yet, as a short note.
function providerNote(provider: Provider, settings: Settings | null): string | undefined {
  const reason = providerReady(provider, settings).trim().replace(/^\(|\)$/g, "");
  return reason ? reason.charAt(0).toUpperCase() + reason.slice(1) : undefined;
}

export function newBot(settings: Settings | null): Bot {
  return {
    id: "", name: "", emoji: "bot", color: botColors[Math.floor(Math.random() * botColors.length)],
    instructions: "", provider: settings?.default_provider || "local", model: settings?.default_model || "",
    capabilities: { files_read: false, files_write: false, shell: false, web: true, browser: false, memory: true },
    approval_mode: "ask", workspace_dir: "", memory_mode: "private", skills: [], mcp_connections: [], position: 0,
  };
}

type Tab = "profile" | "abilities" | "knowledge" | "logins";
const tabs: [Tab, string][] = [["profile", "Profile"], ["abilities", "Abilities"], ["knowledge", "Knowledge"], ["logins", "Logins"]];

// ChoiceCard is one of a few exclusive options, each with what it means.
function ChoiceCard({ icon, title, description, selected, disabled, onClick }: { icon: React.ReactNode; title: string; description: string; selected: boolean; disabled?: boolean; onClick: () => void }) {
  return <button type="button" role="radio" aria-checked={selected} className={`choice-card ${selected ? "selected" : ""}`} disabled={disabled} onClick={onClick}>
    <span className="choice-card-icon">{icon}</span>
    <span className="choice-card-text"><strong>{title}</strong><small>{description}</small></span>
    {selected && <Check size={15} className="choice-card-check" />}
  </button>;
}

// The bot's settings are a page in the main pane: who it is, what it may
// do, what it knows, and where it can sign in. Changes are saved together
// from the bar that appears when something changed.
export function BotEditor({ initial, settings, status, onClose, onSaved, onDeleted }: {
  initial: Bot; settings: Settings | null; status: Status | null; onClose: () => void; onSaved: (bot: Bot) => void; onDeleted: (id: string) => void;
}) {
  const [bot, setBot] = useState<Bot>(initial);
  const [baseline, setBaseline] = useState<Bot>(initial);
  const [tab, setTab] = useState<Tab>("profile");
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [modelError, setModelError] = useState("");
  const [skills, setSkills] = useState<Skill[]>([]);
  const [connections, setConnections] = useState<MCPConnection[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [saving, setSaving] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const isNew = !baseline.id;
  const writeTools = !!status?.write_tools;
  const browserMode = bot.browser_backend || (settings?.kernel_browser && settings.kernel?.configured ? "cloud" : "extension");
  const dirty = isNew || JSON.stringify(bot) !== JSON.stringify(baseline);

  const [allBots, setAllBots] = useState<Bot[]>([]);
  useEffect(() => { api.skills().then(setSkills).catch(() => undefined); api.connections().then(setConnections).catch(() => undefined); api.bots().then(setAllBots).catch(() => undefined); }, []);
  const sharers = allBots.filter(item => item.id !== bot.id && item.memory_mode === "shared");
  const [modelsLoading, setModelsLoading] = useState(false);
  useEffect(() => {
    let current = true;
    setModelError(""); setModelsLoading(true);
    api.models(bot.provider).then(list => { if (current) setModels(list.filter(model => !model.embedding)); })
      .catch(caught => { if (current) { setModels([]); setModelError(errorMessage(caught)); } })
      .finally(() => { if (current) setModelsLoading(false); });
    return () => { current = false; };
  }, [bot.provider]);

  // A new bot's face follows its name until the user picks one.
  const [facePicked, setFacePicked] = useState(!isNew);
  const update = (patch: Partial<Bot>) => { setNotice(""); setBot(current => {
    const next = { ...current, ...patch };
    if (!facePicked && ("name" in patch || "instructions" in patch)) next.emoji = suggestFace(next.name, next.instructions);
    return next;
  }); };
  const pickFace = (emoji: string) => { setFacePicked(true); update({ emoji }); };
  const currentFace = faceJob(bot.emoji);
  const [lookBack, setLookBack] = useState<"" | "asking" | "asked">("");
  const [lookBackError, setLookBackError] = useState("");
  const askLookBack = async () => {
    setLookBack("asking"); setLookBackError("");
    try { await api.lookBack(bot.id); setLookBack("asked"); } catch (caught) { setLookBack(""); setLookBackError(errorMessage(caught)); }
  };
  const capability = (key: keyof Bot["capabilities"], value: boolean) => { setNotice(""); setBot(current => {
    const capabilities = { ...current.capabilities, [key]: value };
    if (key === "files_write" && value) capabilities.files_read = true;
    if (key === "files_read" && !value) { capabilities.files_write = false; capabilities.shell = false; }
    if (key === "shell" && value) capabilities.files_read = true;
    // Shell commands always ask first.
    return { ...current, capabilities, approval_mode: capabilities.shell ? "ask" : current.approval_mode };
  }); };
  const toggleList = (key: "skills" | "mcp_connections", id: string) => update({ [key]: bot[key].includes(id) ? bot[key].filter(item => item !== id) : [...bot[key], id] } as Partial<Bot>);

  const save = async () => {
    setSaving(true); setError("");
    try {
      const saved = isNew ? await api.createBot(bot) : await api.saveBot(bot);
      setBot(saved); setBaseline(saved); setNotice("Saved");
      onSaved(saved);
    } catch (caught) { setError(errorMessage(caught)); } finally { setSaving(false); }
  };
  const leave = () => { if (!isNew && dirty && !window.confirm("Discard your changes to this bot?")) return; onClose(); };
  const remove = async () => {
    try { await api.deleteBot(bot.id); onDeleted(bot.id); } catch (caught) { setError(errorMessage(caught)); setConfirmDelete(false); }
  };

  const abilities = [
    { key: "web" as const, icon: <Globe size={17} />, label: "Search and read the web", description: "Web search and reading public pages",
      note: bot.capabilities.web && status && !status.web_search ? "Search is off until you add a search key in Settings → Tools. Reading pages works." : undefined },
    { key: "browser" as const, icon: <AppWindow size={17} />, label: "Use a browser", description: "Its own browser with a profile that keeps logins. You can take over for sign-ins.",
      disabled: settings ? !settings.browser_enabled : false, note: settings && !settings.browser_enabled ? "Browsers are off in Settings → Tools." : undefined },
    { key: "files_read" as const, icon: <FileText size={17} />, label: "Read files", description: "Files in its own workspace folder only" },
    { key: "files_write" as const, icon: <PenLine size={17} />, label: "Create and edit files", description: "Inside its workspace folder", disabled: !writeTools, note: writeTools ? undefined : "Needs the macOS sandbox, which is not active in the bot engine." },
    { key: "shell" as const, icon: <SquareTerminal size={17} />, label: "Run shell commands", description: "In a macOS sandbox limited to its folder. Asks first unless TypeSafe clears it.", disabled: !writeTools },
  ];
  const on = abilities.filter(item => bot.capabilities[item.key]).length;
  const setupExtension = async () => {
    try { setNotice((await api.setupBrowserExtension(bot.id)).message); }
    catch (error) { setError(errorMessage(error)); }
  };
  const modelLabel = bot.model || (bot.provider === settings?.default_provider && settings?.default_model) || "provider default";

  return <div className="page">
    <section className="page-content gallery-page">
      <div className="detail-page bot-settings">
        <header className="bot-settings-head">
          <button type="button" className="back-link" onClick={leave}><ArrowLeft size={13} />{isNew ? "Cancel" : `Back to ${baseline.name}`}</button>
          <div className="bot-settings-title">
            <Avatar bot={bot} size={60} />
            <div className="grow">
              <input className="bot-name-input" value={bot.name} onChange={event => update({ name: event.target.value })} placeholder="Name your bot" aria-label="Name" autoFocus={isNew} maxLength={40} />
              <small className="muted">{providerNames[bot.provider]} · {modelLabel} · {on} {on === 1 ? "ability" : "abilities"} on</small>
            </div>
            <div className="gallery-header-actions">
              {!isNew && bot.role !== "main" && (confirmDelete
                ? <><span className="small">Delete {baseline.name}? Its conversations go; its folder stays.</span><button type="button" className="button danger-ghost small" onClick={remove}>Delete</button><button type="button" className="link-button" onClick={() => setConfirmDelete(false)}>Keep</button></>
                : <button type="button" className="icon-button ghost" title="Delete bot" aria-label="Delete bot" onClick={() => setConfirmDelete(true)}><Trash2 size={15} /></button>)}
              {notice && !dirty && <span className="saved-note"><Check size={13} /> {notice}</span>}
            </div>
          </div>
          <nav className="tab-bar" role="tablist">{tabs.map(([key, label]) => <button type="button" role="tab" key={key} aria-selected={tab === key} className={tab === key ? "active" : ""} onClick={() => setTab(key)}>{label}</button>)}</nav>
        </header>
        {error && <div className="inline-error">{error}</div>}

        {tab === "profile" && <>
          {bot.role !== "main" && <div className="card">
            <h3 className="section-title">Look{!facePicked ? <span className="muted small"> · face suggested from the name</span> : null}</h3>
            <div className="face-choices">{faces.map(item => <button type="button" key={item.job} title={item.label} aria-label={item.label} className={`face-choice ${currentFace === item.job ? "selected" : ""}`} onClick={() => pickFace(item.job)}>
              <BotFace icon={item.job} color={bot.color} seed={item.job} size={40} />
            </button>)}</div>
            <ColorPicker value={bot.color} onChange={color => update({ color })} />
          </div>}
          <div className="card">
            <h3 className="section-title">Instructions</h3>
            <textarea className="instructions-input" rows={8} value={bot.instructions} onChange={event => update({ instructions: event.target.value })} placeholder="You plan trips. Always suggest train options before flights…" />
            <span className="field-hint">Who this bot is and how it should work. Skills add reusable know-how on top.</span>
          </div>
          <div className="card">
            <h3 className="section-title">Model</h3>
            <p className="muted small">Where {bot.name || "the bot"} thinks. Bots need a model that can call tools.</p>
            <ProviderPicker value={bot.provider} onChange={provider => update({ provider, model: "", context_window: 0 })}
              providers={(Object.keys(providerNames) as Provider[]).map(id => ({ id, note: providerNote(id, settings) }))} />
            <ModelPicker value={bot.model} models={models} loading={modelsLoading} error={modelError} onChange={model => update({ model, context_window: 0 })}
              defaultLabel={bot.provider === settings?.default_provider && settings?.default_model ? `Follows Settings: ${modelName({ id: settings.default_model })}` : "The provider's default"} />
            <Field label="Context window override" hint="Leave empty to use the provider's reported limit. If unavailable, enter this model's verified token limit to enable automatic compaction.">
              <input type="number" min={4096} max={4000000} value={bot.context_window || ""} onChange={event => update({ context_window: Number(event.target.value) || 0 })} placeholder="From provider metadata" />
            </Field>
          </div>
        </>}

        {tab === "abilities" && <>
          <div className="ability-grid">{abilities.map(item => <div key={item.key} className={`ability ${bot.capabilities[item.key] ? "on" : ""} ${item.disabled ? "disabled" : ""}`}>
            <TileMark seed={item.key} icon={item.icon} size={36} />
            <Toggle label={item.label} description={item.description} note={item.note} checked={bot.capabilities[item.key]} disabled={item.disabled} onChange={value => capability(item.key, value)} />
          </div>)}</div>
          {bot.capabilities.browser && <div className="card">
            <Field label="Browser mode" hint="Choose before starting a task. Changing mode starts a new run; logins stay in the local bot profile.">
              <Select value={bot.browser_backend || ""} onChange={value => update({ browser_backend: value as Bot["browser_backend"] })}>
                <option value="">{settings?.kernel_browser && settings.kernel?.configured ? "Follow Settings (Cloud)" : "Default (Extension)"}</option>
                <option value="standard">Standard</option>
                <option value="extension">Extension (experimental)</option>
                <option value="cloud" disabled={!settings?.kernel?.configured}>Cloud (Kernel)</option>
              </Select>
            </Field>
            {browserMode === "extension" && <>
              <p className="muted small">Uses a visible local window. Some controls and embedded frames need your help. Compatibility with protected sites varies.</p>
              <button type="button" className="button secondary small" disabled={isNew || dirty} onClick={setupExtension}>Open extension browser</button>
              <p className="field-hint">The extension loads automatically in the bundled browser. No installation or Developer mode is needed.</p>
              {notice && <p className="field-hint">{notice}</p>}
            </>}
          </div>}
          <div className="card">
            <h3 className="section-title">Approvals</h3>
            <div className="choice-row" role="radiogroup" aria-label="Approvals">
              <ChoiceCard icon={<UserRound size={16} />} title="Ask for risky actions" description="Reading, browsing and memory never ask. File edits, commands and app writes ask first." selected={bot.approval_mode === "ask"} onClick={() => update({ approval_mode: "ask" })} />
              <ChoiceCard icon={<Check size={16} />} title="Act within its scope" description={bot.capabilities.shell ? "Not available while it can run shell commands: those always ask." : "Acts without asking, within the abilities above."} selected={bot.approval_mode === "auto"} disabled={bot.capabilities.shell} onClick={() => update({ approval_mode: "auto" })} />
            </div>
          </div>
          {bot.capabilities.files_read && <div className="card">
            <Field label="Workspace folder" hint="Its file tools only reach this folder. It is not an operating-system sandbox for everything else."><input value={bot.workspace_dir} onChange={event => update({ workspace_dir: event.target.value })} placeholder={`${status?.workspace_root || "~/Rex"}/${bot.name ? bot.name.toLowerCase().replace(/[^a-z0-9]+/g, "-") : "bot"}`} /></Field>
          </div>}
          <div className="card">
            <Toggle label="Suggest improvements" description="Now and then, and after corrections or problems, it looks back at recent tasks and suggests better instructions. Nothing changes until you apply it." checked={!bot.retro_off} onChange={value => update({ retro_off: !value })} />
            {!isNew && !bot.retro_off && <div className="look-back-row">
              <button type="button" className="button secondary small" disabled={lookBack !== ""} onClick={askLookBack}>{lookBack === "asked" ? "Looking back…" : "Look back now"}</button>
              {lookBackError && <small className="inline-error-text">{lookBackError}</small>}
            </div>}
          </div>
        </>}

        {tab === "knowledge" && <>
          <div className="card">
            <h3 className="section-title">Memory</h3>
            <div className="choice-row three" role="radiogroup" aria-label="Memory">
              <ChoiceCard icon={<UserRound size={16} />} title="Its own" description="Only this bot reads and writes its memories." selected={bot.memory_mode === "private"} onClick={() => update({ memory_mode: "private" })} />
              <ChoiceCard icon={<Users size={16} />} title="Shared" description={sharers.length ? `One memory with ${sharers.map(item => item.name).join(", ")}.` : "One memory with every bot set to Shared."} selected={bot.memory_mode === "shared"} onClick={() => update({ memory_mode: "shared" })} />
              <ChoiceCard icon={<CircleOff size={16} />} title="Off" description="Nothing is remembered between conversations." selected={bot.memory_mode === "off"} onClick={() => update({ memory_mode: "off" })} />
            </div>
          </div>
          <div className="card">
            <div className="row between"><h3 className="section-title">Skills</h3><span className="muted small">{bot.skills.length} on</span></div>
            {skills.length === 0 ? <p className="muted small"><BookOpen size={13} /> No skills yet. Install or write one on the Skills page.</p>
              : <div className="bot-picker">{skills.map(skill => { const active = bot.skills.includes(skill.id); return <button type="button" key={skill.id} className={`bot-chip ${active ? "on" : ""}`} aria-pressed={active} onClick={() => toggleList("skills", skill.id)}>
                <TileMark seed={skill.id} label={skill.title || skill.id} size={20} /><span>{skill.title || skill.id}</span>{active && <Check size={13} />}
              </button>; })}</div>}
          </div>
          <div className="card">
            <div className="row between"><h3 className="section-title">Apps</h3><span className="muted small">{bot.mcp_connections.length} on</span></div>
            {connections.length === 0 ? <p className="muted small"><Blocks size={13} /> No apps connected yet. Connect one from Connect apps.</p>
              : <div className="bot-picker">{connections.map(connection => { const active = bot.mcp_connections.includes(connection.id); const tools = connection.tools.filter(tool => tool.enabled).length; return <button type="button" key={connection.id} className={`bot-chip ${active ? "on" : ""}`} aria-pressed={active} onClick={() => toggleList("mcp_connections", connection.id)}>
                <AppMark app={appFor(connection)} size={20} /><span>{connection.name}</span><small className="muted">{tools} tools</small>{active && <Check size={13} />}
              </button>; })}</div>}
          </div>
        </>}

        {tab === "logins" && (isNew ? <div className="gallery-empty"><strong>Save the bot first</strong><span className="muted small">Then add the sites it may sign in to.</span></div>
          : <div className="card"><Logins bot={baseline} /></div>)}

        {dirty && <div className="save-bar" role="status">
          <span>{isNew ? "A new bot" : "You have unsaved changes"}</span><span className="spacer" />
          {!isNew && <button type="button" className="button secondary small" onClick={() => setBot(baseline)} disabled={saving}>Discard</button>}
          <button type="button" className="button primary small" disabled={saving || !bot.name.trim()} onClick={save}>{saving ? "Saving…" : isNew ? "Create bot" : "Save changes"}</button>
        </div>}
      </div>
    </section>
  </div>;
}
