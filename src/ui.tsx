import { type ReactNode, useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ArrowLeft, Check, Copy, Search, X } from "lucide-react";
import type { Bot } from "./types";
import { BotFace } from "./BotFace";
import { AppLogo } from "./AppLogo";

export function Markdown({ children }: { children: string }) {
  return <div className="markdown"><ReactMarkdown remarkPlugins={[remarkGfm]} components={{ pre: CodeBlock }}>{children}</ReactMarkdown></div>;
}

// Code blocks get a copy button.
function CodeBlock({ children }: { children?: ReactNode }) {
  const ref = useRef<HTMLPreElement>(null);
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(ref.current?.innerText || ""); setCopied(true); window.setTimeout(() => setCopied(false), 1400); } catch { /* clipboard unavailable */ }
  };
  return <div className="code-block"><pre ref={ref}>{children}</pre>
    <button type="button" className="code-copy" onClick={copy} aria-label="Copy code">{copied ? <Check size={12} /> : <Copy size={12} />}</button></div>;
}

/** Mixes a #rrggbb colour towards white (amount > 0) or black (< 0). */
export function shade(hex: string, amount: number): string {
  const value = /^#[0-9a-f]{6}$/i.test(hex) ? hex : "#7a7f8c";
  const target = amount > 0 ? 255 : 0;
  const weight = Math.abs(amount);
  const channel = (index: number) => {
    const current = parseInt(value.slice(index, index + 2), 16);
    return Math.round(current + (target - current) * weight).toString(16).padStart(2, "0");
  };
  return `#${channel(1)}${channel(3)}${channel(5)}`;
}

export function Avatar({ bot, size = 36, round = false, working = false }: { bot: Pick<Bot, "emoji" | "color" | "name"> & { id?: string; role?: string }; size?: number; round?: boolean; working?: boolean }) {
  // The main bot is the app itself and wears the app's logo.
  if (bot.role === "main") return <AppLogo size={size} working={working} />;
  return <BotFace icon={bot.emoji} color={bot.color} seed={bot.id || bot.name} size={size} working={working} />;
}

export function Modal({ title, subtitle, onClose, children, footer, wide, className = "" }: { title: string; subtitle?: string; onClose: () => void; children: ReactNode; footer?: ReactNode; wide?: boolean; className?: string }) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return <div className="modal-backdrop" onMouseDown={event => { if (event.target === event.currentTarget) onClose(); }}>
    <section className={`modal ${wide ? "wide" : ""} ${className}`} role="dialog" aria-modal="true" aria-label={title}>
      <header className="modal-header">
        <div>{subtitle && <small className="eyebrow">{subtitle}</small>}<h2>{title}</h2></div>
        <button type="button" className="icon-button ghost" onClick={onClose} aria-label="Close"><X size={17} /></button>
      </header>
      <div className="modal-body">{children}</div>
      {footer && <footer className="modal-footer">{footer}</footer>}
    </section>
  </div>;
}

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return <label className="field"><span className="field-label">{label}</span>{children}{hint && <span className="field-hint">{hint}</span>}</label>;
}

export function Toggle({ checked, onChange, label, description, disabled, note }: { checked: boolean; onChange: (value: boolean) => void; label: string; description?: string; disabled?: boolean; note?: string }) {
  return <label className={`toggle-row ${disabled ? "disabled" : ""}`}>
    <span className="toggle-text"><strong>{label}</strong>{description && <small>{description}</small>}{note && <small className="toggle-note">{note}</small>}</span>
    <input type="checkbox" role="switch" checked={checked} disabled={disabled} onChange={event => onChange(event.target.checked)} />
    <span className="switch" aria-hidden />
  </label>;
}

export function Empty({ icon, title, children }: { icon: ReactNode; title: string; children?: ReactNode }) {
  return <div className="empty"><div className="empty-icon">{icon}</div><h3>{title}</h3>{children && <div className="empty-body">{children}</div>}</div>;
}

export function relativeTime(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  const seconds = Math.round((Date.now() - date.getTime()) / 1000);
  if (seconds < 0) {
    const ahead = -seconds;
    if (ahead < 60) return "in a moment";
    if (ahead < 3600) return `in ${Math.round(ahead / 60)} min`;
    if (ahead < 86400) return `in ${Math.round(ahead / 3600)} h`;
    return date.toLocaleDateString(undefined, { weekday: "short", hour: "2-digit", minute: "2-digit" });
  }
  if (seconds < 45) return "just now";
  if (seconds < 3600) return `${Math.round(seconds / 60)} min ago`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)} h ago`;
  if (seconds < 7 * 86400) return date.toLocaleDateString(undefined, { weekday: "short" });
  return date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function formatDuration(milliseconds: number): string {
  if (milliseconds < 1_000) return `${milliseconds} ms`;
  const seconds = Math.round(milliseconds / 1_000);
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Opens a link in the user's default browser (the app window stays). */
export async function openExternal(url: string) {
  if ((window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__) {
    const { openUrl } = await import("@tauri-apps/plugin-opener");
    await openUrl(url);
  } else {
    window.open(url, "_blank", "noopener");
  }
}

/** Starts an MCP connection's sign-in in the browser. */
export async function signInToConnection(id: string) {
  const { api } = await import("./api");
  const { url } = await api.startSignIn(id);
  await openExternal(url);
}

// --- page building blocks shared by Apps, Skills, Routines and Settings ---

/** A page's title, what it is for, and its main actions. */
export function PageHeader({ title, description, back, actions }: { title: ReactNode; description?: ReactNode; back?: { label: string; onClick: () => void }; actions?: ReactNode }) {
  return <header className="gallery-header">
    <div className="gallery-header-text">
      {back && <button type="button" className="back-link" onClick={back.onClick}><ArrowLeft size={13} />{back.label}</button>}
      <h1>{title}</h1>
      {description && <p className="muted">{description}</p>}
    </div>
    {actions && <div className="gallery-header-actions">{actions}</div>}
  </header>;
}

export function SearchBox({ value, onChange, placeholder }: { value: string; onChange: (value: string) => void; placeholder: string }) {
  return <label className="gallery-search"><Search size={14} /><input value={value} onChange={event => onChange(event.target.value)} placeholder={placeholder} aria-label={placeholder} /></label>;
}

/** A soft hue that stays the same for the same seed. */
export function seedHue(seed: string): number {
  let hash = 0;
  for (const char of seed) hash = (hash * 31 + char.charCodeAt(0)) | 0;
  return Math.abs(hash) % 360;
}

/** A card's icon: a lucide icon or a letter on a tint picked from the seed. */
export function TileMark({ seed, icon, label, size = 40 }: { seed: string; icon?: ReactNode; label?: string; size?: number }) {
  const hue = seedHue(seed);
  return <span className="tile-mark" style={{ width: size, height: size, borderRadius: Math.round(size * 0.26), "--tile-hue": hue } as React.CSSProperties}>
    {icon || <span style={{ fontSize: Math.round(size * 0.42) }}>{(label || seed).trim().charAt(0).toUpperCase()}</span>}
  </span>;
}
