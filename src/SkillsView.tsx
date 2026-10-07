import { useCallback, useEffect, useRef, useState } from "react";
import { BookOpen, FileText, FolderOpen, Plus, Trash2, Upload } from "lucide-react";
import { api } from "./api";
import { SideList } from "./SideList";
import type { Bot, Skill } from "./types";
import { Empty, Field, errorMessage } from "./ui";

// Skills are folders in ~/Rex/skills with a SKILL.md (front matter with a
// name and description, then instructions) and any files it uses, such as
// scripts or reference notes, following the Agent Skills format.

const template = `---
name: my-skill
description: What this skill does and when the bot should use it.
---

# My skill

1. …
`;

export function SkillsView({ bots, workspaceRoot }: { bots: Bot[]; workspaceRoot: string }) {
  const [skills, setSkills] = useState<Skill[]>([]);
  const [draft, setDraft] = useState<Skill | null>(null);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const load = useCallback(() => api.skills().then(list => { setSkills(list); setDraft(current => current ?? list[0] ?? null); }).catch(caught => setError(errorMessage(caught))), []);
  useEffect(() => { void load(); }, [load]);

  const save = async () => {
    if (!draft) return;
    setError("");
    try { const skill = await api.saveSkill(draft); setDraft(skill); setSaved(true); window.setTimeout(() => setSaved(false), 1500); void load(); } catch (caught) { setError(errorMessage(caught)); }
  };
  const remove = async () => {
    if (!draft?.id || !window.confirm(`Delete the skill “${draft.title}” and its folder?`)) return;
    await api.deleteSkill(draft.id); setDraft(null); void load();
  };
  const importZip = async (file: File | undefined) => {
    if (!file) return;
    setError("");
    try { const skill = await api.importSkill(file); setDraft(skill); void load(); } catch (caught) { setError(errorMessage(caught)); }
  };
  const usedBy = draft?.id ? bots.filter(bot => bot.skills.includes(draft.id)) : [];

  return <div className="page">
    <SideList>
      <header className="page-list-header row between"><div><h2>Skills</h2><small className="subtitle">Reusable know-how for bots</small></div>
        <div className="row">
          <button type="button" className="icon-button" title="Import a skill (.zip)" onClick={() => fileInput.current?.click()}><Upload size={15} /></button>
          <button type="button" className="icon-button" title="New skill" onClick={() => setDraft({ id: "", title: "", body: template })}><Plus size={16} /></button>
        </div>
        <input ref={fileInput} type="file" accept=".zip,application/zip" hidden onChange={event => { void importZip(event.target.files?.[0]); event.target.value = ""; }} />
      </header>
      <div className="callout small">Each skill is a folder in <code>{workspaceRoot}/skills</code> with a SKILL.md and any files it needs. Turn skills on per bot in the bot's settings.</div>
      <nav className="list">{skills.map(skill => <button type="button" key={skill.id} className={`list-item ${draft?.id === skill.id ? "selected" : ""}`} onClick={() => setDraft(skill)}>
        <span className="list-icon"><BookOpen size={15} /></span><span className="list-text"><strong>{skill.title}</strong>
          <small>{bots.filter(bot => bot.skills.includes(skill.id)).length} bots{skill.files?.length ? ` · ${skill.files.length + 1} files` : ""}</small></span>
      </button>)}</nav>
    </SideList>
    <section className="page-content">
      {error && <div className="inline-error">{error}</div>}
      {!draft ? <Empty icon={<BookOpen size={22} />} title="No skill selected">A skill teaches a bot how to do something, for example publish to your blog or write a research report. Import a skill .zip, or create one.</Empty> : <div className="skill-editor">
        <div className="content-header"><div><h1>{draft.title || "New skill"}</h1>{draft.description && <p className="muted">{draft.description}</p>}</div></div>
        <Field label="SKILL.md" hint={usedBy.length ? `Used by ${usedBy.map(bot => bot.name).join(", ")}` : "Not turned on for any bot yet"}>
          <textarea className="code" rows={20} value={draft.body} onChange={event => setDraft({ ...draft, body: event.target.value })} spellCheck={false} />
        </Field>
        {draft.id && <div className="skill-files">
          <div className="row between"><span className="field-label">Files</span><button type="button" className="link-button" onClick={() => api.revealSkills()}><FolderOpen size={13} /> Show in Finder</button></div>
          {draft.files?.length ? <ul>{draft.files.map(file => <li key={file}><FileText size={12} /> {file}</li>)}</ul>
            : <p className="muted small">Only SKILL.md. Add scripts or reference files to the skill's folder in Finder; bots read them with read_skill, and bots that can run commands can run its scripts.</p>}
        </div>}
        <div className="row">
          {draft.id && <button type="button" className="button danger-ghost" onClick={remove}><Trash2 size={14} />Delete</button>}
          <span className="spacer" />
          <button type="button" className="button primary" onClick={save}>{saved ? "Saved" : "Save skill"}</button>
        </div>
      </div>}
    </section>
  </div>;
}
