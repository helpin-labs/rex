import { useEffect, useRef, useState } from "react";
import { Blocks, FolderOpen, Settings as SettingsIcon } from "lucide-react";
import { api } from "./api";
import { initials } from "./apps";
import { type Theme, loadTheme, saveTheme } from "./theme";
import type { Status } from "./types";

// The profile button sits at the bottom of the left pane: the user's
// initials, with the bot engine's state as a dot. Its menu holds the places
// that aren't tabs: Settings, Apps, appearance and logs.
export function ProfileMenu({ status, active, onSettings, onApps }: { status: Status | null; active: boolean; onSettings: () => void; onApps: () => void }) {
  const [open, setOpen] = useState(false);
  const [theme, setTheme] = useState<Theme>(loadTheme);
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false); };
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    window.addEventListener("pointerdown", onPointer);
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("pointerdown", onPointer); window.removeEventListener("keydown", onKey); };
  }, [open]);

  const name = status?.user_name || "You";
  const ready = !!status?.runtime_ready;
  const engine = ready ? "Rex is running" : "Bot engine starting…";
  const go = (action: () => void) => () => { setOpen(false); action(); };

  return <div className="profile" ref={root}>
    <button type="button" className={`profile-button ${active || open ? "active" : ""}`} aria-haspopup="menu" aria-expanded={open} title={`${name} · ${engine}`} onClick={() => setOpen(value => !value)}>
      <span className="profile-avatar">{initials(name)}</span>
      <span className={`engine-dot ${ready ? "ready" : ""}`} aria-hidden />
    </button>
    {open && <div className="profile-menu" role="menu">
      <div className="profile-menu-head">
        <span className="profile-avatar large">{initials(name)}</span>
        <span className="grow"><strong>{name}</strong><small className="muted row"><span className={`status-dot ${ready ? "ok" : ""}`} />{engine}</small></span>
      </div>
      <div className="menu-separator" />
      <button type="button" role="menuitem" className="menu-item" onClick={go(onSettings)}><SettingsIcon size={15} />Settings</button>
      <button type="button" role="menuitem" className="menu-item" onClick={go(onApps)}><Blocks size={15} />Connect apps</button>
      <button type="button" role="menuitem" className="menu-item" onClick={go(() => void api.revealLogs())}><FolderOpen size={15} />Show logs</button>
      <div className="menu-separator" />
      <div className="menu-row"><span className="small muted">Appearance</span>
        <div className="segmented small">{(["system", "light", "dark"] as const).map(option => <button type="button" key={option} className={theme === option ? "active" : ""} onClick={() => { setTheme(option); saveTheme(option); }}>{option === "system" ? "Auto" : option === "light" ? "Light" : "Dark"}</button>)}</div>
      </div>
    </div>}
  </div>;
}
