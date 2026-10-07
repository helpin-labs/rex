import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { BookOpen, FileText, FolderOpen, Plus, Trash2, Upload } from "lucide-react";
import { api } from "./api";
import { BotFace } from "./BotFace";
import { AppLogo } from "./AppLogo";
import { SideList } from "./SideList";
import { CatalogDetail, CatalogList, SourcesPanel } from "./SkillCatalog";
import { composeSkillDoc, parseSkillDoc, skillSlug, titleFromName, type SkillDoc } from "./skillDoc";
import type { Bot, CatalogSkill, Skill, SkillSource } from "./types";
import { Empty, Markdown, errorMessage } from "./ui";

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
  const [mode, setMode] = useState<"installed" | "browse">("installed");
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<CatalogSkill[]>([]);
  const [sources, setSources] = useState<SkillSource[]>([]);
  const [picked, setPicked] = useState<CatalogSkill | null>(null);
  const [searching, setSearching] = useState(false);
  const loadSources = useCallback(() => api.skillSources().then(setSources).catch(() => undefined), []);
  useEffect(() => {
    if (mode !== "browse") return;
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
  useEffect(() => { if (selected === null && skills.length) open(skills[0]); }, [skills, selected, open]);

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
    if (!skill || !window.confirm(`Delete the skill “${skillTitle(skill)}” and its folder? Bots that use it stop using it.`)) return;
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

  return <div className="page skills-page">
    <SideList>
      <header className="page-list-header row between">
        <div><h2>Skills</h2><small className="subtitle">Reusable know-how for bots</small></div>
        <div className="row">
          <button type="button" className="icon-button" title="Import a skill (.zip)" aria-label="Import a skill" onClick={() => fileInput.current?.click()}><Upload size={15} /></button>
          <button type="button" className="icon-button" title="New skill" aria-label="New skill" onClick={() => open(null)}><Plus size={16} /></button>
        </div>
        <input ref={fileInput} type="file" accept=".zip,application/zip" hidden onChange={event => { void importZip(event.target.files?.[0]); event.target.value = ""; }} />
      </header>
      <div className="segmented wide skill-tabs">
        <button type="button" className={mode === "installed" ? "active" : ""} onClick={() => setMode("installed")}>Installed</button>
        <button type="button" className={mode === "browse" ? "active" : ""} onClick={() => setMode("browse")}>Browse</button>
      </div>
      {mode === "browse" ? <CatalogList results={results} query={query} onQuery={setQuery} selected={picked?.id ?? null} onSelect={setPicked} loading={searching}
        sourcesOn={sources.filter(source => source.enabled).length} onSources={() => setPicked(null)} />
      : <nav className="list skill-list">
        {isNew && <button type="button" className="list-item selected"><span className="list-icon"><Plus size={15} /></span><span className="list-text"><strong>{doc?.title || "New skill"}</strong><small>Not saved yet</small></span></button>}
        {skills.map(item => {
          const users = usedBy(bots, item.id);
          return <button type="button" key={item.id} className={`list-item ${selected === item.id ? "selected" : ""}`} onClick={() => open(item)}>
            <span className="list-icon"><BookOpen size={15} /></span>
            <span className="list-text"><strong>{skillTitle(item)}</strong>
              <small>{users.length === 0 ? "Not used yet" : `Used by ${users.length === 1 ? users[0].name : `${users.length} bots`}`}{item.files?.length ? ` · ${item.files.length + 1} files` : ""}</small></span>
          </button>;
        })}
        {skills.length === 0 && !isNew && <p className="muted small skill-list-empty">No skills yet. Browse the catalog, or create one.</p>}
      </nav>}
    </SideList>

    <section className="page-content">
      {mode === "browse" ? (picked ? <CatalogDetail key={picked.id} item={picked} bots={bots} onInstalled={result => void installed(result)} />
        : <SourcesPanel sources={sources} onChanged={() => void sourcesChanged()} />)
      : !doc ? <Empty icon={<BookOpen size={22} />} title="No skill selected">A skill teaches a bot how to do something, for example publish to your blog or write a research report. Create one, or import a skill .zip.</Empty>
        : <div className="skill-editor">
          <header className="skill-header">
            <div className="skill-header-text">
              <input className="skill-title-input" value={doc.title} onChange={event => update({ title: event.target.value })} placeholder={isNew ? "Name the skill" : titleFromName(doc.name)} aria-label="Title" />
              <small className="muted">{skill?.source ? <>From {skill.source.repo} · {skill.source.commit.slice(0, 7)} · </> : null}{isNew ? (skillSlug(doc.title) ? `Saved as ${workspaceRoot}/skills/${skillSlug(doc.title)}` : "Saved in a folder under " + workspaceRoot + "/skills") : `${workspaceRoot}/skills/${doc.name}`}</small>
            </div>
            <div className="row skill-actions">
              {notice && <span className="saved-note">{notice}</span>}
              {!isNew && <button type="button" className="icon-button" title="Delete skill" aria-label="Delete skill" onClick={remove}><Trash2 size={15} /></button>}
              <button type="button" className="button primary" disabled={!canSave} onClick={save}>{isNew ? "Create skill" : "Save"}</button>
            </div>
          </header>
          {error && <div className="inline-error">{error}</div>}

          <label className="field">
            <span className="field-label">When should a bot use it?</span>
            <textarea rows={2} value={doc.description} onChange={event => update({ description: event.target.value })} placeholder="For example: Use when the user asks to publish or update a page on the blog." />
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

          {!isNew && <div className="skill-section">
            <span className="field-label">Used by</span>
            <div className="skill-bots">
              {selectable.map(bot => <label key={bot.id} className={`skill-bot ${bot.skills.includes(selected!) ? "on" : ""}`}>
                <input type="checkbox" checked={bot.skills.includes(selected!)} onChange={event => void toggleBot(bot, event.target.checked)} />
                {bot.role === "main" ? <AppLogo size={22} /> : <BotFace icon={bot.emoji} color={bot.color} seed={bot.id} size={22} />}
                <span>{bot.name}</span>
              </label>)}
            </div>
            <span className="field-hint">Turned on here or in each bot's settings; it takes effect from the bot's next message.</span>
          </div>}

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
