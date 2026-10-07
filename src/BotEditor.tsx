import { useEffect, useState } from "react";
import { Trash2 } from "lucide-react";
import { api } from "./api";
import type { Bot, MCPConnection, ModelInfo, Provider, Settings, Skill, Status } from "./types";
import { Avatar, Field, Modal, Toggle, errorMessage } from "./ui";
import { BotFace, botColors, faceJob, faces, suggestFace } from "./BotFace";
import { ColorPicker } from "./NewBotDialog";
import { Logins } from "./Logins";
const providerNames: Record<Provider, string> = { local: "Local model server", anthropic: "Anthropic", openai: "OpenAI", openrouter: "OpenRouter" };

export function newBot(settings: Settings | null): Bot {
  return {
    id: "", name: "", emoji: "bot", color: botColors[Math.floor(Math.random() * botColors.length)],
    instructions: "", provider: settings?.default_provider || "local", model: settings?.default_model || "",
    capabilities: { files_read: false, files_write: false, shell: false, web: true, browser: false, memory: true },
    approval_mode: "ask", workspace_dir: "", memory_mode: "private", skills: [], mcp_connections: [], position: 0,
  };
}

export function BotEditor({ initial, settings, status, onClose, onSaved, onDeleted }: {
  initial: Bot; settings: Settings | null; status: Status | null; onClose: () => void; onSaved: (bot: Bot) => void; onDeleted: (id: string) => void;
}) {
  const [bot, setBot] = useState<Bot>(initial);
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [modelError, setModelError] = useState("");
  const [skills, setSkills] = useState<Skill[]>([]);
  const [connections, setConnections] = useState<MCPConnection[]>([]);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const isNew = !initial.id;
  const writeTools = !!status?.write_tools;

  const [allBots, setAllBots] = useState<Bot[]>([]);
  useEffect(() => { api.skills().then(setSkills).catch(() => undefined); api.connections().then(setConnections).catch(() => undefined); api.bots().then(setAllBots).catch(() => undefined); }, []);
  const sharers = allBots.filter(item => item.id !== bot.id && item.memory_mode === "shared");
  useEffect(() => {
    setModelError("");
    api.models(bot.provider).then(list => setModels(list.filter(model => !model.embedding))).catch(caught => { setModels([]); setModelError(errorMessage(caught)); });
  }, [bot.provider]);

  // A new bot's face follows its name until the user picks one.
  const [facePicked, setFacePicked] = useState(!isNew);
  const update = (patch: Partial<Bot>) => setBot(current => {
    const next = { ...current, ...patch };
    if (!facePicked && ("name" in patch || "instructions" in patch)) next.emoji = suggestFace(next.name, next.instructions);
    return next;
  });
  const pickFace = (emoji: string) => { setFacePicked(true); setBot(current => ({ ...current, emoji })); };
  const currentFace = faceJob(bot.emoji);
  const capability = (key: keyof Bot["capabilities"], value: boolean) => setBot(current => {
    const capabilities = { ...current.capabilities, [key]: value };
    if (key === "files_write" && value) capabilities.files_read = true;
    if (key === "files_read" && !value) { capabilities.files_write = false; capabilities.shell = false; }
    return { ...current, capabilities };
  });
  const toggleList = (key: "skills" | "mcp_connections", id: string) => setBot(current => ({ ...current, [key]: current[key].includes(id) ? current[key].filter(item => item !== id) : [...current[key], id] }));

  const save = async () => {
    setSaving(true); setError("");
    try {
      const saved = isNew ? await api.createBot(bot) : await api.saveBot(bot);
      onSaved(saved);
    } catch (caught) { setError(errorMessage(caught)); } finally { setSaving(false); }
  };
  const remove = async () => {
    if (!window.confirm(`Delete ${bot.name}? Its conversations are removed. Files in its workspace folder stay on disk.`)) return;
    try { await api.deleteBot(bot.id); onDeleted(bot.id); } catch (caught) { setError(errorMessage(caught)); }
  };

  const footer = <>
    {!isNew && bot.role !== "main" && <button type="button" className="button danger-ghost" onClick={remove}><Trash2 size={14} />Delete bot</button>}
    <span className="spacer" />
    <button type="button" className="button secondary" onClick={onClose}>Cancel</button>
    <button type="button" className="button primary" disabled={saving || !bot.name.trim()} onClick={save}>{saving ? "Saving…" : isNew ? "Create bot" : "Save"}</button>
  </>;

  return <Modal title={isNew ? "New bot" : bot.name} subtitle={isNew ? "Create" : "Bot settings"} onClose={onClose} footer={footer} wide>
    {error && <div className="inline-error">{error}</div>}
    <div className="editor-grid">
      <section>
        <h3 className="section-title">Identity</h3>
        <div className="identity-row">
          <Avatar bot={bot} size={68} />
          <Field label="Name"><input value={bot.name} onChange={event => update({ name: event.target.value })} placeholder="e.g. Travel planner" autoFocus /></Field>
        </div>
        <div className="face-picker">
          <span className="field-label">Face{!facePicked ? " · suggested from the name" : ""}</span>
          <div className="face-choices">{faces.map(item => <button type="button" key={item.job} title={item.label} aria-label={item.label} className={`face-choice ${currentFace === item.job ? "selected" : ""}`} onClick={() => pickFace(item.job)}>
            <BotFace icon={item.job} color={bot.color} seed={item.job} size={40} />
          </button>)}</div>
          <span className="field-label">Colour</span>
          <ColorPicker value={bot.color} onChange={color => update({ color })} />
        </div>
        <Field label="Instructions" hint="Who this bot is and how it should work. Skills below add reusable instructions.">
          <textarea className="instructions-input" rows={6} value={bot.instructions} onChange={event => update({ instructions: event.target.value })} placeholder="You plan trips. Always suggest train options before flights…" />
        </Field>
        <h3 className="section-title">Model</h3>
        <div className="two-col">
          <Field label="Provider">
            <select value={bot.provider} onChange={event => update({ provider: event.target.value as Provider, model: "" })}>
              {(Object.keys(providerNames) as Provider[]).map(provider => <option key={provider} value={provider} disabled={provider !== "local" && settings ? !settings[provider].configured : false}>{providerNames[provider]}{provider !== "local" && settings && !settings[provider].configured ? " (add key in Settings)" : ""}</option>)}
            </select>
          </Field>
          <Field label="Model" hint={modelError || (bot.provider === "local" ? "Models your local server has available" : undefined)}>
            {models.length ? <select value={bot.model} onChange={event => update({ model: event.target.value })}>
              <option value="">{bot.provider === "local" ? "Choose a model" : "Provider default"}</option>
              {models.map(model => <option key={model.id} value={model.id}>{model.id}</option>)}
            </select> : <input value={bot.model} onChange={event => update({ model: event.target.value })} placeholder="model id" />}
          </Field>
        </div>
      </section>
      <section>
        <h3 className="section-title">What it can do</h3>
        <div className="toggles">
          <Toggle label="Search and read the web" description="Web search and reading public pages" checked={bot.capabilities.web} onChange={value => capability("web", value)}
            note={bot.capabilities.web && status && !status.web_search ? "Search is off until you add a search key in Settings → Models. Reading pages works." : undefined} />
          <Toggle label="Use a browser" description="Its own browser with a persistent profile; you can take over for logins" checked={bot.capabilities.browser} onChange={value => capability("browser", value)} disabled={settings ? !settings.browser_enabled : false} />
          <Toggle label="Read files" description="Files in its own workspace folder only" checked={bot.capabilities.files_read} onChange={value => capability("files_read", value)} />
          <Toggle label="Create and edit files" description="Inside its workspace folder" checked={bot.capabilities.files_write} onChange={value => capability("files_write", value)} disabled={!writeTools} note={writeTools ? undefined : "Needs the macOS sandbox, which is not active in the bot engine."} />
          <Toggle label="Run shell commands" description="Runs in a macOS sandbox limited to its folder; asks first unless TypeSafe clears it" checked={bot.capabilities.shell} onChange={value => capability("shell", value)} disabled={!writeTools} />
        </div>
        <Field label="Approvals" hint={bot.approval_mode === "ask" ? "Reading, browsing and memory never ask. File edits, commands and connection writes ask." : "Acts without asking, within the permissions above."}>
          <div className="segmented">
            <button type="button" className={bot.approval_mode === "ask" ? "active" : ""} onClick={() => update({ approval_mode: "ask" })}>Ask for risky actions</button>
            <button type="button" className={bot.approval_mode === "auto" ? "active" : ""} disabled={bot.capabilities.shell} onClick={() => update({ approval_mode: "auto" })}>Act within its scope</button>
          </div>
        </Field>
        {bot.capabilities.files_read && <Field label="Workspace folder" hint="A workspace folder is not an OS sandbox; it limits Rex's file tools."><input value={bot.workspace_dir} onChange={event => update({ workspace_dir: event.target.value })} placeholder={`${status?.workspace_root || "~/Rex"}/${bot.name ? bot.name.toLowerCase().replace(/[^a-z0-9]+/g, "-") : "bot"}`} /></Field>}
        <Field label="Memory">
          <div className="segmented">
            {(["private", "shared", "off"] as const).map(mode => <button type="button" key={mode} className={bot.memory_mode === mode ? "active" : ""} onClick={() => update({ memory_mode: mode })}>{mode === "private" ? "Its own" : mode === "shared" ? "Shared" : "Off"}</button>)}
          </div>
        </Field>
        <p className="muted small">{bot.memory_mode === "private" ? "Only this bot reads and writes its memories." : bot.memory_mode === "shared"
          ? sharers.length ? `Shares one memory with ${sharers.map(item => item.name).join(", ")}. What one learns, the others can use.` : "Shares one memory with every bot set to Shared. No other bot uses it yet."
          : "Nothing is remembered between conversations."}</p>
        {skills.length > 0 && <><h3 className="section-title">Skills</h3><div className="check-list">{skills.map(skill => <label key={skill.id} className="check"><input type="checkbox" checked={bot.skills.includes(skill.id)} onChange={() => toggleList("skills", skill.id)} /><span>{skill.title}</span></label>)}</div></>}
        {!isNew && <Logins bot={bot} />}
        {connections.length > 0 && <><h3 className="section-title">Connections</h3><div className="check-list">{connections.map(connection => <label key={connection.id} className="check"><input type="checkbox" checked={bot.mcp_connections.includes(connection.id)} onChange={() => toggleList("mcp_connections", connection.id)} /><span>{connection.name}</span><small>{connection.tools.filter(tool => tool.enabled).length} tools</small></label>)}</div></>}
      </section>
    </div>
  </Modal>;
}
