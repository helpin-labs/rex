import { useEffect, useMemo, useRef, useState } from "react";
import { Check, ChevronDown, Eye, LoaderCircle, Search, Sparkles, Wrench } from "lucide-react";
import { AppMark } from "./AppsView";
import { chatModels, contextLabel, groupByLab, labFor, matchesModel, modelName, priceLabel, providerMarks, recommendedModels } from "./models";
import type { ModelInfo, Provider } from "./types";

const providerLabels: Record<Provider, string> = { local: "Local", anthropic: "Anthropic", openai: "OpenAI", openrouter: "OpenRouter", chatgpt: "ChatGPT" };

// ProviderPicker chooses where a model runs. Providers that need a key or a
// sign-in first say so instead of being chosen.
export function ProviderPicker({ value, providers, onChange }: { value: Provider; providers: { id: Provider; note?: string }[]; onChange: (provider: Provider) => void }) {
  return <div className="provider-picker" role="radiogroup" aria-label="Provider">
    {providers.map(item => <button type="button" role="radio" key={item.id} aria-checked={value === item.id} disabled={!!item.note} title={item.note}
      className={`provider-option ${value === item.id ? "selected" : ""}`} onClick={() => onChange(item.id)}>
      <AppMark app={providerMarks[item.id]} size={24} />
      <span><strong>{providerLabels[item.id]}</strong>{item.note && <small>{item.note}</small>}</span>
    </button>)}
  </div>;
}

function ModelRow({ model, selected, active, onPick, onHover }: { model: ModelInfo; selected: boolean; active: boolean; onPick: () => void; onHover: () => void }) {
  const noTools = model.tools === false;
  const details = [contextLabel(model.context) && `${contextLabel(model.context)} context`, priceLabel(model) && `${priceLabel(model)} per M`].filter(Boolean).join(" · ");
  return <button type="button" role="option" aria-selected={selected} disabled={noTools} data-active={active || undefined}
    className={`model-row ${selected ? "selected" : ""} ${active ? "active" : ""}`} onClick={onPick} onMouseMove={onHover}
    title={noTools ? "This model can't call tools, which every bot needs" : model.id}>
    <AppMark app={labFor(model.id).mark} size={26} />
    <span className="model-row-text">
      <strong>{modelName(model)}</strong>
      <small>{details || model.id}</small>
    </span>
    <span className="model-row-tags">
      {model.vision && <span className="chip" title="Understands images"><Eye size={10} />Vision</span>}
      {noTools && <span className="chip warn"><Wrench size={10} />No tools</span>}
      {selected && <Check size={14} className="model-row-check" />}
    </span>
  </button>;
}

// ModelPicker chooses a model from what the provider offers: recommended
// ones first, then every model grouped by lab, with search. Leaving it on
// the default follows the default model in Settings.
export function ModelPicker({ value, models, loading, error, defaultLabel, onChange }: {
  value: string; models: ModelInfo[]; loading?: boolean; error?: string; defaultLabel: string; onChange: (model: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const usable = useMemo(() => chatModels(models), [models]);
  const current = usable.find(model => model.id === value) || (value ? { id: value, embedding: false } : null);
  const recommended = useMemo(() => recommendedModels(usable), [usable]);
  const searching = query.trim().length > 0;
  const groups = useMemo(() => groupByLab(usable.filter(model => !searching || matchesModel(model, query))), [usable, query, searching]);
  // One flat order for the keyboard: default, recommended, then the groups.
  const flat = useMemo(() => [...(searching ? [] : [null, ...recommended]), ...groups.flatMap(group => group.models)], [searching, recommended, groups]);

  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false); };
    window.addEventListener("pointerdown", onPointer);
    return () => window.removeEventListener("pointerdown", onPointer);
  }, [open]);
  useEffect(() => { setActive(0); }, [query]);
  useEffect(() => { list.current?.querySelector("[data-active]")?.scrollIntoView({ block: "nearest" }); }, [active]);

  const pick = (model: ModelInfo | null) => { if (model?.tools === false) return; onChange(model ? model.id : ""); setOpen(false); setQuery(""); };
  const onKey = (event: React.KeyboardEvent) => {
    if (event.key === "ArrowDown") { event.preventDefault(); setActive(index => Math.min(flat.length - 1, index + 1)); }
    if (event.key === "ArrowUp") { event.preventDefault(); setActive(index => Math.max(0, index - 1)); }
    if (event.key === "Enter") { event.preventDefault(); if (flat.length) pick(flat[active] ?? null); }
    if (event.key === "Escape") { event.preventDefault(); setOpen(false); }
  };
  let position = searching ? 0 : 1 + recommended.length;

  return <div className="model-picker" ref={root}>
    <button type="button" className="model-trigger" aria-haspopup="listbox" aria-expanded={open} onClick={() => setOpen(value => !value)}>
      {current ? <AppMark app={labFor(current.id).mark} size={24} /> : <span className="model-default-mark"><Sparkles size={13} /></span>}
      <span className="model-trigger-text">
        <strong>{current ? modelName(current) : "Default model"}</strong>
        <small>{current ? current.id : defaultLabel}</small>
      </span>
      {loading ? <LoaderCircle size={14} className="spin" /> : <ChevronDown size={15} />}
    </button>
    {open && <div className="model-popover" role="dialog" aria-label="Choose a model" onKeyDown={onKey}>
      <label className="model-search"><Search size={14} />
        <input autoFocus value={query} onChange={event => setQuery(event.target.value)} placeholder={usable.length ? `Search ${usable.length} models` : "Model id"} aria-label="Search models" />
      </label>
      <div className="model-list" role="listbox" ref={list}>
        {error && <p className="model-note warn">{error}</p>}
        {!searching && <>
          <button type="button" role="option" aria-selected={!value} data-active={active === 0 || undefined} className={`model-row ${!value ? "selected" : ""} ${active === 0 ? "active" : ""}`} onClick={() => pick(null)} onMouseMove={() => setActive(0)}>
            <span className="model-default-mark"><Sparkles size={13} /></span>
            <span className="model-row-text"><strong>Default model</strong><small>{defaultLabel}</small></span>
            <span className="model-row-tags">{!value && <Check size={14} className="model-row-check" />}</span>
          </button>
          {recommended.length > 0 && <div className="model-group-title">Recommended</div>}
          {recommended.map((model, index) => <ModelRow key={`r-${model.id}`} model={model} selected={value === model.id} active={active === index + 1} onPick={() => pick(model)} onHover={() => setActive(index + 1)} />)}
        </>}
        {groups.map(group => <div key={group.lab.id}>
          <div className="model-group-title"><AppMark app={group.lab.mark} size={14} />{group.lab.name}<span>{group.models.length}</span></div>
          {group.models.map(model => { const index = position++; return <ModelRow key={model.id} model={model} selected={value === model.id} active={active === index} onPick={() => pick(model)} onHover={() => setActive(index)} />; })}
        </div>)}
        {searching && !groups.length && <div className="model-note">
          No model matches “{query}”.
          <button type="button" className="button secondary small" onClick={() => pick({ id: query.trim(), embedding: false })}>Use “{query.trim()}” as the model id</button>
        </div>}
        {!searching && !usable.length && !loading && !error && <p className="model-note">This provider didn't list its models. Search to type a model id.</p>}
      </div>
    </div>}
  </div>;
}
