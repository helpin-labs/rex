import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { Select } from "./Select";
import { Bell, Check, CircleAlert, Copy, ExternalLink, FolderOpen, Globe, LoaderCircle, Palette, ShieldCheck } from "lucide-react";
import { api } from "./api";
import { SideList } from "./SideList";
import type { ChatGPTStatus, ModelInfo, Provider, ProviderTest, Settings, Status } from "./types";
import { Field, PageHeader, TileMark, Toggle, errorMessage, openExternal } from "./ui";
import { type Theme, loadTheme, saveTheme } from "./theme";
import { siAnthropic, siBrave, siLmstudio, siOpenrouter } from "simple-icons";
import { AppMark } from "./AppsView";

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
  return <div className="card provider-card">
    <ProviderHead logo={providerLogos.chatgpt} title="ChatGPT subscription" note="Run bots on your ChatGPT Plus or Pro plan instead of an API key. Usage counts toward your plan's limits." status={status.connected ? <span className="chip ok">Signed in</span> : pending ? <span className="chip warn">Waiting for approval</span> : <span className="chip">Not signed in</span>} />
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

const logo = (name: string, icon?: { path: string; hex: string }, color = "#1f2125") => ({ name, icon: icon && { path: icon.path, hex: icon.hex }, color });
const providerLogos = {
  local: logo("LM Studio", siLmstudio, "#4338CA"),
  anthropic: logo("Anthropic", siAnthropic, "#191919"),
  openai: logo("OpenAI", undefined, "#0D0D0D"),
  openrouter: logo("OpenRouter", siOpenrouter, "#6566F1"),
  chatgpt: logo("ChatGPT", undefined, "#10A37F"),
  brave: logo("Brave", siBrave, "#FB542B"),
  exa: logo("Exa", undefined, "#1F40ED"),
  tinyfish: logo("TinyFish", undefined, "#0EA5E9"),
};

// ProviderHead is a provider card's first row: logo, name, what it is for,
// and whether it is set up.
function ProviderHead({ logo: mark, title, note, status }: { logo: { name: string; icon?: { path: string; hex: string }; color: string }; title: string; note?: ReactNode; status?: ReactNode }) {
  return <div className="provider-head">
    <AppMark app={mark} size={36} />
    <div className="grow"><strong>{title}</strong>{note && <small className="muted">{note}</small>}</div>
    {status}
  </div>;
}

const keyChip = (configured: boolean) => configured ? <span className="chip ok"><Check size={11} />Key stored</span> : <span className="chip">Not set</span>;

// Comparing for unsaved changes ignores the ChatGPT sign-in, which saves
// itself.
const comparable = (settings: Settings | null) => settings ? JSON.stringify({ ...settings, chatgpt: undefined }) : "";

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

export function SettingsView({ status, onSaved }: { status: Status | null; onSaved: (settings: Settings) => void }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [savedSettings, setSavedSettings] = useState<Settings | null>(null);
  const [tab, setTab] = useState<"models" | "tools" | "memory" | "about">("models");
  const [refresh, setRefresh] = useState(0);
  const local = useModels("local", refresh);
  const defaultModels = useModels(settings?.default_provider || "local", refresh);
  const [test, setTest] = useState<ProviderTest | null>(null);
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [browsers, setBrowsers] = useState<{ name: string; path: string }[]>([]);
  const [theme, setTheme] = useState<Theme>(loadTheme);
  useEffect(() => { api.browsers().then(setBrowsers).catch(() => undefined); }, []);

  useEffect(() => { api.settings().then(loaded => { setSettings(loaded); setSavedSettings(loaded); }).catch(caught => setError(errorMessage(caught))); }, []);
  if (!settings) return <div className="page"><div className="page-content"><LoaderCircle className="spin" /></div></div>;

  const dirty = comparable(settings) !== comparable(savedSettings);
  const update = (patch: Partial<Settings>) => { setSettings({ ...settings, ...patch }); setMessage(""); };
  const save = async () => {
    setSaving(true); setError(""); setMessage("");
    try {
      const saved = await api.saveSettings(settings);
      setSettings(saved); setSavedSettings(saved); onSaved(saved); setRefresh(value => value + 1);
      setMessage("Saved. The bot engine restarts in the background if needed.");
    } catch (caught) { setError(errorMessage(caught)); } finally { setSaving(false); }
  };
  const discard = () => { setSettings(savedSettings); setError(""); };
  const runTest = async () => {
    setTesting(true); setTest(null);
    try {
      const saved = await api.saveSettings(settings);
      setSettings(saved); setSavedSettings(saved);
      setTest(await api.testProvider("local", settings.default_provider === "local" ? settings.default_model : ""));
      setRefresh(value => value + 1);
    } catch (caught) { setError(errorMessage(caught)); } finally { setTesting(false); }
  };
  const chatModels = local.models.filter(model => !model.embedding);
  const embeddingModels = local.models.filter(model => model.embedding);
  const defaultChoices = defaultModels.models.filter(model => !model.embedding);
  const tabs = [["models", "Models"], ["tools", "Tools"], ["memory", "Memory"], ["about", "General"]] as const;
  const searchOn = searchProviders.some(item => settings[item.key]?.configured);
  const keyField = (key: "anthropic" | "openai" | "openrouter" | "tinyfish" | "exa" | "brave", placeholder: string, hint?: ReactNode, label = "API key") => {
    const config = settings[key] || { base_url: "", configured: false };
    return <Field label={label} hint={config.configured ? <span className="row">Stored in your macOS Keychain. <button type="button" className="link-button" onClick={() => update({ [key]: { ...config, clear_key: true, api_key: "" } } as Partial<Settings>)}>Remove</button></span> : hint || "Stored in your macOS Keychain"}>
      <input type="password" placeholder={config.configured ? "••••••••" : placeholder} value={config.api_key || ""} onChange={event => update({ [key]: { ...config, api_key: event.target.value, clear_key: false } } as Partial<Settings>)} />
    </Field>;
  };

  return <div className="page">
    <SideList>
      <header className="page-list-header"><h2>Settings</h2><small className="subtitle">Models, tools, memory and appearance</small></header>
      <nav className="list">
        {tabs.map(([item, label]) => <button type="button" key={item} className={`list-item compact ${tab === item ? "selected" : ""}`} onClick={() => setTab(item)}>
          <span className="list-text"><strong>{label}</strong></span></button>)}
      </nav>
    </SideList>
    <section className="page-content narrow settings-page">
      {error && <div className="inline-error"><CircleAlert size={14} /><span>{error}</span></div>}
      {message && !dirty && <div className="inline-ok"><Check size={14} />{message}</div>}

      {tab === "models" && <>
        <PageHeader title="Models" description="Where your bots' thinking happens: a model server on this Mac, your API keys, or your ChatGPT plan. Keys never leave your Keychain except to call the provider." />
        <div className="card">
          <h3 className="section-title">Default for new bots</h3>
          <p className="muted small">Bots without their own model use this one too.</p>
          <div className="two-col">
            <Field label="Provider"><Select value={settings.default_provider} onChange={value => update({ default_provider: value as Provider, default_model: "" })}>
              <option value="local">Local model server</option>{hosted.map(item => <option key={item.key} value={item.key}>{item.label}</option>)}{settings.chatgpt?.connected && <option value="chatgpt">ChatGPT subscription</option>}</Select></Field>
            <Field label="Model" hint={defaultChoices.length ? `${defaultChoices.length} models available. Start typing to search.` : undefined}>
              <input list="default-models" value={settings.default_model} onChange={event => update({ default_model: event.target.value })} placeholder="provider default" />
              <datalist id="default-models">{defaultChoices.map(model => <option key={model.id} value={model.id} />)}</datalist>
            </Field>
          </div>
        </div>

        <h3 className="gallery-section-title">Providers</h3>
        <div className="card provider-card">
          <ProviderHead logo={providerLogos.local} title="Local model server" note="LM Studio, Ollama or any OpenAI-compatible server on this Mac. Nothing leaves your computer."
            status={local.error ? <span className="chip warn" title={local.error}>Not running</span> : <span className="chip ok">{chatModels.length} models</span>} />
          <div className="two-col">
            <Field label="Server URL" hint="LM Studio: http://127.0.0.1:1234/v1 · Ollama: http://127.0.0.1:11434/v1">
              <input value={settings.local.base_url} onChange={event => update({ local: { ...settings.local, base_url: event.target.value } })} />
            </Field>
            <Field label="API key (optional)" hint={settings.local.configured ? "A key is stored in your Keychain." : undefined}>
              <input type="password" value={settings.local.api_key || ""} placeholder={settings.local.configured ? "••••••••" : "none"} onChange={event => update({ local: { ...settings.local, api_key: event.target.value } })} />
            </Field>
          </div>
          {local.error && !test && <p className="muted small">Rex can't reach a model server at this address. Start LM Studio or Ollama, then test again.</p>}
          <div className="row"><button type="button" className="button secondary small" onClick={runTest} disabled={testing}>{testing ? <><LoaderCircle className="spin" size={13} />Testing (this can take a minute)…</> : "Test connection"}</button></div>
          <TestResult result={test} />
        </div>
        {hosted.map(item => <div className="card provider-card" key={item.key}>
          <ProviderHead logo={providerLogos[item.key]} title={item.label} status={keyChip(settings[item.key].configured && !settings[item.key].clear_key)}
            note={item.key === "anthropic" ? "Claude models, with your Anthropic API key." : item.key === "openai" ? "GPT models, with your OpenAI API key." : "Hundreds of models from many labs, with one key."} />
          {keyField(item.key, item.placeholder)}
        </div>)}
        <ChatGPTCard initial={settings.chatgpt} onChange={chatgpt => update({ chatgpt })} />
      </>}

      {tab === "tools" && <>
        <PageHeader title="Tools" description="What bots can use beyond their model: searching the web, a browser of their own, and a second opinion on risky steps." />
        <div className="card">
          <div className="provider-head"><TileMark seed="search" icon={<Globe size={18} />} size={36} /><div className="grow"><strong>Web search</strong><small className="muted">Lets bots look things up, not only read pages they already know. If a provider fails, the next one is tried.</small></div>
            {searchOn ? <span className="chip ok">On</span> : <span className="chip warn">Off</span>}</div>
          {searchProviders.map(item => {
            const config = settings[item.key] || { base_url: "", configured: false };
            return <div className="provider-row" key={item.key}>
              <AppMark app={providerLogos[item.key]} size={28} />
              <div className="grow">{keyField(item.key, item.placeholder, item.hint, item.label)}</div>
              {config.configured && <span className="chip ok"><Check size={11} />Stored</span>}
            </div>;
          })}
          {!searchOn && <div className="callout">Search is off: bots can still open and read web pages, but they can't look things up. Add at least one key.</div>}
        </div>
        <div className="card">
          <div className="provider-head"><TileMark seed="browser" icon={<Globe size={18} />} size={36} /><div className="grow"><strong>Browser</strong><small className="muted">Each bot gets its own browser profile, separate from yours, so its logins persist.</small></div>
            {settings.browser_enabled ? <span className="chip ok">On</span> : <span className="chip">Off</span>}</div>
          <Toggle label="Allow bots to use a browser" checked={settings.browser_enabled} onChange={browser_enabled => update({ browser_enabled })} />
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
        <div className="card">
          <div className="provider-head"><TileMark seed="review" icon={<ShieldCheck size={18} />} size={36} /><div className="grow"><strong>Command review (TypeSafe)</strong><small className="muted">A second opinion on file edits and commands, so routine local steps don't need your approval.</small></div>
            {keyChip(!!settings.typesafe?.configured)}</div>
          <p className="muted small">Bots set to “Ask for risky actions” never ask about reading, browsing, screenshots or memory. File edits and commands ask, unless TypeSafe's jev model reviews them and finds a routine local step. Reviews send the proposed command and recent conversation context to TypeSafe. Deletions, pushes, installs and network commands always ask.</p>
          <Field label="TypeSafe API key" hint={settings.typesafe?.configured ? <span className="row">Key stored in your Keychain. <button type="button" className="link-button" onClick={() => update({ typesafe: { ...settings.typesafe, clear_key: true } })}>Remove</button></span> : "Stored in your macOS Keychain"}>
            <input type="password" placeholder={settings.typesafe?.configured ? "••••••••" : "ts_…"} value={settings.typesafe?.api_key || ""} onChange={event => update({ typesafe: { ...(settings.typesafe || { base_url: "", configured: false }), api_key: event.target.value, clear_key: false } })} />
          </Field>
          <Toggle label="Let TypeSafe approve routine steps" description="Without this, TypeSafe only adds extra checks; you still approve every edit and command" checked={settings.typesafe_auto_approve} disabled={!settings.typesafe?.configured && !settings.typesafe?.api_key} onChange={typesafe_auto_approve => update({ typesafe_auto_approve })} />
        </div>
      </>}

      {tab === "memory" && <>
        <PageHeader title="Memory" description="After each answer, Rex keeps durable facts from what you said, such as preferences, people and plans, in a local database. It happens in the background and never delays a reply. Before each new message, relevant memories are given to the bot." />
        <div className="card">
          <div className="row between"><h3 className="section-title">Status</h3><span className={`chip ${status?.memory.status === "ready" ? "ok" : "warn"}`}>Memory is {status?.memory.status || "starting"}</span></div>
          <Toggle label="Remember things automatically" checked={settings.memory.enabled} onChange={enabled => update({ memory: { ...settings.memory, enabled } })} />
          {settings.memory.enabled && settings.memory.provider === "local" && local.error && <div className="callout warning"><CircleAlert size={15} /><span>Memories are saved with the local model server, which isn't running. Start it, or run memory with OpenAI below.</span></div>}
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
        </div>
      </>}

      {tab === "about" && <>
        <PageHeader title="General" description="How Rex looks and when it gets your attention." />
        <div className="card">
          <div className="provider-head"><TileMark seed="appearance" icon={<Palette size={18} />} size={36} /><div className="grow"><strong>Appearance</strong><small className="muted">Auto follows your Mac's light or dark setting.</small></div></div>
          <div className="segmented">
            {(["system", "light", "dark"] as const).map(option => <button type="button" key={option} className={theme === option ? "active" : ""} onClick={() => { setTheme(option); saveTheme(option); }}>{option === "system" ? "Auto" : option === "light" ? "Light" : "Dark"}</button>)}
          </div>
        </div>
        <div className="card">
          <div className="provider-head"><TileMark seed="notify" icon={<Bell size={18} />} size={36} /><div className="grow"><strong>Notifications</strong><small className="muted">The Dock icon shows how many conversations are waiting.</small></div></div>
          <Toggle label="Notify me when a bot needs me" description="A Mac notification when a bot asks for approval or a question, or a routine finishes, while Rex is in the background."
            checked={!settings.notifications_off} onChange={async on => {
              const next = { ...settings, notifications_off: !on };
              setSettings(next);
              try { const saved = await api.saveSettings(next); setSettings(saved); setSavedSettings(saved); onSaved(saved); } catch (caught) { setError(errorMessage(caught)); }
            }} />
        </div>
        <div className="card">
          <h3 className="section-title">About this Mac's Rex</h3>
          <dl className="facts-list">
            <dt>Bot engine</dt><dd><span className={`chip ${status?.runtime_ready ? "ok" : "warn"}`}>{status?.runtime_ready ? "Running" : "Starting"}</span></dd>
            <dt>Data folder</dt><dd><code>{status?.data_dir}</code></dd>
            <dt>Bot workspaces</dt><dd><code>{status?.workspace_root}</code></dd>
            <dt>Credentials</dt><dd>macOS Keychain</dd>
          </dl>
          <button type="button" className="button secondary small" onClick={() => api.revealLogs()}><FolderOpen size={13} />Show logs</button>
        </div>
        <p className="muted small">Rex runs bots on this Mac. A bot's workspace folder limits Rex's file tools but is not an operating-system sandbox. Routines need the Mac awake and Rex running.</p>
      </>}

      {dirty && <div className="save-bar" role="status">
        <span>You have unsaved changes</span><span className="spacer" />
        <button type="button" className="button secondary small" onClick={discard} disabled={saving}>Discard</button>
        <button type="button" className="button primary small" onClick={save} disabled={saving}>{saving ? "Saving…" : "Save changes"}</button>
      </div>}
    </section>
  </div>;
}
