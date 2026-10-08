import { useState } from "react";
import { ListChecks, MessageCircleQuestion, ShieldCheck, SlidersHorizontal } from "lucide-react";
import { botColors, suggestFace } from "./BotFace";
import { Avatar, Field, Modal } from "./ui";

// Names people often start from; picking one fills the name.
const ideas = ["Research assistant", "Inbox helper", "Travel planner", "Price watcher", "Content writer", "Meal planner"];

// A new bot starts from its name: the bot guesses from it what the user
// wants and asks a few questions in its conversation to set itself up.
export function NewBotDialog({ onCreate, onManual, onClose }: { onCreate: (name: string, face: string, color: string) => Promise<void>; onManual: (name: string, color: string) => void; onClose: () => void }) {
  const [name, setName] = useState("");
  const [color, setColor] = useState(() => botColors[Math.floor(Math.random() * botColors.length)]);
  const [busy, setBusy] = useState(false);
  // The face follows the name; the bot can still change it while it sets itself up.
  const face = suggestFace(name);
  const create = async () => {
    if (!name.trim() || busy) return;
    setBusy(true);
    try { await onCreate(name.trim(), face, color); } finally { setBusy(false); }
  };
  return <Modal title="New bot" subtitle="Create" onClose={onClose} className="new-bot-modal"
    footer={<>
      <button type="button" className="button secondary" onClick={() => onManual(name.trim(), color)}><SlidersHorizontal size={14} />Set up manually</button>
      <span className="spacer" />
      <button type="button" className="button primary" disabled={!name.trim() || busy} onClick={create}>{busy ? "Creating…" : "Create"}</button>
    </>}>
    <div className="new-bot">
      <div className="new-bot-preview" style={{ "--bot-color": color } as React.CSSProperties}>
        <Avatar bot={{ name, emoji: face, color }} size={88} />
        <strong>{name.trim() || "Your bot"}</strong>
      </div>
      <div className="new-bot-fields">
        <Field label="What should this bot be called?" hint="Name it after its job. It sets itself up from there.">
          <input value={name} autoFocus maxLength={40} onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === "Enter") void create(); }} placeholder="e.g. Flight watcher" />
        </Field>
        <div className="idea-chips">{ideas.map(idea => <button type="button" key={idea} className={`chip-button ${name === idea ? "on" : ""}`} onClick={() => setName(idea)}>{idea}</button>)}</div>
        <ColorPicker value={color} onChange={setColor} />
      </div>
    </div>
    <ol className="next-steps" aria-label="What happens next">
      <li><MessageCircleQuestion size={15} /><span><strong>It asks what you need</strong><small>A few questions in its own chat</small></span></li>
      <li><ListChecks size={15} /><span><strong>It proposes its setup</strong><small>Instructions, abilities and tools</small></span></li>
      <li><ShieldCheck size={15} /><span><strong>You approve</strong><small>Nothing turns on without you</small></span></li>
    </ol>
  </Modal>;
}

export function ColorPicker({ value, onChange }: { value: string; onChange: (color: string) => void }) {
  return <div className="picker-row" role="radiogroup" aria-label="Colour">{botColors.map(color =>
    <button type="button" key={color} role="radio" aria-checked={value === color} className={`color-choice ${value === color ? "selected" : ""}`} style={{ background: color }} onClick={() => onChange(color)} aria-label={`Colour ${color}`} />)}</div>;
}
