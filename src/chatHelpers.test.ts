import { describe, expect, it } from "vitest";
import { starterPrompts, turnFailure } from "./chatHelpers";
import type { Bot } from "./types";

const caps = (patch: Partial<Bot["capabilities"]> = {}) => ({ files_read: false, files_write: false, shell: false, web: false, browser: false, memory: true, ...patch });

describe("starter prompts", () => {
  it("offer bot creation and routing for Rex", () => {
    expect(starterPrompts({ role: "main", capabilities: caps({ web: true }), memory_mode: "private" }).slice(0, 2)).toEqual(["Create a bot that ", "Which of my bots should handle "]);
  });
  it("follow what a bot can do, at most four", () => {
    const all = starterPrompts({ capabilities: caps({ web: true, browser: true, files_read: true }), memory_mode: "private" });
    expect(all).toHaveLength(4);
    expect(all[0]).toBe("What can you help me with?");
    expect(starterPrompts({ capabilities: caps(), memory_mode: "off" })).toEqual(["What can you help me with?"]);
  });
});

describe("turn failures", () => {
  it("explain common provider errors", () => {
    expect(turnFailure("status 401: invalid api key")).toMatch(/rejected the key/);
    expect(turnFailure("429 Too Many Requests")).toMatch(/rate limiting/);
    expect(turnFailure("This model's maximum context length is 128000 tokens")).toMatch(/too long/);
    expect(turnFailure("402 Payment Required: insufficient credits")).toMatch(/out of credits/);
    expect(turnFailure('Post "https://openrouter.ai/api/v1/chat/completions": dial tcp: lookup openrouter.ai: no such host')).toMatch(/Couldn't reach/);
  });
  it("falls back to the error itself, shortened", () => {
    expect(turnFailure(undefined)).toBe("The bot could not finish this message.");
    expect(turnFailure("x".repeat(300))).toHaveLength(201);
  });
});
