import { useEffect, useRef } from "react";
import { ChevronDown, ChevronUp, Search, X } from "lucide-react";

// ChatSearch finds text in the conversation. Matches are highlighted in
// place with the CSS Custom Highlight API where the webview supports it;
// the current match's message is scrolled into view.

type HighlightRegistry = { set: (name: string, value: unknown) => void; delete: (name: string) => void };
const registry = (): HighlightRegistry | null => (globalThis as { CSS?: { highlights?: HighlightRegistry } }).CSS?.highlights ?? null;
const HighlightCtor = (globalThis as { Highlight?: new (...ranges: Range[]) => unknown }).Highlight;

function rangesIn(root: Element, needle: string): Range[] {
  const ranges: Range[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = (node.textContent || "").toLowerCase();
    for (let at = text.indexOf(needle); at >= 0; at = text.indexOf(needle, at + needle.length)) {
      const range = document.createRange();
      range.setStart(node, at); range.setEnd(node, at + needle.length);
      ranges.push(range);
    }
  }
  return ranges;
}

export function ChatSearch({ query, onQuery, matches, current, onMove, onClose, container, renderKey }: {
  query: string; onQuery: (value: string) => void; matches: string[]; current: number; onMove: (step: 1 | -1) => void; onClose: () => void;
  container: HTMLElement | null; renderKey: unknown;
}) {
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => { input.current?.focus(); input.current?.select(); }, []);

  // Highlight every match, and the current one more strongly.
  useEffect(() => {
    const highlights = registry();
    const needle = query.trim().toLowerCase();
    if (!highlights || !HighlightCtor || !container || !needle) { highlights?.delete("chat-search"); highlights?.delete("chat-search-current"); return; }
    const all = rangesIn(container, needle);
    const currentTurn = matches[current] ? container.querySelector(`[data-turn="${matches[current]}"]`) : null;
    const inCurrent = currentTurn ? rangesIn(currentTurn, needle) : [];
    highlights.set("chat-search", new HighlightCtor(...all));
    highlights.set("chat-search-current", new HighlightCtor(...inCurrent));
    currentTurn?.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [query, matches, current, container, renderKey]);
  useEffect(() => () => { registry()?.delete("chat-search"); registry()?.delete("chat-search-current"); }, []);

  return <div className="chat-search" role="search">
    <Search size={14} />
    <input ref={input} value={query} onChange={event => onQuery(event.target.value)} placeholder="Find in conversation" aria-label="Find in conversation"
      onKeyDown={event => {
        if (event.key === "Enter") { event.preventDefault(); onMove(event.shiftKey ? -1 : 1); }
        if (event.key === "Escape") { event.preventDefault(); onClose(); }
      }} />
    <span className="chat-search-count">{query.trim() ? matches.length ? `${current + 1} of ${matches.length}` : "No matches" : ""}</span>
    <button type="button" className="tool" aria-label="Previous match" disabled={!matches.length} onClick={() => onMove(-1)}><ChevronUp size={15} /></button>
    <button type="button" className="tool" aria-label="Next match" disabled={!matches.length} onClick={() => onMove(1)}><ChevronDown size={15} /></button>
    <button type="button" className="tool" aria-label="Close search" onClick={onClose}><X size={15} /></button>
  </div>;
}
