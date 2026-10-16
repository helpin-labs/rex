import { describe, expect, it } from "vitest";
import { Bot, ChefHat, Compass } from "lucide-react";
import { resolveIcon, searchIcons, suggestIcons } from "./icons";

describe("bot icons", () => {
  it("suggests icons from the bot's name", () => {
    expect(suggestIcons("Spain trip")[0]).toBe("plane");
    expect(suggestIcons("Data extractor")[0]).toBe("chart");
    expect(suggestIcons("Chef")[0]).toBe("chef-hat");
    expect(suggestIcons("Researcher")[0]).toBe("search");
    expect(suggestIcons("Budget helper")).toContain("wallet");
  });
  it("returns nothing for an empty name", () => {
    expect(suggestIcons("")).toEqual([]);
  });
  it("searches by keyword prefix", () => {
    expect(searchIcons("trav")).toContain("plane");
    expect(searchIcons("zzzz")).toEqual([]);
  });
  it("maps ids and emoji saved before icons", () => {
    expect(resolveIcon("chef-hat")).toBe(ChefHat);
    expect(resolveIcon("🧭")).toBe(Compass);
    expect(resolveIcon("🦄")).toBe(Bot);
  });
});

describe("icon ids shared with the host", () => {
  it("match the list new bots choose from", async () => {
    const { readFileSync } = await import("node:fs");
    const shared = readFileSync(new URL("../internal/rex/bot_icons.txt", import.meta.url), "utf8").trim().split(/\r?\n/);
    const { allIcons } = await import("./icons");
    expect(shared).toEqual(allIcons.map(entry => entry.id));
  });
});
