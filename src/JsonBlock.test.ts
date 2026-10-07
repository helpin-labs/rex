import { describe, expect, it } from "vitest";
import { splitJson } from "./JsonBlock";

describe("splitJson", () => {
  it("formats a message that is all JSON", () => {
    expect(splitJson('  {"domain":"databee.me","vendors":["zscaler","talos"]} ')).toEqual([{ kind: "json", value: { domain: "databee.me", vendors: ["zscaler", "talos"] } }]);
  });
  it("finds JSON inside text", () => {
    const parts = splitJson('Use this config: {"retries": 3, "notify": true} and tell me if it works.');
    expect(parts.map(part => part.kind)).toEqual(["text", "json", "text"]);
    expect(parts[1]).toEqual({ kind: "json", value: { retries: 3, notify: true } });
  });
  it("reads ```json fences", () => {
    const parts = splitJson('Here:\n```json\n[{"a": 1}, {"b": "}"}]\n```\nThanks');
    expect(parts[1]).toEqual({ kind: "json", value: [{ a: 1 }, { b: "}" }] });
  });
  it("leaves ordinary text alone", () => {
    for (const text of ["Book {two} rooms", "array[0] is fine", "[link] and {braces}", "Just text"]) {
      expect(splitJson(text)).toEqual([{ kind: "text", text }]);
    }
  });
});
