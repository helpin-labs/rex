import { describe, expect, it } from "vitest";
import { chatModels, contextLabel, groupByLab, labFor, matchesModel, modelName, priceLabel, recommendedModels } from "./models";
import type { ModelInfo } from "./types";

const m = (id: string, patch: Partial<ModelInfo> = {}): ModelInfo => ({ id, embedding: false, ...patch });

describe("models", () => {
  it("know their lab", () => {
    expect(labFor("anthropic/claude-sonnet-5.5").name).toBe("Anthropic");
    expect(labFor("claude-opus-5-5").name).toBe("Anthropic");
    expect(labFor("openai/gpt-5.6-terra").name).toBe("OpenAI");
    expect(labFor("google/gemini-3.8-flash").name).toBe("Google");
    expect(labFor("x-ai/grok-5").name).toBe("xAI");
    expect(labFor("qwen/qwen3.6-35b-a3b").name).toBe("Qwen");
    expect(labFor("cohere/command-a").name).toBe("Cohere");
  });
  it("have readable names", () => {
    expect(modelName(m("anthropic/claude-sonnet-5.5"))).toBe("Claude Sonnet 5.5");
    expect(modelName(m("openai/gpt-5.6-terra"))).toBe("GPT-5.6 Terra");
    expect(modelName(m("~anthropic/claude-sonnet-latest"))).toBe("Claude Sonnet (latest)");
    expect(modelName(m("meta-llama/llama-4-maverick:free"))).toBe("Llama 4 Maverick (free)");
    expect(modelName(m("anthropic/claude-sonnet-5.5", { name: "Anthropic: Claude Sonnet 5.5" }))).toBe("Claude Sonnet 5.5");
  });
  it("label context and prices", () => {
    expect(contextLabel(200_000)).toBe("200K");
    expect(contextLabel(1_000_000)).toBe("1M");
    expect(priceLabel({ price_in: 3, price_out: 15 })).toBe("$3 / $15");
    expect(priceLabel({ price_in: 0.15, price_out: 0.6 })).toBe("$0.15 / $0.6");
    expect(priceLabel({ price_in: 0, price_out: 0 })).toBe("Free");
    expect(priceLabel({})).toBe("");
  });
  it("recommend the newest of each family that can use tools", () => {
    const list = [m("anthropic/claude-sonnet-4.5"), m("anthropic/claude-sonnet-5.5"), m("anthropic/claude-sonnet-5.5:batch"), m("anthropic/claude-opus-5.5"),
      m("anthropic/claude-haiku-5.5"), m("openai/gpt-5.6-terra"), m("openai/gpt-5.5"), m("google/gemini-3.8-pro", { tools: false }), m("text-embedding-3", { embedding: true })];
    expect(recommendedModels(list).map(item => item.id)).toEqual(["anthropic/claude-sonnet-5.5", "anthropic/claude-opus-5.5", "anthropic/claude-haiku-5.5", "openai/gpt-5.6-terra"]);
    expect(chatModels(list).map(item => item.id)).not.toContain("anthropic/claude-sonnet-5.5:batch");
  });
  it("group by lab and search", () => {
    const groups = groupByLab([m("mistralai/mistral-large"), m("anthropic/claude-sonnet-5.5"), m("cohere/command-a")]);
    expect(groups.map(group => group.lab.name)).toEqual(["Anthropic", "Mistral", "Cohere"]);
    expect(matchesModel(m("anthropic/claude-sonnet-5.5"), "sonnet 5.5")).toBe(true);
    expect(matchesModel(m("anthropic/claude-sonnet-5.5"), "anthropic opus")).toBe(false);
  });
});
