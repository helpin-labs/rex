import { useCallback, useEffect, useRef, useState } from "react";
import { Select } from "./Select";
import { Check, CircleAlert, Copy, ExternalLink, FolderOpen, LoaderCircle, Plug, Plus, RefreshCw, Trash2 } from "lucide-react";
import { api } from "./api";
import { SideList } from "./SideList";
import type { ChatGPTStatus, MCPConnection, ModelInfo, Provider, ProviderTest, Settings, Status } from "./types";
import { Field, Modal, Toggle, errorMessage, openExternal, signInToConnection } from "./ui";
import { type Theme, loadTheme, saveTheme } from "./theme";

// Web search providers, in the order the bot engine tries them.
const searchProviders: { key: "tinyfish" | "exa" | "brave"; label: string; placeholder: string; hint: string }[] = [
  { key: "tinyfish", label: "TinyFish", placeholder: "tinyfish key", hint: "Recommended: fast, tried first" },
  { key: "exa", label: "Exa", placeholder: "exa key", hint: "Used for deep searches and as the first fallback" },
  { key: "brave", label: "Brave Search", placeholder: "brave key", hint: "Last fallback" },
];

const hosted: { key: Exclude<Provider, "local" | "chatgpt">; label: string; placeholder: string }[] = [
  { key: "anthropic", label: "Anthropic", placeholder: "sk-ant-…" },
  { key: "openai", label: "OpenAI", placeholder: "sk-…" },
  { key: "openrouter", label: "OpenRouter", placeholder: "sk-or-…" },
];

// ChatGPTCard signs Rex in to the user's ChatGPT subscription with a device
// code: Rex shows a code, the user enters it on ChatGPT's site, and Rex
// checks until it is approved. Bots can then use ChatGPT models.
function ChatGPTCard({ initial, onChange }: { initial?: ChatGPTStatus; onChange: (status: ChatGPTStatus) => void }) {
  // The page re-renders often with a new onChange; timers below keep
  // running and read the latest one from this ref.
  const changedRef = useRef(onChange);
  changedRef.current = onChange;
  const [status, setStatus] = useState<ChatGPTStatus>(initial || { connected: false });
  // Follow the saved sign-in, and look again when Rex comes back to the
  // front (the code is usually entered in the browser meanwhile).
  useEffect(() => { if (initial) setStatus(initial); }, [initial?.connected, initial?.pending?.user_code]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    const look = () => { if (document.visibilityState === "visible") api.chatgpt().then(next => { setStatus(next); changedRef.current(next); }).catch(() => undefined); };
    window.addEventListener("focus", look);
    document.addEventListener("visibilitychange", look);
    return () => { window.removeEventListener("focus", look); document.removeEventListener("visibilitychange", look); };
  }, []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const update = useCallback((next: ChatGPTStatus) => { setStatus(next); changedRef.current(next); }, []);
  const pending = status.pending;
  const code = pending?.user_code;
  const interval = Math.max(2, pending?.interval_seconds || 5);
  useEffect(() => {
    if (!code) return;
    const timer = window.setInterval(() => {
      api.chatgptPoll().then(update).catch(caught => { setError(errorMessage(caught)); update({ connected: false }); });
    }, interval * 1000);
    return () => window.clearInterval(timer);
  }, [code, interval, update]);
  const start = async () => {
    setBusy(true); setError("");
    try {
      const next = await api.chatgptLogin();
      update(next);
      if (next.pending) openExternal(next.pending.verification_url);
    } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); }
  };
  const signOut = async () => {
    if (!window.confirm("Sign out of ChatGPT? Bots set to ChatGPT stop working until you sign in again.")) return;
    try { update(await api.chatgptSignOut()); } catch (caught) { setError(errorMessage(caught)); }
  };
  const copy = async () => {
    if (!pending) return;
    try { await navigator.clipboard.writeText(pending.user_code); setCopied(true); window.setTimeout(() => setCopied(false), 1200); } catch { /* clipboard unavailable */ }
  };
  return <div className="card">
    <div className="row between"><h3 className="section-title">ChatGPT subscription</h3>{status.connected && <span className="badge ok-badge">Signed in</span>}</div>
    <p className="muted small">Run bots on your ChatGPT Plus or Pro plan instead of an API key. Usage counts toward your plan's limits. The sign-in is kept in your macOS Keychain.</p>
    {error && <div className="inline-error">{error}</div>}
    {pending ? <div className="device-code">
      <p className="small">Enter this code on ChatGPT's sign-in page. Rex continues by itself once you approve it.</p>
      <div className="row">
        <code className="device-code-value">{pending.user_code}</code>
        <button type="button" className="icon-button" title="Copy code" aria-label="Copy code" onClick={copy}>{copied ? <Check size={14} /> : <Copy size={14} />}</button>
        <span className="spacer" />
        <button type="button" className="button secondary small" onClick={() => openExternal(pending.verification_url)}><ExternalLink size={13} />Open sign-in page</button>
      </div>
      <small className="muted row"><LoaderCircle className="spin" size={12} /> Waiting for approval · the code works for 15 minutes</small>
    </div> : status.connected
      ? <div className="row"><button type="button" className="button secondary small" onClick={signOut}>Sign out</button></div>
      : <div className="row"><button type="button" className="button primary small" disabled={busy} onClick={start}>{busy ? "Starting…" : "Sign in with ChatGPT"}</button></div>}
  </div>;
}

export function useModels(provider: string, refresh = 0) {
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    setError("");
    api.models(provider).then(setModels).catch(caught => { setModels([]); setError(errorMessage(caught)); });
  }, [provider, refresh]);
  return { models, error };
}

function TestResult({ result }: { result: ProviderTest | null }) {
  if (!result) return null;
  return <div className={`test-result ${result.ok ? "ok" : "bad"}`}>{result.ok ? <Check size={14} /> : <CircleAlert size={14} />}<span>{result.message}</span></div>;
}

function ConnectionEditor({ initial, onClose, onSaved }: { initial: Partial<MCPConnection>; onClose: () => void; onSaved: () => void }) {
  const [connection, setConnection] = useState<Partial<MCPConnection>>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const save = async (close: boolean) => {
    setBusy(true); setError("");
    try {
      const saved = await api.saveConnection(connection);
      setConnection(saved);
      if (close) onSaved();
    } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); }
  };
  // Sign-in happens in the browser; the connection refreshes until it
  // reports signed in.
  const [waiting, setWaiting] = useState(false);
  useEffect(() => {
    if (!waiting || !connection.id) return;
    const timer = window.setInterval(async () => {
      const list = await api.connections().catch(() => []);
      const current = list.find(item => item.id === connection.id);
      if (current?.signed_in) { setConnection(current); setWaiting(false); }
    }, 1500);
    const stop = window.setTimeout(() => setWaiting(false), 10 * 60 * 1000);
    return () => { window.clearInterval(timer); window.clearTimeout(stop); };
  }, [waiting, connection.id]);
  const signIn = async () => {
    setBusy(true); setError("");
    try {
      const saved = connection.id ? connection : await api.saveConnection(connection);
      setConnection(saved);
      await signInToConnection(saved.id!);
      setWaiting(true);
    } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); }
  };
  const signOut = async () => {
    try { await api.signOut(connection.id!); setConnection({ ...connection, signed_in: false }); } catch (caught) { setError(errorMessage(caught)); }
  };
  const tool = (name: string, patch: Partial<MCPConnection["tools"][number]>) => setConnection(current => ({ ...current, tools: (current.tools || []).map(item => item.name === name ? { ...item, ...patch } : item) }));
  return <Modal title={connection.id ? connection.name || "Connection" : "Add a connection"} subtitle="MCP server" onClose={onClose} wide
    footer={<><span className="spacer" /><button type="button" className="button secondary" onClick={onClose}>Close</button><button type="button" className="button primary" disabled={busy} onClick={() => save(true)}>{busy ? "Connecting…" : "Save"}</button></>}>
    {error && <div className="inline-error">{error}</div>}
    {waiting && <div className="callout">Finish signing in in your browser. This updates by itself when you're done.</div>}
    <div className="two-col">
      <Field label="Name"><input value={connection.name || ""} onChange={event => setConnection({ ...connection, name: event.target.value })} placeholder="notion" /></Field>
      <Field label="Server URL" hint="Streamable HTTP endpoint"><input value={connection.url || ""} onChange={event => setConnection({ ...connection, url: event.target.value })} placeholder="https://mcp.example.com/mcp" /></Field>
    </div>
    <div className="two-col">
      <Field label="Authentication"><Select value={connection.auth || "none"} onChange={value => setConnection({ ...connection, auth: value as MCPConnection["auth"] })}><option value="none">None</option><option value="oauth">Sign in (OAuth)</option><option value="bearer">Bearer token</option></Select></Field>
      {connection.auth === "oauth" && <Field label="Account" hint="You sign in on the provider's page in your browser. Rex keeps the sign-in in your macOS Keychain and renews it automatically.">
        <div className="row">
          {connection.signed_in ? <span className="badge ok-badge">Signed in</span> : <span className="muted small">Not signed in</span>}
          <span className="spacer" />
          {connection.signed_in && <button type="button" className="link-button" onClick={signOut}>Sign out</button>}
          <button type="button" className="button secondary small" disabled={busy || !connection.url || !connection.name} onClick={signIn}>{connection.signed_in ? "Sign in again" : "Sign in"}</button>
        </div>
      </Field>}
      {connection.auth === "bearer" && <Field label="Token" hint={connection.has_token ? "A token is stored in your Keychain. Enter a new one to replace it." : "Stored in your macOS Keychain"}><input type="password" value={connection.token || ""} onChange={event => setConnection({ ...connection, token: event.target.value })} placeholder={connection.has_token ? "••••••••" : "token"} /></Field>}
    </div>
    <div className="row"><button type="button" className="button secondary small" disabled={busy || !connection.url} onClick={() => save(false)}><RefreshCw size={13} />{connection.id ? "Refresh tools" : "Connect and list tools"}</button>{connection.status && <span className="muted small">{connection.status}</span>}</div>
    {!!connection.tools?.length && <><h3 className="section-title">Tools</h3><p className="muted small">Write tools ask for approval on bots set to “Ask for risky actions”.</p>
      <ul className="tool-table">{connection.tools.map(item => <li key={item.name}>
        <label className="check"><input type="checkbox" checked={item.enabled} onChange={event => tool(item.name, { enabled: event.target.checked })} /><span><strong>{item.name}</strong><small>{item.description}</small></span></label>
        <Select value={item.access} onChange={value => tool(item.name, { access: value as "read" | "write" })}><option value="read">Read</option><option value="write">Write</option></Select>
      </li>)}</ul></>}
  </Modal>;
}

export function SettingsView({ status, onSaved }: { status: Status | null; onSaved: (settings: Settings) => void }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [tab, setTab] = useState<"models" | "memory" | "connections" | "about">("models");
  const [refresh, setRefresh] = useState(0);
  const local = useModels("local", refresh);
  const [test, setTest] = useState<ProviderTest | null>(null);
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [connections, setConnections] = useState<MCPConnection[]>([]);
  const [editing, setEditing] = useState<Partial<MCPConnection> | null>(null);
  const [browsers, setBrowsers] = useState<{ name: string; path: string }[]>([]);
  const [theme, setTheme] = useState<Theme>(loadTheme);
  useEffect(() => { api.browsers().then(setBrowsers).catch(() => undefined); }, []);

  useEffect(() => { api.settings().then(setSettings).catch(caught => setError(errorMessage(caught))); }, []);
  const loadConnections = useCallback(() => api.connections().then(setConnections).catch(() => undefined), []);
  useEffect(() => { void loadConnections(); }, [loadConnections]);
  if (!settings) return <div className="page"><div className="page-content"><LoaderCircle className="spin" /></div></div>;

  const update = (patch: Partial<Settings>) => setSettings({ ...settings, ...patch });
  const save = async () => {
    setSaving(true); setError(""); setMessage("");
    try {
      const saved = await api.saveSettings(settings);
      setSettings(saved); onSaved(saved); setRefresh(value => value + 1);
      setMessage("Saved. The bot engine restarts in the background if needed.");
    } catch (caught) { setError(errorMessage(caught)); } finally { setSaving(false); }
  };
  const runTest = async () => {
    setTesting(true); setTest(null);
    try {
      await api.saveSettings(settings);
      setTest(await api.testProvider("local", settings.default_provider === "local" ? settings.default_model : ""));
      setRefresh(value => value + 1);
    } catch (caught) { setError(errorMessage(caught)); } finally { setTesting(false); }
  };
  const chatModels = local.models.filter(model => !model.embedding);
  const embeddingModels = local.models.filter(model => model.embedding);

  return <div className="page">
    <SideList>
      <header className="page-list-header"><h2>Settings</h2><small className="subtitle">Models, memory and connections</small></header>
      <nav className="list">
        {(["models", "memory", "connections", "about"] as const).map(item => <button type="button" key={item} className={`list-item compact ${tab === item ? "selected" : ""}`} onClick={() => setTab(item)}>
          <span className="list-text"><strong>{item === "models" ? "Models" : item === "memory" ? "Memory" : item === "connections" ? "Connections" : "General"}</strong></span></button>)}
      </nav>
    </SideList>
    <section className="page-content narrow">
      {error && <div className="inline-error">{error}</div>}
      {message && <div className="inline-ok"><Check size={14} />{message}</div>}
      {tab === "models" && <>
        <h1>Models</h1>
        <div className="card">
          <h3 className="section-title">Local model server</h3>
          <p className="muted small">LM Studio, Ollama or any OpenAI-compatible server on this Mac. Nothing leaves your computer.</p>
          <Field label="Server URL" hint="LM Studio: http://127.0.0.1:1234/v1 · Ollama: http://127.0.0.1:11434/v1">
            <input value={settings.local.base_url} onChange={event => update({ local: { ...settings.local, base_url: event.target.value } })} />
          </Field>
          <Field label="API key (optional)" hint={settings.local.configured ? "A key is stored in your Keychain." : undefined}>
            <input type="password" value={settings.local.api_key || ""} placeholder={settings.local.configured ? "••••••••" : "none"} onChange={event => update({ local: { ...settings.local, api_key: event.target.value } })} />
          </Field>
          <div className="row"><button type="button" className="button secondary small" onClick={runTest} disabled={testing}>{testing ? <><LoaderCircle className="spin" size={13} />Testing (this can take a minute)…</> : "Test connection"}</button>
            <span className="muted small">{local.error ? local.error : `${chatModels.length} chat models found`}</span></div>
          <TestResult result={test} />
        </div>
        <div className="card">
          <h3 className="section-title">Hosted providers</h3>
          <p className="muted small">Keys are stored in your macOS Keychain and never shown again.</p>
          {hosted.map(item => <Field key={item.key} label={item.label} hint={settings[item.key].configured ? <span className="row">Key stored. <button type="button" className="link-button" onClick={() => update({ [item.key]: { ...settings[item.key], clear_key: true } } as Partial<Settings>)}>Remove</button></span> : undefined}>
            <input type="password" placeholder={settings[item.key].configured ? "••••••••" : item.placeholder} value={settings[item.key].api_key || ""} onChange={event => update({ [item.key]: { ...settings[item.key], api_key: event.target.value, clear_key: false } } as Partial<Settings>)} />
          </Field>)}
        </div>
        <ChatGPTCard initial={settings.chatgpt} onChange={chatgpt => update({ chatgpt })} />
        <div className="card">
          <h3 className="section-title">Web search</h3>
          <p className="muted small">Lets bots with web access search the internet, not only read pages they already know. Add at least one key; if a provider fails, the next one is tried. Keys are stored in your macOS Keychain.</p>
          {searchProviders.map(item => {
            const config = settings[item.key] || { base_url: "", configured: false };
            return <Field key={item.key} label={item.label} hint={config.configured ? <span className="row">Key stored. <button type="button" className="link-button" onClick={() => update({ [item.key]: { ...config, clear_key: true } } as Partial<Settings>)}>Remove</button></span> : item.hint}>
              <input type="password" placeholder={config.configured ? "••••••••" : item.placeholder} value={config.api_key || ""} onChange={event => update({ [item.key]: { ...config, api_key: event.target.value, clear_key: false } } as Partial<Settings>)} />
            </Field>;
          })}
          {!searchProviders.some(item => settings[item.key]?.configured) && <div className="callout">Search is off: bots can still open and read web pages, but they can't look things up.</div>}
        </div>
        <div className="card">
          <h3 className="section-title">Command review (TypeSafe)</h3>
          <p className="muted small">Bots set to “Ask for risky actions” never ask about reading, browsing, screenshots or memory. File edits and commands ask, unless TypeSafe's jev model reviews them and finds a routine local step. Reviews send the proposed command and recent conversation context to TypeSafe. Deletions, pushes, installs and network commands always ask.</p>
          <Field label="TypeSafe API key" hint={settings.typesafe?.configured ? <span className="row">Key stored in your Keychain. <button type="button" className="link-button" onClick={() => update({ typesafe: { ...settings.typesafe, clear_key: true } })}>Remove</button></span> : "Stored in your macOS Keychain"}>
            <input type="password" placeholder={settings.typesafe?.configured ? "••••••••" : "ts_…"} value={settings.typesafe?.api_key || ""} onChange={event => update({ typesafe: { ...(settings.typesafe || { base_url: "", configured: false }), api_key: event.target.value, clear_key: false } })} />
          </Field>
          <Toggle label="Let TypeSafe approve routine steps" description="Without this, TypeSafe only adds extra checks; you still approve every edit and command" checked={settings.typesafe_auto_approve} disabled={!settings.typesafe?.configured && !settings.typesafe?.api_key} onChange={typesafe_auto_approve => update({ typesafe_auto_approve })} />
        </div>
        <div className="card">
          <h3 className="section-title">Default for new bots</h3>
          <div className="two-col">
            <Field label="Provider"><Select value={settings.default_provider} onChange={value => update({ default_provider: value as Provider, default_model: "" })}>
              <option value="local">Local model server</option>{hosted.map(item => <option key={item.key} value={item.key}>{item.label}</option>)}{settings.chatgpt?.connected && <option value="chatgpt">ChatGPT subscription</option>}</Select></Field>
            <Field label="Model">{settings.default_provider === "local" && chatModels.length ? <Select value={settings.default_model} onChange={value => update({ default_model: value })}><option value="">Choose…</option>{chatModels.map(model => <option key={model.id}>{model.id}</option>)}</Select>
              : <input value={settings.default_model} onChange={event => update({ default_model: event.target.value })} placeholder="provider default" />}</Field>
          </div>
          <Toggle label="Allow bots to use a browser" description="Each bot gets its own browser profile, separate from yours" checked={settings.browser_enabled} onChange={browser_enabled => update({ browser_enabled })} />
          {settings.browser_enabled && <>
            <Field label="Browser for bots" hint="Any installed Chromium-based browser works. Safari and Firefox can't be automated.">
              <Select value={settings.browser_path || ""} onChange={value => update({ browser_path: value })}>
                <option value="">{browsers.length ? `Automatic (${browsers[0].name})` : "Built-in test browser"}</option>
                {browsers.map(browser => <option key={browser.path} value={browser.path}>{browser.name}</option>)}
              </Select>
            </Field>
            {!settings.kernel_browser && <Toggle label="Show bot browsers on screen" description="Bots browse in a normal window you can watch. Turn off to keep them in the background (watch them from the chat instead)." checked={!settings.browser_hidden} onChange={visible => update({ browser_hidden: !visible })} />}
            <div className="subsection">
              <Field label="Kernel API key (cloud browsers)" hint={settings.kernel?.configured ? <span className="row">Key stored in your Keychain. <button type="button" className="link-button" onClick={() => update({ kernel: { ...settings.kernel!, clear_key: true }, kernel_browser: false })}>Remove</button></span> : <>From <button type="button" className="link-button" onClick={() => openExternal("https://www.kernel.sh")}>kernel.sh</button>. Stored in your macOS Keychain.</>}>
                <input type="password" placeholder={settings.kernel?.configured ? "••••••••" : "sk_…"} value={settings.kernel?.api_key || ""} onChange={event => update({ kernel: { ...(settings.kernel || { base_url: "", configured: false }), api_key: event.target.value, clear_key: false } })} />
              </Field>
              <Toggle label="Browse in Kernel's cloud" description="Every bot gets a fresh cloud browser for each task instead of a browser on this Mac. Saved logins, taking over the browser and filling in your saved passwords don't work there. If Kernel credits run out, bots fall back to this Mac."
                checked={!!settings.kernel_browser} disabled={!settings.kernel?.configured && !settings.kernel?.api_key} onChange={kernel_browser => update({ kernel_browser })} />
            </div>
          </>}
        </div>
        <div className="row"><span className="spacer" /><button type="button" className="button primary" onClick={save} disabled={saving}>{saving ? "Saving…" : "Save"}</button></div>
      </>}
      {tab === "memory" && <>
        <h1>Memory</h1>
        <p className="muted">After each answer, Rex reads what you said and keeps durable facts, such as preferences, people and plans, in a local database. This happens in the background and never delays a reply. Before each new message, relevant memories are given to the bot.</p>
        <div className="card">
          <Toggle label="Remember things automatically" checked={settings.memory.enabled} onChange={enabled => update({ memory: { ...settings.memory, enabled } })} />
          <Field label="Run memory with"><Select value={settings.memory.provider} onChange={value => update({ memory: { ...settings.memory, provider: value as "local" | "openai" } })}><option value="local">Local model server</option><option value="openai" disabled={!settings.openai.configured}>OpenAI{settings.openai.configured ? "" : " (add key first)"}</option></Select></Field>
          <div className="two-col">
            <Field label="Model for saving memories" hint="Each bot's memories are extracted by its own chat model, so your model server never has to swap models. Finding memories only uses the embedding model.">{settings.memory.provider === "local" && chatModels.length ? <Select value={settings.memory.extraction_model} onChange={value => update({ memory: { ...settings.memory, extraction_model: value } })}><option value="">Each bot's own chat model</option>{chatModels.map(model => <option key={model.id}>{model.id}</option>)}</Select>
              : <input value={settings.memory.extraction_model} onChange={event => update({ memory: { ...settings.memory, extraction_model: event.target.value } })} placeholder="gpt-5.6-mini" />}</Field>
            <Field label="Finding memories" hint="The embedding model turns memories and questions into vectors. The built-in one runs inside Rex (about 180 MB of memory) and needs no model server.">
              <Select value={settings.memory.embedding_source === "server" ? "server" : "bundled"} onChange={value => update({ memory: { ...settings.memory, embedding_source: value as "bundled" | "server" } })}>
                <option value="bundled">Built into Rex (Bekko)</option>
                <option value="server">From the model server</option>
              </Select>
            </Field>
          </div>
          {settings.memory.embedding_source === "server" && <Field label="Embedding model on the server">{settings.memory.provider === "local" && embeddingModels.length ? <Select value={settings.memory.embedding_model} onChange={value => update({ memory: { ...settings.memory, embedding_model: value } })}><option value="">Choose…</option>{embeddingModels.map(model => <option key={model.id}>{model.id}</option>)}</Select>
              : <input value={settings.memory.embedding_model} onChange={event => update({ memory: { ...settings.memory, embedding_model: event.target.value } })} placeholder="text-embedding-3-small" />}</Field>}
          <p className="muted small">Changing how memories are found rebuilds memory: the old database is kept as a backup and every remembered source is learned again in the background.</p>
          <p className="muted small memory-status"><span className={`status-dot ${status?.memory.status === "ready" ? "ok" : ""}`} />Memory is {status?.memory.status || "starting"}</p>
        </div>
        <div className="row"><span className="spacer" /><button type="button" className="button primary" onClick={save} disabled={saving}>{saving ? "Saving…" : "Save"}</button></div>
      </>}
      {tab === "connections" && <>
        <div className="row between"><h1>Connections</h1><button type="button" className="button primary small" onClick={() => setEditing({ auth: "none", tools: [] })}><Plus size={14} />Add connection</button></div>
        <p className="muted">Connect MCP servers to give bots tools such as notes, calendars or project trackers. Choose which bots use each connection in the bot's settings.</p>
        {connections.length === 0 ? <div className="card muted">No connections yet.</div> : connections.map(connection => <div className="card connection" key={connection.id}>
          <Plug size={16} /><div className="grow"><strong>{connection.name}</strong><small className="muted">{connection.url}</small><small className="muted">{connection.status}</small></div>
          <button type="button" className="button secondary small" onClick={() => setEditing(connection)}>Manage</button>
          <button type="button" className="icon-button ghost" onClick={async () => { if (window.confirm(`Remove ${connection.name}?`)) { await api.deleteConnection(connection.id); void loadConnections(); } }}><Trash2 size={14} /></button>
        </div>)}
      </>}
      {tab === "about" && <>
        <h1>General</h1>
        <div className="card">
          <h3 className="section-title">Appearance</h3>
          <div className="segmented">
            {(["system", "light", "dark"] as const).map(option => <button type="button" key={option} className={theme === option ? "active" : ""} onClick={() => { setTheme(option); saveTheme(option); }}>{option === "system" ? "System" : option === "light" ? "Light" : "Dark"}</button>)}
          </div>
          <p className="muted small">System follows your Mac's light or dark setting.</p>
        </div>
        <div className="card">
          <h3 className="section-title">Notifications</h3>
          <Toggle label="Notify me when a bot needs me" description="A Mac notification when a bot asks for approval or a question, or a routine finishes, while Rex is in the background. The Dock icon shows how many conversations are waiting."
            checked={!settings.notifications_off} onChange={async on => {
              const next = { ...settings, notifications_off: !on };
              setSettings(next);
              try { const saved = await api.saveSettings(next); setSettings(saved); onSaved(saved); } catch (caught) { setError(errorMessage(caught)); }
            }} />
        </div>
        <div className="card">
          <dl className="facts-list">
            <dt>Bot engine</dt><dd>{status?.runtime_ready ? "Running" : "Starting"}</dd>
            <dt>Data folder</dt><dd><code>{status?.data_dir}</code></dd>
            <dt>Bot workspaces</dt><dd><code>{status?.workspace_root}</code></dd>
            <dt>Credentials</dt><dd>macOS Keychain</dd>
          </dl>
          <button type="button" className="button secondary small" onClick={() => api.revealLogs()}><FolderOpen size={13} />Show logs</button>
        </div>
        <p className="muted small">Rex runs bots on this Mac. A bot's workspace folder limits Rex's file tools but is not an operating-system sandbox. Routines need the Mac awake and Rex running.</p>
      </>}
    </section>
    {editing && <ConnectionEditor initial={editing} onClose={() => { setEditing(null); void loadConnections(); }} onSaved={() => { setEditing(null); void loadConnections(); }} />}
  </div>;
}
