import { useEffect, useMemo, useState } from "react";
import { Download, ExternalLink, FileCode2, FileText, RefreshCw, Search, Star, Trash2 } from "lucide-react";
import { api } from "./api";
import { AppLogo } from "./AppLogo";
import { BotFace } from "./BotFace";
import { parseSkillDoc } from "./skillDoc";
import type { Bot, CatalogSkill, Skill, SkillSource } from "./types";
import { Markdown, Toggle, errorMessage, openExternal, relativeTime } from "./ui";

// Browsing the skill catalog: curated GitHub collections Rex indexes. The
// list shows what matches a search; the detail shows a skill's SKILL.md,
// source, licence and files, and installs it at the indexed commit.

// Catalog descriptions mostly start with the same words; the list shows
// what each skill is for.
function gist(description: string): string {
  const rest = description.replace(/^(use (this skill )?)?when (the )?user (wants|asks|needs)( help)?( to| with| for)?\s+/i, "");
  return rest.charAt(0).toUpperCase() + rest.slice(1);
}

export function CatalogList({ results, query, onQuery, selected, onSelect, loading, sourcesOn, onSources }: {
  results: CatalogSkill[]; query: string; onQuery: (value: string) => void; selected: string | null; onSelect: (item: CatalogSkill) => void;
  loading: boolean; sourcesOn: number; onSources: () => void;
}) {
  return <>
    <div className="catalog-search">
      <Search size={14} />
      <input value={query} onChange={event => onQuery(event.target.value)} placeholder="Search, e.g. pdf, seo, cold email" aria-label="Search skills" />
    </div>
    <button type="button" className={`catalog-sources-link ${selected === null ? "active" : ""}`} onClick={onSources}>{sourcesOn} source{sourcesOn === 1 ? "" : "s"} on · manage</button>
    <nav className="list skill-list">
      {results.map(item => <button type="button" key={item.id} className={`list-item ${selected === item.id ? "selected" : ""}`} onClick={() => onSelect(item)}>
        <span className="list-icon">{item.installed ? <Download size={15} /> : <FileText size={15} />}</span>
        <span className="list-text" title={`${item.description}\n\n${item.repo}`}><strong>{item.name}</strong><small>{item.installed ? "Installed · " : ""}{item.description ? gist(item.description) : item.repo}</small></span>
      </button>)}
      {!loading && results.length === 0 && <p className="muted small skill-list-empty">{sourcesOn === 0 ? "Turn on a source to browse its skills." : query ? "No skills match." : "Sources are being read; this takes a moment the first time."}</p>}
    </nav>
  </>;
}

export function CatalogDetail({ item, bots, onInstalled }: { item: CatalogSkill; bots: Bot[]; onInstalled: (skill: Skill) => void }) {
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [forBot, setForBot] = useState("");
  useEffect(() => {
    setContent(null); setError(""); setForBot("");
    api.catalogPreview(item.id).then(result => setContent(result.content)).catch(caught => setError(errorMessage(caught)));
  }, [item.id]);
  const doc = useMemo(() => content ? parseSkillDoc(content) : null, [content]);
  const install = async () => {
    setBusy(true); setError("");
    try { onInstalled(await api.installCatalogSkill(item.id, forBot)); } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); }
  };
  const selectable = bots.filter(bot => !bot.setup);
  return <div className="skill-editor">
    <header className="skill-header">
      <div className="skill-header-text">
        <h1 className="catalog-title">{doc?.title || item.name}</h1>
        <small className="muted"><button type="button" className="link-button" onClick={() => openExternal(item.url)}>{item.repo}/{item.dir} <ExternalLink size={11} /></button> · {item.commit.slice(0, 7)}</small>
      </div>
      <div className="row skill-actions">
        {item.installed ? <span className="saved-note">Installed as “{item.installed}”</span>
          : <button type="button" className="button primary" disabled={busy} onClick={install}><Download size={14} />{busy ? "Installing…" : "Install"}</button>}
      </div>
    </header>
    {error && <div className="inline-error">{error}</div>}
    <p className="catalog-description">{item.description}</p>
    <div className="catalog-facts">
      <span>{item.license ? `Licence: ${item.license}` : "No licence stated"}</span>
      <span>{item.files.length} file{item.files.length === 1 ? "" : "s"}</span>
      {item.has_scripts && <span className="catalog-warning"><FileCode2 size={13} /> Contains scripts: they run only with the bot's command permission and approvals</span>}
    </div>
    {!item.installed && <div className="skill-section">
      <span className="field-label">Turn it on for</span>
      <div className="skill-bots">
        <label className={`skill-bot ${forBot === "" ? "on" : ""}`}><input type="radio" name="install-for" checked={forBot === ""} onChange={() => setForBot("")} /><span>No bot yet</span></label>
        {selectable.map(bot => <label key={bot.id} className={`skill-bot ${forBot === bot.id ? "on" : ""}`}>
          <input type="radio" name="install-for" checked={forBot === bot.id} onChange={() => setForBot(bot.id)} />
          {bot.role === "main" ? <AppLogo size={22} /> : <BotFace icon={bot.emoji} color={bot.color} seed={bot.id} size={22} />}
          <span>{bot.name}</span>
        </label>)}
      </div>
      <span className="field-hint">Installing copies the skill into your skills folder at this exact version. You can turn it on for more bots later.</span>
    </div>}
    <div className="skill-section">
      <span className="field-label">Instructions</span>
      <div className="skill-preview">{content === null && !error ? <p className="muted small">Loading…</p> : <Markdown>{doc?.instructions || ""}</Markdown>}</div>
    </div>
    <div className="skill-section">
      <span className="field-label">Files</span>
      <ul className="skill-file-list">{item.files.map(file => <li key={file}>{/\.(py|sh|js|ts|rb|mjs|cjs)$/i.test(file) ? <FileCode2 size={13} /> : <FileText size={13} />} {file}</li>)}</ul>
    </div>
  </div>;
}

export function SourcesPanel({ sources, onChanged }: { sources: SkillSource[]; onChanged: () => void }) {
  const [repo, setRepo] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const act = async (id: string, work: () => Promise<unknown>) => {
    setBusy(id); setError("");
    try { await work(); onChanged(); } catch (caught) { setError(errorMessage(caught)); onChanged(); } finally { setBusy(""); }
  };
  return <div className="skill-editor">
    <header className="skill-header"><div className="skill-header-text"><h1 className="catalog-title">Skill sources</h1>
      <small className="muted plain">GitHub collections Rex searches for skills. Rex reads their skill names and descriptions; nothing is installed until you choose to.</small></div></header>
    {error && <div className="inline-error">{error}</div>}
    <div className="source-list">
      {sources.map(source => <div key={source.id} className={`source-row ${source.enabled ? "" : "off"}`}>
        <div className="source-main">
          <div className="source-name">
            <button type="button" className="link-button" onClick={() => openExternal(`https://github.com/${source.repo}`)}>{source.repo}</button>
            {source.stars > 0 && <span className="source-stars"><Star size={11} /> {source.stars >= 1000 ? `${Math.round(source.stars / 100) / 10}k` : source.stars}</span>}
          </div>
          <small>{source.note || source.description}</small>
          <small className="muted">{source.error ? <span className="source-error">{source.error}</span>
            : source.indexed_at ? `${source.skill_count} skills · ${source.license || "no licence stated"} · read ${relativeTime(source.indexed_at)}` : source.enabled ? "Not read yet" : "Off"}</small>
        </div>
        <div className="row source-actions">
          {source.enabled && <button type="button" className="icon-button" title="Read again" aria-label="Read again" disabled={busy === source.id} onClick={() => act(source.id, () => api.refreshSkillSource(source.id))}><RefreshCw size={14} className={busy === source.id ? "spin" : ""} /></button>}
          {!source.builtin && <button type="button" className="icon-button" title="Remove" aria-label="Remove" onClick={() => act(source.id, () => api.removeSkillSource(source.id))}><Trash2 size={14} /></button>}
          <Toggle label="" checked={source.enabled} disabled={busy === source.id} onChange={value => act(source.id, () => api.setSkillSource(source.id, value))} />
        </div>
      </div>)}
    </div>
    <form className="source-add" onSubmit={event => { event.preventDefault(); if (repo.trim()) void act("new", async () => { await api.addSkillSource(repo); setRepo(""); }); }}>
      <input value={repo} onChange={event => setRepo(event.target.value)} placeholder="Add a GitHub repository: owner/name or its link" aria-label="GitHub repository" />
      <button type="submit" className="button secondary" disabled={!repo.trim() || busy === "new"}>{busy === "new" ? "Reading…" : "Add"}</button>
    </form>
    <span className="field-hint">Skills from these sources are other people's instructions. Read a skill before installing it; skills with scripts are marked.</span>
  </div>;
}
