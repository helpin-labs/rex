import { describe, expect, it } from "vitest";
import { describeApproval, instructionChanges, looksLikePassword, stepLabel } from "./steps";

describe("setup approvals", () => {
  it("describe tools and routines in plain language", () => {
    expect(describeApproval("mcp__setup__enable_tools", { browser: true, shell: true })).toEqual({ title: "Turn on tools for this bot?", lines: ["Browser", "Run commands and Python"] });
    expect(describeApproval("mcp__setup__create_routine", JSON.stringify({ title: "Price check", schedule: "daily", daily_time: "08:00", prompt: "Check prices" })))
      .toEqual({ title: "Add the routine “Price check”?", lines: ["Every day at 08:00", "Check prices"] });
    expect(describeApproval("write_file", {})).toBeNull();
  });
});

describe("instruction changes", () => {
  it("lists removed and added lines", () => {
    expect(instructionChanges("You write a newsletter.\nUse Reuters.", "You write a newsletter.\nUse AP and Ground News.")).toEqual([
      { kind: "remove", text: "Use Reuters." }, { kind: "add", text: "Use AP and Ground News." },
    ]);
  });
});

describe("instruction changes in one paragraph", () => {
  it("compares sentences", () => {
    expect(instructionChanges("You write a newsletter. Use Reuters. Keep it short.", "You write a newsletter. Use AP and Ground News. Keep it short.")).toEqual([
      { kind: "remove", text: "Use Reuters." }, { kind: "add", text: "Use AP and Ground News." },
    ]);
  });
});

describe("passwords typed in chat", () => {
  it("are noticed", () => {
    expect(looksLikePassword("my momondo password is hunter22")).toBe(true);
    expect(looksLikePassword("Password: Tr0ub4dor&3")).toBe(true);
    expect(looksLikePassword("can you reset my password?")).toBe(false);
    expect(looksLikePassword("find flights to Madrid")).toBe(false);
  });
});

describe("batch worker steps", () => {
  const step = (args: object) => stepLabel({ kind: "tool", id: "", tool_name: "submit_item_result", args_text: JSON.stringify(args), result: "", error: "", status: "completed", started_at: "", order: 0 });
  it("names the item each submission is for", () => {
    expect(step({ item_id: "redis", outcome: "completed" })).toBe("Save the result for “redis”");
    expect(step({ item_id: "vite", outcome: "exception" })).toBe("Couldn't finish “vite”");
    expect(step({ item_id: "bun", outcome: "no_result" })).toBe("Found nothing for “bun”");
  });
});
