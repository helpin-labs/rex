import { useState } from "react";
import { Select } from "./Select";
import { ArrowRight, Check, CircleAlert, LoaderCircle } from "lucide-react";
import { api } from "./api";
import type { ProviderTest, Settings } from "./types";
import { Field, errorMessage } from "./ui";
import { AppLogo } from "./AppLogo";
import { useModels } from "./SettingsView";

// First run: connect a model so the default bots can work.
export function Onboarding({ initial, onDone }: { initial: Settings; onDone: (settings: Settings) => void }) {
  const [settings, setSettings] = useState(initial);
  const [mode, setMode] = useState<"local" | "hosted">("local");
  const [refresh, setRefresh] = useState(0);
  const local = useModels("local", refresh);
  const [test, setTest] = useState<ProviderTest | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const chat = local.models.filter(model => !model.embedding);
  const embed = local.models.filter(model => model.embedding);

  const check = async () => {
    setBusy(true); setError(""); setTest(null);
    try {
      await api.saveSettings({ ...settings, default_provider: "local" });
      setRefresh(value => value + 1);
      if (settings.default_model) setTest(await api.testProvider("local", settings.default_model));
    } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); }
  };

  const finish = async () => {
    setBusy(true); setError("");
    try {
      const next: Settings = mode === "local"
        ? { ...settings, default_provider: "local", onboarded: true, memory: { ...settings.memory, enabled: true, provider: "local", embedding_source: "bundled" } }
        : { ...settings, onboarded: true, memory: { ...settings.memory, embedding_source: "bundled" } };
      const saved = await api.saveSettings(next);
      for (const bot of await api.bots()) {
        if (!bot.model) await api.saveBot({ ...bot, provider: saved.default_provider, model: saved.default_model });
      }
      onDone(saved);
    } catch (caught) { setError(errorMessage(caught)); } finally { setBusy(false); }
  };

  return <div className="onboarding">
    <div className="onboarding-card">
      <AppLogo size={72} />
      <h1>Welcome to Rex</h1>
      <p className="muted">Personal bots that work on your computer, remember what matters, and ask before they act. First, connect a model.</p>
      <div className="segmented wide">
        <button type="button" className={mode === "local" ? "active" : ""} onClick={() => setMode("local")}>Local model (LM Studio, Ollama)</button>
        <button type="button" className={mode === "hosted" ? "active" : ""} onClick={() => setMode("hosted")}>Hosted (Anthropic, OpenAI, OpenRouter)</button>
      </div>
      {error && <div className="inline-error">{error}</div>}
      {mode === "local" ? <>
        <Field label="Server URL" hint={local.error ? <span className="danger-text">{local.error}</span> : `${chat.length} chat models found. Load a tool-capable model with at least 16k context.`}>
          <div className="row"><input value={settings.local.base_url} onChange={event => setSettings({ ...settings, local: { ...settings.local, base_url: event.target.value } })} /><button type="button" className="button secondary small" onClick={() => { void api.saveSettings(settings).then(() => setRefresh(value => value + 1)); }}>Find models</button></div>
        </Field>
        <Field label="Model for your bots">
          <Select value={settings.default_model} onChange={value => { setSettings({ ...settings, default_model: value }); setTest(null); }}>
            <option value="">Choose a model</option>{chat.map(model => <option key={model.id}>{model.id}</option>)}
          </Select>
        </Field>
        <div className="row"><button type="button" className="button secondary" disabled={!settings.default_model || busy} onClick={check}>{busy ? <><LoaderCircle className="spin" size={14} />Testing…</> : "Test model"}</button>
          {test && <span className={`test-result inline ${test.ok ? "ok" : "bad"}`}>{test.ok ? <Check size={14} /> : <CircleAlert size={14} />}{test.message}</span>}</div>
      </> : <>
        {(["anthropic", "openai", "openrouter"] as const).map(key => <Field key={key} label={key === "anthropic" ? "Anthropic API key" : key === "openai" ? "OpenAI API key" : "OpenRouter API key"}>
          <input type="password" value={settings[key].api_key || ""} onChange={event => setSettings({ ...settings, default_provider: key, [key]: { ...settings[key], api_key: event.target.value } })} />
        </Field>)}
        <p className="muted small">Keys go to your system credential store. The first provider you fill in becomes the default.</p>
      </>}
      <button type="button" className="button primary big" disabled={busy || (mode === "local" ? !settings.default_model : !["anthropic", "openai", "openrouter"].some(key => settings[key as "openai"].api_key))} onClick={finish}>Start using Rex <ArrowRight size={16} /></button>
    </div>
  </div>;
}
