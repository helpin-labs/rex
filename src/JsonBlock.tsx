import { type ReactNode, useState } from "react";
import { Check, ChevronDown, Copy } from "lucide-react";

// JSON in messages and tool inputs, shown formatted and coloured instead of
// as one raw line. Long values start collapsed.

export type TextPart = { kind: "text"; text: string } | { kind: "json"; value: unknown };

const COLLAPSE_AFTER_LINES = 14;

/** Splits a message into plain text and JSON values: the whole message,
 * ```json fences, or {…} / […] spans that parse as JSON. */
export function splitJson(text: string): TextPart[] {
  const whole = parseJson(text.trim());
  if (whole !== undefined) return [{ kind: "json", value: whole }];
  const parts: TextPart[] = [];
  let rest = 0;
  const pushText = (value: string) => { if (value.trim()) parts.push({ kind: "text", text: value }); };
  let index = 0;
  while (index < text.length) {
    const fence = text.startsWith("```", index) ? /^```(?:json)?[ \t]*\n([\s\S]*?)\n?```/i.exec(text.slice(index)) : null;
    if (fence) {
      const value = parseJson(fence[1].trim());
      if (value !== undefined) {
        pushText(text.slice(rest, index));
        parts.push({ kind: "json", value });
        index += fence[0].length;
        rest = index;
        continue;
      }
    }
    const char = text[index];
    if ((char === "{" || char === "[") && (index === 0 || /\s|[:(]/.test(text[index - 1]))) {
      const end = matchingBracket(text, index);
      if (end > index) {
        const candidate = text.slice(index, end + 1);
        const value = candidate.length >= 15 ? parseJson(candidate) : undefined;
        if (value !== undefined && typeof value === "object" && value !== null) {
          pushText(text.slice(rest, index));
          parts.push({ kind: "json", value });
          index = end + 1;
          rest = index;
          continue;
        }
      }
    }
    index++;
  }
  pushText(text.slice(rest));
  return parts.length ? parts : [{ kind: "text", text }];
}

function parseJson(text: string): unknown {
  if (!/^[[{]/.test(text)) return undefined;
  try { return JSON.parse(text); } catch { return undefined; }
}

// The index of the bracket that closes the one at start, skipping strings.
function matchingBracket(text: string, start: number): number {
  const stack: string[] = [];
  let inString = false;
  for (let index = start; index < text.length; index++) {
    const char = text[index];
    if (inString) {
      if (char === "\\") index++;
      else if (char === "\"") inString = false;
      continue;
    }
    if (char === "\"") inString = true;
    else if (char === "{" || char === "[") stack.push(char === "{" ? "}" : "]");
    else if (char === "}" || char === "]") {
      if (stack.pop() !== char) return -1;
      if (stack.length === 0) return index;
    }
  }
  return -1;
}

/** Text that may contain JSON, for user messages. */
export function TextWithJson({ text }: { text: string }) {
  const parts = splitJson(text);
  if (parts.length === 1 && parts[0].kind === "text") return <>{text}</>;
  return <>{parts.map((part, index) => part.kind === "json"
    ? <JsonBlock key={index} value={part.value} />
    : <span key={index} className="text-part">{part.text.trim()}</span>)}</>;
}

const TOKEN = /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g;

function highlight(json: string) {
  const out: ReactNode[] = [];
  let last = 0;
  let key = 0;
  for (const match of json.matchAll(TOKEN)) {
    const at = match.index ?? 0;
    if (at > last) out.push(json.slice(last, at));
    const [token, string, colon, literal, number] = match;
    if (string !== undefined) {
      out.push(<span key={key++} className={colon ? "json-key" : "json-string"}>{string}</span>);
      if (colon) out.push(colon);
    } else if (literal !== undefined) out.push(<span key={key++} className="json-literal">{literal}</span>);
    else if (number !== undefined) out.push(<span key={key++} className="json-number">{number}</span>);
    else out.push(token);
    last = at + token.length;
  }
  if (last < json.length) out.push(json.slice(last));
  return out;
}

function summary(value: unknown): string {
  if (Array.isArray(value)) return `${value.length} item${value.length === 1 ? "" : "s"}`;
  if (value && typeof value === "object") {
    const keys = Object.keys(value);
    return keys.slice(0, 4).join(", ") + (keys.length > 4 ? `, +${keys.length - 4}` : "");
  }
  return typeof value;
}

/** One JSON value, formatted, coloured and copyable. */
export function JsonBlock({ value }: { value: unknown }) {
  const json = JSON.stringify(value, null, 2) ?? String(value);
  const lines = json.split("\n").length;
  const [open, setOpen] = useState(lines <= COLLAPSE_AFTER_LINES);
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(json); setCopied(true); window.setTimeout(() => setCopied(false), 1200); } catch { /* clipboard unavailable */ }
  };
  return <div className={`json-block ${open ? "open" : ""}`}>
    <div className="json-head">
      <button type="button" className="json-toggle" onClick={() => setOpen(current => !current)} aria-expanded={open}>
        <ChevronDown size={13} className="json-chevron" />
        <span className="json-label">JSON</span>
        <span className="json-summary">{Array.isArray(value) ? "[" : "{"} {summary(value)} {Array.isArray(value) ? "]" : "}"}</span>
      </button>
      <button type="button" className="json-copy" title="Copy JSON" aria-label="Copy JSON" onClick={copy}>{copied ? <Check size={13} /> : <Copy size={13} />}</button>
    </div>
    {open && <pre className="json-body">{highlight(json)}</pre>}
  </div>;
}
