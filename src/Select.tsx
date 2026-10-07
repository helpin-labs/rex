import { Children, Fragment, isValidElement, type ReactNode, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown } from "lucide-react";

// Select is the app's dropdown. A native <select> opens the system menu,
// which never matches the app; this one draws its own list and keeps the
// keyboard: arrows, Home/End, Enter, Escape and typing to jump. Options are
// written as <option> children, like a native select.

type Item = { value: string; label: string; disabled: boolean };

function itemsFrom(children: ReactNode): Item[] {
  const items: Item[] = [];
  const walk = (nodes: ReactNode) => Children.forEach(nodes, node => {
    if (!isValidElement(node)) return;
    const props = node.props as { value?: unknown; disabled?: boolean; children?: ReactNode };
    if (node.type === Fragment) return walk(props.children);
    if (node.type !== "option") return;
    const label = Children.toArray(props.children).join("");
    items.push({ value: props.value === undefined ? label : String(props.value), label, disabled: !!props.disabled });
  });
  walk(children);
  return items;
}

export function Select({ value, onChange, children, disabled, className = "", placeholder, "aria-label": ariaLabel }: {
  value: string | number; onChange: (value: string) => void; children: ReactNode; disabled?: boolean; className?: string; placeholder?: string; "aria-label"?: string;
}) {
  const items = useMemo(() => itemsFrom(children), [children]);
  const current = String(value);
  const selected = items.find(item => item.value === current);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [place, setPlace] = useState<{ left: number; top: number; width: number; maxHeight: number; up: boolean } | null>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const typed = useRef({ text: "", at: 0 });
  // Long lists (a provider's models) get a search box.
  const searchable = items.length > 14;
  const [query, setQuery] = useState("");
  const search = useRef<HTMLInputElement>(null);
  const shown = useMemo(() => {
    const words = query.toLowerCase().split(/\s+/).filter(Boolean);
    return items.map((item, index) => ({ item, index })).filter(({ item }) => words.every(word => item.label.toLowerCase().includes(word)));
  }, [items, query]);

  // The next enabled option after (or before) an option, in the order shown.
  const step = (from: number, direction: 1 | -1) => {
    const order = shown.map(entry => entry.index);
    let position = order.indexOf(from);
    if (position < 0) position = direction > 0 ? -1 : order.length;
    for (let next = position + direction; next >= 0 && next < order.length; next += direction) if (!items[order[next]].disabled) return order[next];
    return -1;
  };
  const enabledFrom = (start: number, direction: 1 | -1) => {
    for (let index = start; index >= 0 && index < items.length; index += direction) if (!items[index].disabled) return index;
    return -1;
  };
  const show = () => {
    if (disabled || !items.length) return;
    const index = items.findIndex(item => item.value === current);
    setActive(index >= 0 ? index : Math.max(0, enabledFrom(0, 1)));
    setQuery("");
    setOpen(true);
    trigger.current?.focus({ preventScroll: true });
  };
  const close = useCallback((focus = true) => { setOpen(false); if (focus) trigger.current?.focus({ preventScroll: true }); }, []);
  const choose = (index: number) => {
    const item = items[index];
    if (!item || item.disabled) return;
    if (item.value !== current) onChange(item.value);
    close();
  };

  // Place the list under the field, or above it when there is no room.
  const measure = useCallback(() => {
    if (!trigger.current) return;
    const box = trigger.current.getBoundingClientRect();
    const below = window.innerHeight - box.bottom - 12, above = box.top - 12;
    const up = below < 180 && above > below;
    // As wide as the field, or wider for long options, within the window.
    const longest = items.reduce((most, item) => Math.max(most, item.label.length), 0);
    const width = Math.min(Math.max(box.width, Math.min(longest * 7.4 + 52, 420)), window.innerWidth - 16);
    const left = Math.max(8, Math.min(box.left, window.innerWidth - width - 8));
    setPlace({ left, top: up ? box.top - 4 : box.bottom + 4, width, maxHeight: Math.min(320, up ? above : below), up });
  }, [items]);
  useLayoutEffect(() => { if (open) measure(); }, [open, measure]);

  // Close on a click elsewhere, or when the page scrolls or resizes under it.
  useEffect(() => {
    if (!open) return;
    const outside = (event: MouseEvent) => {
      const target = event.target as Node;
      if (!list.current?.contains(target) && !trigger.current?.contains(target)) close(false);
    };
    const scrolled = (event: Event) => { if (!list.current?.contains(event.target as Node)) close(false); };
    const resized = () => measure();
    document.addEventListener("mousedown", outside);
    window.addEventListener("scroll", scrolled, true);
    window.addEventListener("resize", resized);
    return () => { document.removeEventListener("mousedown", outside); window.removeEventListener("scroll", scrolled, true); window.removeEventListener("resize", resized); };
  }, [open, close, measure]);

  useEffect(() => { if (open && place && searchable) search.current?.focus({ preventScroll: true }); }, [open, place, searchable]);
  // A search keeps the highlight on a match.
  useEffect(() => {
    if (open && shown.length && !shown.some(entry => entry.index === active)) setActive(shown.find(entry => !entry.item.disabled)?.index ?? shown[0].index);
  }, [open, shown, active]);

  // Keep the active option in view by scrolling the list only.
  useEffect(() => {
    const box = list.current, option = box?.querySelector<HTMLElement>(`[data-index="${active}"]`);
    if (!box || !option) return;
    if (option.offsetTop < box.scrollTop) box.scrollTop = option.offsetTop - 4;
    else if (option.offsetTop + option.offsetHeight > box.scrollTop + box.clientHeight) box.scrollTop = option.offsetTop + option.offsetHeight - box.clientHeight + 4;
  }, [open, active, place]);

  // Closed, the field opens on arrows, Enter or Space. Open, the list takes
  // the keys before anything else on the page (WebKit doesn't focus a
  // clicked button, and Escape must not also close a dialog behind it).
  const onKeyDown = (event: React.KeyboardEvent) => {
    if (open) return;
    if (["ArrowDown", "ArrowUp", "Enter", " "].includes(event.key)) { event.preventDefault(); show(); }
  };
  const keys = useRef<(event: KeyboardEvent) => void>(() => undefined);
  keys.current = (event: KeyboardEvent) => {
    const key = event.key;
    const handled = () => { event.preventDefault(); event.stopPropagation(); };
    if (key === "Escape") { handled(); close(); }
    else if (key === "Tab") close(false);
    else if (key === "ArrowDown") { handled(); const next = step(active, 1); if (next >= 0) setActive(next); }
    else if (key === "ArrowUp") { handled(); const next = step(active, -1); if (next >= 0) setActive(next); }
    else if (key === "Home" && !searchable) { handled(); setActive(Math.max(0, enabledFrom(0, 1))); }
    else if (key === "End" && !searchable) { handled(); setActive(Math.max(0, enabledFrom(items.length - 1, -1))); }
    else if (key === "Enter" || (key === " " && !searchable)) { handled(); if (shown.some(entry => entry.index === active)) choose(active); }
    else if (searchable) { /* typing goes to the search box */ }
    else if (key.length === 1 && !event.metaKey && !event.ctrlKey) {
      handled();
      const now = Date.now();
      typed.current = { text: (now - typed.current.at < 700 ? typed.current.text : "") + key.toLowerCase(), at: now };
      const match = items.findIndex(item => !item.disabled && item.label.toLowerCase().startsWith(typed.current.text));
      if (match >= 0) setActive(match);
    }
  };
  useEffect(() => {
    if (!open) return;
    const listener = (event: KeyboardEvent) => keys.current(event);
    window.addEventListener("keydown", listener, true);
    return () => window.removeEventListener("keydown", listener, true);
  }, [open]);

  const listId = useRef(`select-${Math.random().toString(36).slice(2)}`).current;
  return <>
    <button type="button" ref={trigger} className={`select ${open ? "open" : ""} ${className}`} disabled={disabled}
      role="combobox" aria-haspopup="listbox" aria-expanded={open} aria-controls={open ? listId : undefined} aria-label={ariaLabel}
      aria-activedescendant={open ? `${listId}-${active}` : undefined}
      onClick={() => (open ? close() : show())} onKeyDown={onKeyDown}>
      <span className={`select-value ${selected ? "" : "placeholder"}`}>{selected?.label || placeholder || "Choose…"}</span>
      <ChevronDown size={14} className="select-chevron" />
    </button>
    {open && place && createPortal(<div ref={list} id={listId} role="listbox" className={`select-list ${place.up ? "up" : ""}`}
      style={{ left: place.left, width: place.width, maxHeight: place.maxHeight, ...(place.up ? { bottom: window.innerHeight - place.top } : { top: place.top }) }}
      onMouseDown={event => { if (event.target !== search.current) event.preventDefault(); }}>
      {searchable && <input ref={search} className="select-search" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search" aria-label="Search options" />}
      {searchable && shown.length === 0 && <div className="select-empty">No matches</div>}
      {shown.map(({ item, index }) => <div key={`${item.value}-${index}`} id={`${listId}-${index}`} data-index={index} role="option" aria-selected={item.value === current} aria-disabled={item.disabled}
        className={`select-option ${index === active ? "active" : ""} ${item.value === current ? "selected" : ""} ${item.disabled ? "disabled" : ""}`}
        onMouseEnter={() => !item.disabled && setActive(index)} onClick={() => choose(index)}>
        <span title={item.label}>{item.label}</span>{item.value === current && <Check size={14} />}
      </div>)}
    </div>, document.body)}
  </>;
}
