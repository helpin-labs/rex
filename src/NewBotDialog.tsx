import { useState } from "react";
import { botColors, suggestFace } from "./BotFace";
import { Avatar, Field, Modal } from "./ui";

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
  return <Modal title="New bot" onClose={onClose}
    footer={<>
      <button type="button" className="link-button" onClick={() => onManual(name.trim(), color)}>Set it up yourself instead</button>
      <span className="spacer" />
      <button type="button" className="button primary" disabled={!name.trim() || busy} onClick={create}>{busy ? "Creating…" : "Create"}</button>
    </>}>
    <div className="new-bot">
      <Avatar bot={{ name, emoji: face, color }} size={76} />
      <div className="new-bot-fields">
        <Field label="What should this bot be called?" hint="For example “Flight watcher” or “Weekly newsletter”. It will ask you a few questions next.">
          <input value={name} autoFocus maxLength={40} onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === "Enter") void create(); }} placeholder="Name your bot" />
        </Field>
        <ColorPicker value={color} onChange={setColor} />
      </div>
    </div>
  </Modal>;
}

export function ColorPicker({ value, onChange }: { value: string; onChange: (color: string) => void }) {
  return <div className="picker-row" role="radiogroup" aria-label="Colour">{botColors.map(color =>
    <button type="button" key={color} role="radio" aria-checked={value === color} className={`color-choice ${value === color ? "selected" : ""}`} style={{ background: color }} onClick={() => onChange(color)} aria-label={`Colour ${color}`} />)}</div>;
}
