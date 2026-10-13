import { useCallback, useEffect, useMemo, useState } from "react";
import { Blocks, Check, CircleAlert, ExternalLink, KeyRound, LoaderCircle, Plus, RefreshCw, Trash2 } from "lucide-react";
import { api } from "./api";
import { type AppCategory, type AppEntry, appCategories, appFor, connectionFor, connectionState, searchApps, appCatalog } from "./apps";
import { Select } from "./Select";
import { SideList } from "./SideList";
import type { Bot, GoogleStatus, MCPConnection } from "./types";
import { Avatar, Field, Modal, PageHeader, SearchBox, errorMessage, openExternal, signInToConnection } from "./ui";

// Apps are MCP connections. The gallery connects a known app in one step
// (sign in on its site, or no account at all); anything else is added as a
// custom app with its server URL. Each app is then turned on per bot.

type Filter = "all" | "connected" | AppCategory;

// AppMark is an app's logo: its simple-icons glyph, or its first letter on
// the brand color.
export function AppMark({ app, size = 36 }: { app?: Pick<AppEntry, "name" | "icon" | "color">; size?: number }) {
  const style = { width: size, height: size, borderRadius: Math.round(size * 0.26) };
  if (!app) return <span className="app-mark custom" style={style}><Blocks size={Math.round(size * 0.5)} /></span>;
  if (app.icon) return <span className="app-mark" style={style}>
    <svg viewBox="0 0 24 24" width={Math.round(size * 0.56)} height={Math.round(size * 0.56)} aria-hidden fill={`#${app.icon.hex}`} className={isDark(app.icon.hex) ? "dark-glyph" : ""}><path d={app.icon.path} /></svg>
  </span>;
  return <span className="app-mark letter" style={{ ...style, background: app.color, fontSize: Math.round(size * 0.44) }}>{app.name[0].toUpperCase()}</span>;
}

// Near-black glyphs (Notion, Vercel, GitHub) turn light in dark mode.
function isDark(hex: string): boolean {
  const value = parseInt(hex, 16);
  return ((value >> 16) & 255) * 0.299 + ((value >> 8) & 255) * 0.587 + (value & 255) * 0.114 < 60;
}

// StatusLine shows why an app isn't working, in the server's own words.
function StatusLine({ connection }: { connection: MCPConnection }) {
  const state = connectionState(connection);
  const text = state === "signin" ? "Sign in again to keep using its tools." : connection.status || "Rex couldn't reach this app.";
  return <p className={`tile-error ${state}`} title={text}><CircleAlert size={12} /><span>{text}</span></p>;
}

function usedBy(bots: Bot[], connectionId: string): Bot[] {
  return bots.filter(bot => bot.mcp_connections.includes(connectionId));
}

export function AppsView({ bots, onBotsChanged, focusApp }: { bots: Bot[]; onBotsChanged: () => void; focusApp?: string }) {
  const [connections, setConnections] = useState<MCPConnection[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState(() => appCatalog.find(app => app.id === focusApp)?.name || "");
  const [editing, setEditing] = useState<{ connection: Partial<MCPConnection>; app?: AppEntry; justConnected?: boolean } | null>(null);
  // The app being connected, and the connection waiting for its sign-in.
  const [busy, setBusy] = useState<string | null>(null);
  const [waiting, setWaiting] = useState<{ appId: string; connectionId: string } | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(() => api.connections().then(list => { setConnections(list); setLoaded(true); return list; }), []);
  // Sign-ins expire and servers go down while the page is open: look again
  // when the filter changes or Rex comes back to the front.
  useEffect(() => { load().catch(caught => setError(errorMessage(caught))); }, [load, filter]);
  useEffect(() => {
    const look = () => void load().catch(() => undefined);
    window.addEventListener("focus", look);
    return () => window.removeEventListener("focus", look);
  }, [load]);

  // Sign-in finishes in the browser; check until the connection is signed
  // in, then offer to choose its bots.
  useEffect(() => {
    if (!waiting) return;
    const look = async () => {
      const list = await load().catch(() => null);
      const current = list?.find(item => item.id === waiting.connectionId);
      if (current?.signed_in) {
        setWaiting(null);
        setEditing({ connection: current, app: appFor(current), justConnected: true });
      }
    };
    const timer = window.setInterval(look, 1500);
    const stop = window.setTimeout(() => setWaiting(null), 10 * 60 * 1000);
    window.addEventListener("focus", look);
    return () => { window.clearInterval(timer); window.clearTimeout(stop); window.removeEventListener("focus", look); };
  }, [waiting, load]);

  const connect = async (app: AppEntry) => {
    const existing = connectionFor(app, connections);
    // Token apps need the token first.
    if (app.auth === "bearer") return setEditing({ connection: existing ?? { name: app.name, url: app.url, auth: "bearer", tools: [] }, app });
    setBusy(app.id); setError("");
    try {
      // Google apps need a Google client before the first sign-in; the
      // editor walks through adding one.
      if (app.auth === "google" && !(await api.google()).configured) return setEditing({ connection: existing ?? { name: app.name, url: app.url, auth: "google", tools: [] }, app });
      const saved = existing ?? await api.saveConnection({ name: app.name, url: app.url, auth: app.auth, tools: [] });
      if (app.auth === "oauth" || app.auth === "google") {
        await signInToConnection(saved.id);
        setWaiting({ appId: app.id, connectionId: saved.id });
      } else {
        await load();
        setEditing({ connection: saved, app, justConnected: connectionState(saved) === "ready" });
      }
    } catch (caught) { setError(`${app.name}: ${errorMessage(caught)}`); } finally { setBusy(null); }
  };

  const connected = connections.filter(connection => connectionState(connection) === "ready").length;
  const apps = useMemo(() => searchApps(query, filter !== "all" && filter !== "connected" ? filter : undefined), [query, filter]);
  // Connections not from the gallery are custom apps.
  const custom = connections.filter(connection => !appFor(connection));

  const card = (app: AppEntry) => {
    const connection = connectionFor(app, connections);
    const state = connection && connectionState(connection);
    const open = () => connection ? setEditing({ connection, app }) : void connect(app);
    const users = connection ? usedBy(bots, connection.id) : [];
    return <article key={app.id} className={`gallery-card ${state || ""}`}>
      <button type="button" className="gallery-card-main" onClick={open} aria-label={connection ? `Manage ${app.name}` : `Connect ${app.name}`}>
        <AppMark app={app} size={40} />
        <span className="gallery-card-text"><strong>{app.name}</strong><small>{app.description}</small></span>
      </button>
      {connection && state !== "ready" && <StatusLine connection={connection} />}
      <footer className="gallery-card-footer">
        {waiting?.appId === app.id ? <><span className="muted small row"><LoaderCircle className="spin" size={12} />Finish signing in in your browser</span><span className="spacer" /><button type="button" className="link-button" onClick={() => setWaiting(null)}>Cancel</button></>
          : state === "ready" ? <><span className="tile-status ok"><Check size={12} />Connected</span>
            {users.length > 0 ? <span className="tile-users" title={`Used by ${users.map(bot => bot.name).join(", ")}`}>{users.slice(0, 4).map(bot => <Avatar key={bot.id} bot={bot} size={18} />)}</span> : <span className="muted small">No bots yet</span>}
            <span className="spacer" /><button type="button" className="button secondary small" onClick={open}>Manage</button></>
          : state === "signin" ? <><span className="tile-status warn"><CircleAlert size={12} />Signed out</span><span className="spacer" /><button type="button" className="button secondary small" disabled={busy === app.id} onClick={() => void connect(app)}>Sign in</button></>
          : state === "broken" ? <><span className="tile-status bad"><CircleAlert size={12} />Needs attention</span><span className="spacer" /><button type="button" className="button secondary small" onClick={open}>Fix</button></>
          : <>{(app.auth === "bearer" || app.auth === "none") && <span className="muted small">{app.auth === "bearer" ? "Uses an access token" : "No account needed"}</span>}<span className="spacer" />
            <button type="button" className="button primary small" disabled={busy === app.id} onClick={() => void connect(app)}>{busy === app.id ? <LoaderCircle className="spin" size={13} /> : <Plus size={13} />}Connect</button></>}
      </footer>
    </article>;
  };

  const customCard = (connection: MCPConnection) => {
    const state = connectionState(connection);
    return <article key={connection.id} className={`gallery-card ${state}`}>
      <button type="button" className="gallery-card-main" onClick={() => setEditing({ connection })}>
        <AppMark size={40} />
        <span className="gallery-card-text"><strong>{connection.name}</strong><small className="mono-url">{connection.url}</small></span>
      </button>
      {state !== "ready" && <StatusLine connection={connection} />}
      <footer className="gallery-card-footer">
        {state === "ready" ? <span className="tile-status ok"><Check size={12} />Connected</span> : <span className={`tile-status ${state === "signin" ? "warn" : "bad"}`}><CircleAlert size={12} />{state === "signin" ? "Signed out" : "Needs attention"}</span>}
        <span className="spacer" /><button type="button" className="button secondary small" onClick={() => setEditing({ connection })}>Manage</button>
      </footer>
    </article>;
  };

  const addCustom = <button type="button" className="gallery-card add-custom" onClick={() => setEditing({ connection: { auth: "none", tools: [] } })}>
    <span className="app-mark custom"><Plus size={18} /></span>
    <span className="gallery-card-text"><strong>Custom app</strong><small>Any MCP server with a Streamable HTTP URL</small></span>
  </button>;

  const title = filter === "all" ? "Connect apps" : filter === "connected" ? "Connected" : filter;
  const connectedApps = connections.map(connection => ({ connection, app: appFor(connection) }));

  return <div className="page">
    <SideList>
      <header className="page-list-header"><h2>Apps</h2><small className="subtitle">Tools your bots can use</small></header>
      <nav className="list">
        {(["all", "connected", ...appCategories] as Filter[]).map(item => <button type="button" key={item} className={`list-item compact ${filter === item ? "selected" : ""}`} onClick={() => setFilter(item)}>
          <span className="list-text"><strong>{item === "all" ? "All apps" : item === "connected" ? "Connected" : item}</strong></span>
          {item === "connected" && connected > 0 && <span className="count">{connected}</span>}
        </button>)}
      </nav>
    </SideList>
    <section className="page-content gallery-page">
      <PageHeader title={title} description="Give your bots tools from the apps you use. Rex signs in on the app's site and keeps the sign-in in your macOS Keychain. Then choose which bots can use each app."
        actions={filter !== "connected" && <SearchBox value={query} onChange={setQuery} placeholder="Search apps" />} />
      {error && <div className="inline-error">{error}</div>}
      {!loaded ? <LoaderCircle className="spin" />
        : filter === "connected" ? (connectedApps.length === 0
          ? <div className="gallery-empty"><Blocks size={22} /><strong>No apps connected yet</strong><span className="muted small">Connect one from the gallery to give your bots its tools.</span><button type="button" className="button secondary small" onClick={() => setFilter("all")}>Browse apps</button></div>
          : <div className="gallery-grid">{connectedApps.map(({ connection, app }) => app ? card(app) : customCard(connection))}</div>)
        : <>
          {apps.length === 0 && <p className="muted">No apps match “{query}”. You can still add it as a custom app with its MCP server URL.</p>}
          {filter === "all" && !query ? appCategories.map(category => <section key={category} className="gallery-section">
            <h3 className="gallery-section-title">{category}</h3>
            <div className="gallery-grid">{apps.filter(app => app.category === category).map(card)}</div>
          </section>) : <div className="gallery-grid">{apps.map(card)}</div>}
          <section className="gallery-section">
            <h3 className="gallery-section-title">Custom</h3>
            <div className="gallery-grid">{custom.map(customCard)}{addCustom}</div>
          </section>
        </>}
    </section>
    {editing && <ConnectionEditor initial={editing.connection} app={editing.app} justConnected={editing.justConnected} bots={bots}
      onClose={() => { setEditing(null); void load(); }}
      onSaved={() => { setEditing(null); void load(); onBotsChanged(); }} />}
  </div>;
}

// ConnectionEditor manages one app: its sign-in or token, its tools, and
// the bots that use it.
export function ConnectionEditor({ initial, app, justConnected, bots, onClose, onSaved }: { initial: Partial<MCPConnection>; app?: AppEntry; justConnected?: boolean; bots: Bot[]; onClose: () => void; onSaved: () => void }) {
  const [connection, setConnection] = useState<Partial<MCPConnection>>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [removing, setRemoving] = useState(false);
  const isGoogle = app?.auth === "google" || connection.auth === "google";
  const [google, setGoogle] = useState<GoogleStatus | null>(null);
  const [changingClient, setChangingClient] = useState(false);
  useEffect(() => { if (isGoogle) api.google().then(setGoogle).catch(caught => setError(errorMessage(caught))); }, [isGoogle]);
  // Bots still in their setup interview are left alone: saving a bot ends it.
  const choosable = bots.filter(bot => !bot.setup);
  const [useWith, setUseWith] = useState<Set<string>>(() => new Set(initial.id ? usedBy(bots, initial.id).map(bot => bot.id) : []));
  const toggleBot = (id: string) => setUseWith(current => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next; });

  const applyBots = async (connectionId: string) => {
    for (const bot of choosable) {
      const has = bot.mcp_connections.includes(connectionId);
      if (has === useWith.has(bot.id)) continue;
      await api.saveBot({ ...bot, mcp_connections: has ? bot.mcp_connections.filter(id => id !== connectionId) : [...bot.mcp_connections, connectionId] });
    }
  };
  const save = async (close: boolean) => {
    setBusy(true); setError("");
    try {
      const saved = await api.saveConnection(connection);
      setConnection(saved);
      if (close) { await applyBots(saved.id); onSaved(); }
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
  // A new Google client signs Google apps out: show what the host says.
  const refreshConnection = async () => {
    if (!connection.id) return;
    const current = (await api.connections().catch(() => [])).find(item => item.id === connection.id);
    if (current) setConnection(current);
  };
  const signOut = async () => {
    try { await api.signOut(connection.id!); setConnection({ ...connection, signed_in: false }); } catch (caught) { setError(errorMessage(caught)); }
  };
  const remove = async () => {
    setBusy(true); setError("");
    try { await api.deleteConnection(connection.id!); onSaved(); } catch (caught) { setError(errorMessage(caught)); setBusy(false); }
  };
  const tool = (name: string, patch: Partial<MCPConnection["tools"][number]>) => setConnection(current => ({ ...current, tools: (current.tools || []).map(item => item.name === name ? { ...item, ...patch } : item) }));
  const tools = connection.tools || [];
  const enabled = tools.filter(item => item.enabled).length;

  return <Modal title={connection.id ? connection.name || "App" : app ? `Connect ${app.name}` : "Add a custom app"} subtitle={app ? app.category : "MCP server"} onClose={onClose} wide
    footer={<>
      {connection.id && (removing
        ? <><span className="small">Remove {connection.name} and its sign-in?</span><button type="button" className="button danger-ghost small" disabled={busy} onClick={remove}>Remove</button><button type="button" className="link-button" onClick={() => setRemoving(false)}>Keep</button></>
        : <button type="button" className="button danger-ghost small" onClick={() => setRemoving(true)}><Trash2 size={13} />Remove</button>)}
      <span className="spacer" />
      <button type="button" className="button secondary" onClick={onClose}>Close</button>
      <button type="button" className="button primary" disabled={busy || !connection.name || !connection.url} onClick={() => save(true)}>{busy ? "Saving…" : "Save"}</button>
    </>}>
    {justConnected && <div className="inline-ok"><Check size={14} />Connected to {connection.name}. Choose which bots can use it.</div>}
    {error && <div className="inline-error">{error}</div>}
    {waiting && <div className="callout">Finish signing in in your browser. This updates by itself when you're done.</div>}
    {app && <div className="app-editor-head"><AppMark app={app} size={44} /><div className="grow"><strong>{app.name}</strong><small className="muted">{app.description}</small></div></div>}
    {!app && <div className="two-col">
      <Field label="Name"><input value={connection.name || ""} onChange={event => setConnection({ ...connection, name: event.target.value })} placeholder="notes" /></Field>
      <Field label="Server URL" hint="Streamable HTTP endpoint"><input value={connection.url || ""} onChange={event => setConnection({ ...connection, url: event.target.value })} placeholder="https://mcp.example.com/mcp" /></Field>
    </div>}
    <div className="two-col">
      {!app && <Field label="Authentication"><Select value={connection.auth || "none"} onChange={value => setConnection({ ...connection, auth: value as MCPConnection["auth"] })}><option value="none">None</option><option value="oauth">Sign in (OAuth)</option><option value="bearer">Bearer token</option></Select></Field>}
      {isGoogle && <Field label="Google account" hint={app?.access ? `Rex asks Google to let it: ${app.access}` : undefined}>
        <div className="row">
          {connection.signed_in ? <span className="badge ok-badge" title="Signed in">{connection.account || "Signed in"}</span> : <span className="muted small">Not signed in</span>}
          <span className="spacer" />
          {connection.signed_in && <button type="button" className="link-button" onClick={signOut}>Sign out</button>}
          <button type="button" className="button secondary small" disabled={busy || !google?.configured} onClick={signIn}>{connection.signed_in ? "Sign in again" : "Sign in with Google"}</button>
        </div>
      </Field>}
      {connection.auth === "oauth" && <Field label="Account" hint="You sign in on the app's page in your browser. Rex keeps the sign-in in your macOS Keychain and renews it automatically.">
        <div className="row">
          {connection.signed_in ? <span className="badge ok-badge">Signed in</span> : <span className="muted small">Not signed in</span>}
          <span className="spacer" />
          {connection.signed_in && <button type="button" className="link-button" onClick={signOut}>Sign out</button>}
          <button type="button" className="button secondary small" disabled={busy || !connection.url || !connection.name} onClick={signIn}>{connection.signed_in ? "Sign in again" : "Sign in"}</button>
        </div>
      </Field>}
      {connection.auth === "bearer" && <Field label="Access token" hint={connection.has_token ? "A token is stored in your Keychain. Enter a new one to replace it." : app?.tokenHint || "Stored in your macOS Keychain"}>
        <input type="password" value={connection.token || ""} onChange={event => setConnection({ ...connection, token: event.target.value })} placeholder={connection.has_token ? "••••••••" : "token"} />
      </Field>}
    </div>
    {isGoogle && google && (!google.configured || changingClient
      ? <GoogleClientSetup status={google} onSaved={status => { setGoogle(status); setChangingClient(false); void refreshConnection(); }} onCancel={google.configured ? () => setChangingClient(false) : undefined} />
      : google.source === "settings" && <p className="muted small row"><KeyRound size={12} /><span>Signs in with your Google client <span className="mono-url">{google.client_id}</span></span><button type="button" className="link-button" onClick={() => setChangingClient(true)}>Change</button></p>)}
    {!(isGoogle && !connection.signed_in) && <div className="row"><button type="button" className="button secondary small" disabled={busy || !connection.url || !connection.name} onClick={() => save(false)}><RefreshCw size={13} />{connection.id ? "Refresh tools" : "Connect and list tools"}</button>{connection.status && connection.id && connectionState(connection as MCPConnection) === "ready" && <span className="muted small">{connection.status}</span>}</div>}
    {connection.status && connection.id && connectionState(connection as MCPConnection) === "broken" && <div className="inline-error"><CircleAlert size={14} /><span>{connection.status}</span></div>}
    {choosable.length > 0 && <>
      <h3 className="section-title">Use with</h3>
      <div className="bot-picker">{choosable.map(bot => <button type="button" key={bot.id} className={`bot-chip ${useWith.has(bot.id) ? "on" : ""}`} aria-pressed={useWith.has(bot.id)} onClick={() => toggleBot(bot.id)}>
        <Avatar bot={bot} size={20} /><span>{bot.name}</span>{useWith.has(bot.id) && <Check size={13} />}
      </button>)}</div>
    </>}
    {tools.length > 0 && <><h3 className="section-title">Tools <span className="muted small">{enabled} of {tools.length} on</span></h3><p className="muted small">Write tools ask for approval on bots set to “Ask for risky actions”.</p>
      <ul className="tool-table">{tools.map(item => <li key={item.name}>
        <label className="check"><input type="checkbox" checked={item.enabled} onChange={event => tool(item.name, { enabled: event.target.checked })} /><span><strong>{item.name}</strong><small>{item.description}</small></span></label>
        <Select value={item.access} onChange={value => tool(item.name, { access: value as "read" | "write" })}><option value="read">Read</option><option value="write">Write</option></Select>
      </li>)}</ul></>}
    {app && !isGoogle && <p className="muted small row"><ExternalLink size={12} /><span className="mono-url">{app.url}</span></p>}
  </Modal>;
}

const googleConsole = "https://console.cloud.google.com";
const googleSteps: { text: string; link?: { label: string; url: string } }[] = [
  { text: "Create a Google Cloud project (or pick one you have).", link: { label: "New project", url: `${googleConsole}/projectcreate` } },
  { text: "Turn on the Gmail, Google Sheets, Google Calendar and Google Drive APIs for it.", link: { label: "Enable APIs", url: `${googleConsole}/flows/enableapi?apiid=gmail.googleapis.com,sheets.googleapis.com,calendar-json.googleapis.com,drive.googleapis.com` } },
  { text: "Set up the consent screen: choose External, and add your Google address under Audience → Test users.", link: { label: "Consent screen", url: `${googleConsole}/auth/overview` } },
  { text: "Create an OAuth client of type Desktop app, then paste its ID and secret here.", link: { label: "Create client", url: `${googleConsole}/auth/clients/create` } },
];

// GoogleClientSetup adds the Google client Rex signs in with. Until Rex
// ships with its own, each person creates one in their Google Cloud
// project; Google shows it as unverified, which is expected for a client
// only you use.
function GoogleClientSetup({ status, onSaved, onCancel }: { status: GoogleStatus; onSaved: (status: GoogleStatus) => void; onCancel?: () => void }) {
  const [clientId, setClientId] = useState("");
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const save = async (client: { client_id: string; client_secret?: string }) => {
    setBusy(true); setError("");
    try { onSaved(await api.saveGoogleClient(client)); } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); }
  };
  return <section className="google-setup">
    <h3 className="section-title">Connect Rex to Google</h3>
    <p className="muted small">Rex signs in to Google with an OAuth client from your own Google Cloud project. It takes about five minutes, once, and covers every Google app.</p>
    <ol className="google-steps">{googleSteps.map((step, index) => <li key={index}>
      <span>{step.text}</span>
      {step.link && <button type="button" className="link-button" onClick={() => void openExternal(step.link!.url)}>{step.link.label}<ExternalLink size={11} /></button>}
    </li>)}</ol>
    {error && <div className="inline-error">{error}</div>}
    <div className="two-col">
      <Field label="Client ID"><input value={clientId} onChange={event => setClientId(event.target.value)} placeholder="1234-abc.apps.googleusercontent.com" spellCheck={false} /></Field>
      <Field label="Client secret" hint="Stored in your macOS Keychain"><input type="password" value={secret} onChange={event => setSecret(event.target.value)} placeholder="GOCSPX-…" /></Field>
    </div>
    <p className="muted small">When Google says the app isn't verified, choose Continue: it's your own client. {status.configured && "Changing the client signs Google apps out."}</p>
    <div className="row">
      {status.source === "settings" && <button type="button" className="link-button" disabled={busy} onClick={() => void save({ client_id: "" })}>Remove my client</button>}
      <span className="spacer" />
      {onCancel && <button type="button" className="button secondary small" onClick={onCancel}>Cancel</button>}
      <button type="button" className="button primary small" disabled={busy || !clientId.trim().endsWith(".apps.googleusercontent.com")} onClick={() => void save({ client_id: clientId, client_secret: secret })}>{busy ? <LoaderCircle className="spin" size={13} /> : <Check size={13} />}Save client</button>
    </div>
  </section>;
}
