import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { BookOpen, Compass, FileText, FolderOpen, LoaderCircle, Plus, Trash2, Upload } from "lucide-react";
import { api } from "./api";
import { SideList } from "./SideList";
import { CatalogDetail, SourcesPanel, gist } from "./SkillCatalog";
import { composeSkillDoc, parseSkillDoc, skillSlug, titleFromName, type SkillDoc } from "./skillDoc";
import type { Bot, CatalogSkill, Skill, SkillSource } from "./types";
import { Avatar, Markdown, PageHeader, SearchBox, TileMark, errorMessage } from "./ui";
import { confirm } from "./confirm";

// Skills are folders in ~/Rex/skills with a SKILL.md and any files it uses
// (scripts, reference notes), in the Agent Skills format. The page edits a
// skill as a title, when to use it and its instructions, and turns it on for
// bots; SKILL.md is written from those fields.

const newSkill: SkillDoc = { name: "", description: "", title: "", instructions: "1. …", extra: [] };

function skillTitle(skill: Skill): string {
  return skill.title && skill.title !== skill.id ? skill.title : titleFromName(skill.id);
}

function usedBy(bots: Bot[], id: string): Bot[] {
  return bots.filter(bot => bot.skills.includes(id));
}

export function SkillsView({ bots: initialBots, workspaceRoot, onBotsChanged }: { bots: Bot[]; workspaceRoot: string; onBotsChanged?: () => void }) {
  const [skills, setSkills] = useState<Skill[]>([]);
  const [bots, setBots] = useState<Bot[]>(initialBots);
  const [selected, setSelected] = useState<string | null>(null); // skill id, "" for a new one
  const [doc, setDoc] = useState<SkillDoc | null>(null);
  const [saved, setSaved] = useState<string>("");
  const [preview, setPreview] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const fileInput = useRef<HTMLInputElement>(null);
  useEffect(() => setBots(initialBots), [initialBots]);

  // Browse: the skill catalog.
  const [mode, setMode] = useState<"installed" | "browse" | "sources">("installed");
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<CatalogSkill[]>([]);
  const [sources, setSources] = useState<SkillSource[]>([]);
  const [picked, setPicked] = useState<CatalogSkill | null>(null);
  const [searching, setSearching] = useState(false);
  const loadSources = useCallback(() => api.skillSources().then(setSources).catch(() => undefined), []);
  useEffect(() => {
    if (mode === "installed") return;
    void loadSources();
    setSearching(true);
    const timer = window.setTimeout(() => {
      api.catalog(query).then(list => { setResults(list); setPicked(current => current ? list.find(item => item.id === current.id) ?? current : null); })
        .catch(caught => setError(errorMessage(caught))).finally(() => setSearching(false));
    }, 200);
    return () => window.clearTimeout(timer);
  }, [mode, query, loadSources, sources.length]);
  const sourcesChanged = async () => {
    await loadSources();
    api.catalog(query).then(setResults).catch(() => undefined);
  };
  const installed = async (result: Skill) => {
    await load();
    onBotsChanged?.();
    setMode("installed");
    open(result);
    setNotice("Installed");
    window.setTimeout(() => setNotice(""), 2000);
  };

  const load = useCallback(async () => {
    try { setSkills(await api.skills()); } catch (caught) { setError(errorMessage(caught)); }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const skill = selected ? skills.find(item => item.id === selected) ?? null : null;
  const open = useCallback((item: Skill | null) => {
    const next = item ? parseSkillDoc(item.body) : { ...newSkill };
    if (item && !next.name) next.name = item.id;
    // Skills without a heading show their readable name as the title.
    if (item && !next.title) next.title = skillTitle(item);
    setSelected(item ? item.id : "");
    setDoc(next);
    setSaved(item ? composeSkillDoc(next) : "");
    // A saved skill opens to read; a new one opens to write.
    setPreview(!!item);
    setError(""); setNotice("");
  }, []);

  const composed = doc ? composeSkillDoc({ ...doc, name: doc.name || skillSlug(doc.title) }) : "";
  const dirty = doc !== null && composed !== saved;
  const isNew = selected === "";
  const canSave = !!doc && dirty && !!doc.description.trim() && !!doc.instructions.trim() && (!isNew || !!skillSlug(doc.title || doc.name));

  const save = async () => {
    if (!doc || !canSave) return;
    setError("");
    try {
      const result = await api.saveSkill({ id: isNew ? "" : selected!, title: doc.title, description: doc.description, body: composed });
      await load();
      open(result);
      setNotice("Saved");
      window.setTimeout(() => setNotice(""), 1500);
    } catch (caught) { setError(errorMessage(caught)); }
  };
  const remove = async () => {
    if (!skill || !(await confirm(`“${skillTitle(skill)}” and its folder are deleted. Bots that use it stop using it.`, { title: "Delete skill?", confirmLabel: "Delete", danger: true }))) return;
    try { await api.deleteSkill(skill.id); setSelected(null); setDoc(null); await load(); } catch (caught) { setError(errorMessage(caught)); }
  };
  const importZip = async (file: File | undefined) => {
    if (!file) return;
    setError("");
    try { const result = await api.importSkill(file); await load(); open(result); } catch (caught) { setError(errorMessage(caught)); }
  };
  const toggleBot = async (bot: Bot, on: boolean) => {
    if (!skill) return;
    const updated = { ...bot, skills: on ? [...bot.skills, skill.id] : bot.skills.filter(id => id !== skill.id) };
    setBots(current => current.map(item => item.id === bot.id ? updated : item));
    try { await api.saveBot(updated); onBotsChanged?.(); } catch (caught) {
      setBots(current => current.map(item => item.id === bot.id ? bot : item));
      setError(errorMessage(caught));
    }
  };
  const update = (patch: Partial<SkillDoc>) => setDoc(current => current ? { ...current, ...patch } : current);
  const selectable = useMemo(() => bots.filter(bot => !bot.setup), [bots]);

  // Leaving an edited skill asks first.
  const discardSkill = "Your changes to this skill are not saved.";
  const discardOptions = { title: "Discard changes?", confirmLabel: "Discard", danger: true };
  const close = async () => {
    if (dirty && !(await confirm(discardSkill, discardOptions))) return;
    setSelected(null); setDoc(null);
  };
  const go = async (next: "installed" | "browse" | "sources") => {
    if (doc && dirty && !(await confirm(discardSkill, discardOptions))) return;
    setMode(next); setSelected(null); setDoc(null); setPicked(null);
  };
  const [filterText, setFilterText] = useState("");
  const shownSkills = skills.filter(item => `${skillTitle(item)} ${item.description || ""}`.toLowerCase().includes(filterText.trim().toLowerCase()));
  const sourcesOn = sources.filter(source => source.enabled).length;

  const skillCard = (item: Skill) => {
    const users = usedBy(bots, item.id);
    return <article key={item.id} className="gallery-card">
      <button type="button" className="gallery-card-main" onClick={() => open(item)}>
        <TileMark seed={item.id} label={skillTitle(item)} />
        <span className="gallery-card-text"><strong>{skillTitle(item)}</strong><small className="clamp">{item.description ? gist(item.description) : "No description yet"}</small></span>
      </button>
      <footer className="gallery-card-footer">
        {users.length > 0 ? <span className="tile-users" title={`Used by ${users.map(bot => bot.name).join(", ")}`}>{users.slice(0, 4).map(bot => <Avatar key={bot.id} bot={bot} size={18} />)}</span> : <span className="muted small">Not used yet</span>}
        <span className="spacer" />
        <span className="chip" title={item.source ? `${item.source.repo} at ${item.source.commit.slice(0, 7)}` : "Written here"}>{item.source ? item.source.repo.split("/")[0] : "Yours"}</span>
      </footer>
    </article>;
  };

  const catalogCard = (item: CatalogSkill) => <article key={item.id} className="gallery-card">
    <button type="button" className="gallery-card-main" onClick={() => setPicked(item)}>
      <TileMark seed={item.id} label={item.name} />
      <span className="gallery-card-text"><strong>{titleFromName(item.name)}</strong><small className="clamp">{item.description ? gist(item.description) : item.repo}</small></span>
    </button>
    <footer className="gallery-card-footer">
      <span className="gallery-card-meta"><span>{item.repo}</span></span>
      <span className="spacer" />
      {item.installed ? <span className="chip ok">Installed</span> : <button type="button" className="button secondary small" onClick={() => setPicked(item)}>View</button>}
    </footer>
  </article>;

  return <div className="page skills-page">
    <SideList>
      <header className="page-list-header"><h2>Skills</h2><small className="subtitle">Reusable know-how for bots</small></header>
      <nav className="list">
        <button type="button" className={`list-item compact ${mode === "installed" ? "selected" : ""}`} onClick={() => go("installed")}><span className="list-text"><strong>Your skills</strong></span>{skills.length > 0 && <span className="count">{skills.length}</span>}</button>
        <button type="button" className={`list-item compact ${mode === "browse" ? "selected" : ""}`} onClick={() => go("browse")}><span className="list-text"><strong>Browse catalog</strong></span></button>
        <button type="button" className={`list-item compact ${mode === "sources" ? "selected" : ""}`} onClick={() => go("sources")}><span className="list-text"><strong>Sources</strong></span>{sourcesOn > 0 && <span className="count">{sourcesOn}</span>}</button>
      </nav>
      <input ref={fileInput} type="file" accept=".zip,application/zip" hidden onChange={event => { void importZip(event.target.files?.[0]); event.target.value = ""; }} />
    </SideList>

    <section className="page-content gallery-page">
      {mode === "sources" ? <SourcesPanel sources={sources} onChanged={() => void sourcesChanged()} />
      : mode === "browse" ? (picked ? <CatalogDetail key={picked.id} item={picked} bots={bots} onBack={() => setPicked(null)} onInstalled={result => void installed(result)} />
        : <>
          <PageHeader title="Browse skills" description={<>Skills from {sourcesOn} GitHub collection{sourcesOn === 1 ? "" : "s"}. Read one before installing it: it becomes instructions your bots follow. <button type="button" className="link-button" onClick={() => go("sources")}>Manage sources</button></>}
            actions={<SearchBox value={query} onChange={setQuery} placeholder="Search, e.g. pdf, seo, cold email" />} />
          {error && <div className="inline-error">{error}</div>}
          {searching && results.length === 0 ? <LoaderCircle className="spin" />
            : results.length === 0 ? <div className="gallery-empty"><Compass size={22} /><strong>{sourcesOn === 0 ? "No sources are on" : query ? "No skills match" : "Reading sources…"}</strong><span className="muted small">{sourcesOn === 0 ? "Turn on a source to browse its skills." : query ? "Try other words." : "This takes a moment the first time."}</span></div>
            : <div className="gallery-grid">{results.map(catalogCard)}</div>}
        </>)
      : !doc ? <>
        <PageHeader title="Your skills" description={<>A skill teaches a bot how to do something, like publishing to your blog or writing a research report. Skills live in <code>{workspaceRoot}/skills</code>.</>}
          actions={<>
            {skills.length > 3 && <SearchBox value={filterText} onChange={setFilterText} placeholder="Filter skills" />}
            <button type="button" className="button secondary small" onClick={() => fileInput.current?.click()}><Upload size={13} />Import .zip</button>
            <button type="button" className="button primary small" onClick={() => open(null)}><Plus size={14} />New skill</button>
          </>} />
        {error && <div className="inline-error">{error}</div>}
        {skills.length === 0 ? <div className="gallery-empty"><BookOpen size={22} /><strong>No skills yet</strong><span className="muted small">Install one from the catalog, or write your own.</span>
          <div className="row"><button type="button" className="button secondary small" onClick={() => go("browse")}><Compass size={13} />Browse catalog</button><button type="button" className="button primary small" onClick={() => open(null)}><Plus size={13} />New skill</button></div></div>
          : <div className="gallery-grid">{shownSkills.map(skillCard)}</div>}
      </>
        : <div className="skill-editor">
          <header className="skill-header">
            <div className="skill-header-text">
              <button type="button" className="back-link" onClick={close}>← Your skills</button>
              <div className="row skill-title-row">
                <TileMark seed={isNew ? "new" : selected!} label={doc.title || "?"} size={36} />
                <input className="skill-title-input" value={doc.title} onChange={event => update({ title: event.target.value })} placeholder={isNew ? "Name the skill" : titleFromName(doc.name)} aria-label="Title" autoFocus={isNew} />
              </div>
              <small className="muted">{skill?.source ? <>From {skill.source.repo} · {skill.source.commit.slice(0, 7)} · </> : null}{isNew ? (skillSlug(doc.title) ? `Saved as ${workspaceRoot}/skills/${skillSlug(doc.title)}` : "Saved in a folder under " + workspaceRoot + "/skills") : `${workspaceRoot}/skills/${doc.name}`}</small>
            </div>
            <div className="row skill-actions">
              {notice && <span className="saved-note">{notice}</span>}
              {!isNew && <button type="button" className="icon-button" title="Delete skill" aria-label="Delete skill" onClick={remove}><Trash2 size={15} /></button>}
              <button type="button" className="button primary" disabled={!canSave} onClick={save}>{isNew ? "Create skill" : "Save"}</button>
            </div>
          </header>
          {error && <div className="inline-error">{error}</div>}

          {!isNew && <div className="skill-section">
            <span className="field-label">Used by</span>
            <div className="bot-picker">
              {selectable.map(bot => { const on = bot.skills.includes(selected!); return <button type="button" key={bot.id} className={`bot-chip ${on ? "on" : ""}`} aria-pressed={on} onClick={() => void toggleBot(bot, !on)}>
                <Avatar bot={bot} size={20} /><span>{bot.name}</span>
              </button>; })}
            </div>
            <span className="field-hint">Takes effect from each bot's next message. You can also turn skills on in a bot's settings.</span>
          </div>}

          <label className="field">
            <span className="field-label">When should a bot use it?</span>
            <textarea rows={3} value={doc.description} onChange={event => update({ description: event.target.value })} placeholder="For example: Use when the user asks to publish or update a page on the blog." />
            <span className={`field-hint ${doc.description.trim().length < 40 ? "warn" : ""}`}>{doc.description.trim().length < 40 ? "Too short for bots to know when to use it. Name the situations and words that should trigger it." : "Bots read this to decide when the skill applies, so name the situations and words that should trigger it."}</span>
          </label>

          <div className="field">
            <div className="row between"><span className="field-label">Instructions</span>
              <div className="segmented small">
                <button type="button" className={!preview ? "active" : ""} onClick={() => setPreview(false)}>Edit</button>
                <button type="button" className={preview ? "active" : ""} onClick={() => setPreview(true)}>Preview</button>
              </div>
            </div>
            {preview ? <div className="skill-preview"><Markdown>{doc.instructions || "_Nothing yet._"}</Markdown></div>
              : <textarea className="code skill-instructions" rows={18} value={doc.instructions} onChange={event => update({ instructions: event.target.value })} spellCheck={false} />}
            <span className="field-hint">Step-by-step know-how in Markdown. Long reference material belongs in files next to it.</span>
          </div>

          {!isNew && skill && <div className="skill-section">
            <div className="row between"><span className="field-label">Files</span><button type="button" className="link-button" onClick={() => api.revealSkills()}><FolderOpen size={13} /> Show in Finder</button></div>
            <ul className="skill-file-list">
              <li><FileText size={13} /> SKILL.md</li>
              {(skill.files || []).map(file => <li key={file}><FileText size={13} /> {file}</li>)}
            </ul>
            {!skill.files?.length && <span className="field-hint">Add scripts or reference files to the skill's folder in Finder. Bots read them with read_skill; bots that can run commands can run its scripts.</span>}
          </div>}
        </div>}
    </section>
  </div>;
}
