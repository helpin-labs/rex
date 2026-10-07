import { describe, expect, it } from "vitest";
import { notificationFor, waitingConversations } from "./attention";
import type { AttentionItem, Bot } from "./types";

const item = (patch: Partial<AttentionItem>): AttentionItem => ({ id: "i", kind: "question", conversation_id: "c1", bot_id: "b1", title: "", at: "", ...patch });

describe("attention", () => {
  it("names the bot and what it needs", () => {
    const bot = { id: "b1", name: "Spain trip" } as Bot;
    expect(notificationFor(item({ detail: "Please solve the **CAPTCHA**" }), bot)).toEqual({ title: "Spain trip has a question", body: "Please solve the CAPTCHA" });
    expect(notificationFor(item({ kind: "approval", title: "Run python3" }), bot).title).toBe("Spain trip needs your approval");
    expect(notificationFor(item({ kind: "routine", title: "Morning digest", detail: "Finished" }), bot)).toEqual({ title: "Morning digest", body: "Spain trip · Finished" });
  });
  it("counts waiting conversations, not routine reports", () => {
    const waiting = waitingConversations([item({ id: "a" }), item({ id: "b", kind: "approval" }), item({ id: "c", conversation_id: "c2" }), item({ id: "d", kind: "routine", conversation_id: "c3" })]);
    expect([...waiting].sort()).toEqual(["c1", "c2"]);
  });
});
