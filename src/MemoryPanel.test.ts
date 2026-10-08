import { describe, expect, it } from "vitest";
import { bankLabel, memoryFailureReason, memoryHome, memoryWhereLabel } from "./MemoryPanel";
import type { Bot, MemoryOverview } from "./types";

const bot = (id: string, name: string, memory_mode: Bot["memory_mode"]) => ({ id, name, memory_mode } as Bot);
const overview: MemoryOverview = { available: true, status: "", banks: [
  { bank: "bot:spain", label: "Spain trip", bot_ids: ["spain"] },
  { bank: "shared", label: "Shared memory", bot_ids: ["scribe", "data"] },
] };
const bots = [bot("spain", "Spain trip", "private"), bot("scribe", "Scribe", "shared"), bot("data", "Data extractor", "shared")];

describe("where memory lives", () => {
  it("is private or shared with named bots", () => {
    expect(memoryWhereLabel(memoryHome(bots[0], overview, bots), bots[0])).toBe("Only Spain trip");
    const shared = memoryHome(bots[1], overview, bots)!;
    expect(shared.sharedWith.map(item => item.name)).toEqual(["Data extractor"]);
    expect(memoryWhereLabel(shared, bots[1])).toBe("Shared with 1 bot");
    expect(memoryHome(bot("x", "X", "off"), overview, bots)).toBeNull();
  });
  it("names the bank a recalled memory came from", () => {
    expect(bankLabel("shared", overview)).toBe("Shared memory");
    expect(bankLabel("bot:spain", overview)).toBe("Spain trip");
  });
});

describe("memory failures", () => {
  it("say why in plain words", () => {
    expect(memoryFailureReason('Post "http://127.0.0.1:1234/v1/chat/completions": dial tcp 127.0.0.1:1234: connect: connection refused')).toBe("The memory model at 127.0.0.1:1234 isn't reachable. Start your local model server, or run memory with OpenAI.");
    expect(memoryFailureReason("status 401: invalid api key")).toMatch(/rejected its key/);
    expect(memoryFailureReason("model gpt-x not found")).toMatch(/wasn't found/);
    expect(memoryFailureReason("something odd")).toMatch(/on hover/);
  });
});
