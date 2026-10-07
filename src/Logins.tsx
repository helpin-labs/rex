import { useCallback, useEffect, useState } from "react";
import { KeyRound, Trash2 } from "lucide-react";
import { api } from "./api";
import type { Bot, BotLogin } from "./types";
import { Field, errorMessage, relativeTime } from "./ui";

// Logins lets the user save sites a bot may sign in to. The bot never sees
// the password: Rex types it into the bot's browser, only on that site.
export function Logins({ bot }: { bot: Bot }) {
  const [logins, setLogins] = useState<BotLogin[]>([]);
  const [draft, setDraft] = useState<{ id?: string; site: string; username: string; password: string; two_step_key: string } | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(() => api.logins(bot.id).then(setLogins).catch(caught => setError(errorMessage(caught))), [bot.id]);
  useEffect(() => { void load(); }, [load]);

  const save = async () => {
    if (!draft) return;
    setError("");
    try {
      await api.saveLogin(bot.id, draft.id, { site: draft.site, username: draft.username, password: draft.password, two_step_key: draft.two_step_key });
      setDraft(null);
      await load();
    } catch (caught) { setError(errorMessage(caught)); }
  };
  const remove = async (login: BotLogin) => {
    if (!window.confirm(`Remove the ${login.site} login?`)) return;
    try { await api.deleteLogin(bot.id, login.id); await load(); } catch (caught) { setError(errorMessage(caught)); }
  };

  return <div className="logins">
    <h3 className="section-title">Logins</h3>
    <p className="muted small">Sites {bot.name} can sign in to. Rex types the password into the bot's browser, only on that site; the bot never sees it. Passwords stay in your macOS Keychain.</p>
    {!bot.capabilities.browser && <div className="callout">Turn on “Use a browser” to let this bot sign in with these.</div>}
    {error && <div className="inline-error">{error}</div>}
    {logins.map(login => <div className="login-row" key={login.id}>
      <KeyRound size={14} />
      <span className="grow"><strong>{login.site}</strong><small className="muted">{login.username}{login.has_2fa ? " · 2FA codes" : ""}{login.last_used_at ? ` · used ${relativeTime(login.last_used_at)}` : ""}</small></span>
      <button type="button" className="link-button" onClick={() => setDraft({ id: login.id, site: login.site, username: login.username, password: "", two_step_key: "" })}>Update</button>
      <button type="button" className="icon-button ghost tiny" title="Remove" onClick={() => remove(login)}><Trash2 size={12} /></button>
    </div>)}
    {draft ? <div className="login-form">
      <div className="two-col">
        <Field label="Site"><input value={draft.site} onChange={event => setDraft({ ...draft, site: event.target.value })} placeholder="momondo.se" autoFocus={!draft.id} /></Field>
        <Field label="Username or email"><input value={draft.username} onChange={event => setDraft({ ...draft, username: event.target.value })} autoComplete="off" /></Field>
      </div>
      <div className="two-col">
        <Field label="Password" hint={draft.id ? "Leave empty to keep the saved password" : undefined}><input type="password" value={draft.password} onChange={event => setDraft({ ...draft, password: event.target.value })} autoComplete="new-password" /></Field>
        <Field label="2FA setup key (optional)" hint="The key or otpauth:// link shown when you set up an authenticator app"><input type="password" value={draft.two_step_key} onChange={event => setDraft({ ...draft, two_step_key: event.target.value })} autoComplete="off" /></Field>
      </div>
      <div className="row"><span className="spacer" /><button type="button" className="button secondary small" onClick={() => setDraft(null)}>Cancel</button><button type="button" className="button primary small" onClick={save}>Save login</button></div>
    </div> : <button type="button" className="button secondary small" onClick={() => setDraft({ site: "", username: "", password: "", two_step_key: "" })}>Add a login</button>}
  </div>;
}
