import { siAnthropic, siDeepseek, siGooglegemini, siLmstudio, siMeta, siMinimax, siMistralai, siMoonshotai, siNvidia, siOllama, siOpenrouter, siPerplexity, siQwen, siX } from "simple-icons";
import type { ModelInfo, Provider } from "./types";

// Model picking: which lab a model comes from (for its logo and group), a
// readable name, the details worth showing, and a short recommended list.

export type Mark = { name: string; icon?: { path: string; hex: string }; color: string };
const mark = (name: string, icon: { path: string; hex: string } | undefined, color: string): Mark => ({ name, icon: icon && { path: icon.path, hex: icon.hex }, color });

export type Lab = { id: string; name: string; mark: Mark; match: RegExp };
export const labs: Lab[] = [
  { id: "anthropic", name: "Anthropic", mark: mark("Anthropic", siAnthropic, "#191919"), match: /^~?anthropic\/|claude/ },
  { id: "openai", name: "OpenAI", mark: mark("OpenAI", undefined, "#0D0D0D"), match: /^~?openai\/|(^|\/)(gpt|o\d|chatgpt|codex)/ },
  { id: "google", name: "Google", mark: mark("Google", siGooglegemini, "#8E75B2"), match: /^~?google\/|gemini|gemma/ },
  { id: "xai", name: "xAI", mark: mark("xAI", siX, "#000000"), match: /^~?x-ai\/|grok/ },
  { id: "meta", name: "Meta", mark: mark("Meta", siMeta, "#0467DF"), match: /^~?meta-llama\/|llama/ },
  { id: "mistral", name: "Mistral", mark: mark("Mistral", siMistralai, "#FA520F"), match: /^~?mistralai\/|mistral|mixtral|codestral|magistral|devstral|ministral/ },
  { id: "deepseek", name: "DeepSeek", mark: mark("DeepSeek", siDeepseek, "#4D6BFE"), match: /deepseek/ },
  { id: "qwen", name: "Qwen", mark: mark("Qwen", siQwen, "#615CED"), match: /qwen|qwq|alibaba/ },
  { id: "moonshot", name: "Moonshot", mark: mark("Moonshot", siMoonshotai, "#16191E"), match: /moonshot|kimi/ },
  { id: "minimax", name: "MiniMax", mark: mark("MiniMax", siMinimax, "#E73562"), match: /minimax/ },
  { id: "perplexity", name: "Perplexity", mark: mark("Perplexity", siPerplexity, "#1FB8CD"), match: /perplexity|sonar/ },
  { id: "nvidia", name: "NVIDIA", mark: mark("NVIDIA", siNvidia, "#76B900"), match: /nvidia|nemotron/ },
];
const otherLab = (id: string): Lab => {
  const vendor = id.includes("/") ? id.replace(/^~/, "").split("/")[0] : "Other";
  const name = vendor === "Other" ? "Other" : vendor.split(/[-_]/).map(word => word.charAt(0).toUpperCase() + word.slice(1)).join(" ");
  return { id: vendor.toLowerCase(), name, mark: mark(name, undefined, "#8e9bb0"), match: /$^/ };
};

export function labFor(id: string): Lab {
  const lower = id.toLowerCase();
  return labs.find(lab => lab.match.test(lower)) || otherLab(lower);
}

export const providerMarks: Record<Provider, Mark> = {
  local: mark("Local", siLmstudio, "#4338CA"),
  anthropic: mark("Anthropic", siAnthropic, "#191919"),
  openai: mark("OpenAI", undefined, "#0D0D0D"),
  openrouter: mark("OpenRouter", siOpenrouter, "#6566F1"),
  chatgpt: mark("ChatGPT", undefined, "#10A37F"),
};
export const ollamaMark = mark("Ollama", siOllama, "#000000");

const words: Record<string, string> = { gpt: "GPT", claude: "Claude", gemini: "Gemini", gemma: "Gemma", llama: "Llama", grok: "Grok", qwen: "Qwen", deepseek: "DeepSeek", mistral: "Mistral", ai: "AI", it: "IT", vl: "VL", r1: "R1", v3: "V3" };

/** A readable name: the provider's, without its "Lab: " prefix, or one made from the id. */
export function modelName(model: Pick<ModelInfo, "id" | "name">): string {
  const variant = /:([a-z-]+)$/.exec(model.id)?.[1];
  const tag = variant && variant !== "batch" ? ` (${variant})` : "";
  if (model.name?.trim()) return model.name.replace(/^[^:]{2,30}:\s+/, "").trim() + (tag && !model.name.toLowerCase().includes(variant!) ? tag : "");
  let base = model.id.replace(/^~/, "").replace(/:[a-z-]+$/, "");
  base = base.includes("/") ? base.split("/").slice(1).join("/") : base;
  base = base.replace(/-(\d{8}|\d{4}-\d{2}-\d{2})$/, "");
  const gpt = /^gpt-(\d[\w.]*)(.*)$/i.exec(base);
  if (gpt) base = `gpt§${gpt[1]}${gpt[2]}`;
  const name = base.split(/[-_]/).filter(Boolean).map(part => {
    if (part.startsWith("gpt§")) return `GPT-${part.slice(4)}`;
    return words[part.toLowerCase()] || (/^\d/.test(part) ? part : part.charAt(0).toUpperCase() + part.slice(1));
  }).join(" ");
  return (name.replace(/\blatest\b/i, "(latest)") + tag).trim();
}

/** "200K" or "1M" for a context window. */
export function contextLabel(tokens?: number): string {
  if (!tokens) return "";
  if (tokens >= 1_000_000) return `${+(tokens / 1_000_000).toFixed(1)}M`;
  return `${Math.round(tokens / 1000)}K`;
}

/** "$3 / $15", "Free", or "" when the provider gives no prices. */
export function priceLabel(model: Pick<ModelInfo, "price_in" | "price_out">): string {
  if (model.price_in === undefined || model.price_out === undefined) return "";
  if (model.price_in === 0 && model.price_out === 0) return "Free";
  const money = (value: number) => `$${value < 1 ? +value.toFixed(2) : +value.toFixed(value < 10 ? 1 : 0)}`;
  return `${money(model.price_in)} / ${money(model.price_out)}`;
}

/** Models a bot can use: chat models, without batch-only variants. */
export function chatModels(models: ModelInfo[]): ModelInfo[] {
  return models.filter(model => !model.embedding && !model.id.endsWith(":batch"));
}

const version = (id: string, pattern: RegExp) => {
  const found = pattern.exec(id);
  return found ? found[1].split(".").map(Number) : null;
};
const newer = (a: number[], b: number[]) => { for (let i = 0; i < Math.max(a.length, b.length); i++) { const d = (a[i] ?? 0) - (b[i] ?? 0); if (d) return d > 0; } return false; };

// Families worth suggesting, newest version first: each pattern captures the version.
const families: RegExp[] = [
  /claude-sonnet-(\d+(?:\.\d+)?)$/, /claude-opus-(\d+(?:\.\d+)?)$/, /claude-haiku-(\d+(?:\.\d+)?)$/,
  /gpt-(\d+(?:\.\d+)?)(?:-[a-z]+)?$/, /gemini-(\d+(?:\.\d+)?)-pro$/,
];

/** The newest model of each well-known family the provider offers, at most five. */
export function recommendedModels(models: ModelInfo[]): ModelInfo[] {
  const usable = chatModels(models).filter(model => model.tools !== false && !model.id.includes(":"));
  const picks: ModelInfo[] = [];
  for (const family of families) {
    let best: ModelInfo | null = null, bestVersion: number[] | null = null;
    for (const model of usable) {
      const found = version(model.id.toLowerCase(), family);
      if (found && (!bestVersion || newer(found, bestVersion))) { best = model; bestVersion = found; }
    }
    if (best) picks.push(best);
  }
  return picks.slice(0, 5);
}

/** Models grouped by lab, labs in the order above, then the rest by name. */
export function groupByLab(models: ModelInfo[]): { lab: Lab; models: ModelInfo[] }[] {
  const groups = new Map<string, { lab: Lab; models: ModelInfo[] }>();
  for (const model of models) {
    const lab = labFor(model.id);
    if (!groups.has(lab.id)) groups.set(lab.id, { lab, models: [] });
    groups.get(lab.id)!.models.push(model);
  }
  const order = (lab: Lab) => { const index = labs.findIndex(item => item.id === lab.id); return index < 0 ? labs.length : index; };
  return [...groups.values()].sort((a, b) => order(a.lab) - order(b.lab) || a.lab.name.localeCompare(b.lab.name));
}

export function matchesModel(model: ModelInfo, query: string): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  const haystack = `${model.id} ${modelName(model)} ${labFor(model.id).name}`.toLowerCase();
  return words.every(word => haystack.includes(word));
}
