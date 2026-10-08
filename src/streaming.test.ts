import { describe, expect, it } from "vitest";
import { CATCH_UP_MS, closeOpenFence, nextShown, sharedPrefix, splitBlocks } from "./streaming";

describe("smooth reveal", () => {
  it("drains a burst in about one burst interval", () => {
    let shown = 0;
    const total = 140;
    for (let elapsed = 0; elapsed < CATCH_UP_MS * 3; elapsed += 16) shown = nextShown(shown, total, 16, false);
    expect(shown).toBe(total);
    expect(nextShown(0, 140, 16, false)).toBeGreaterThan(5);
  });
  it("never overshoots and always moves", () => {
    expect(nextShown(10, 11, 16, false)).toBe(11);
    expect(nextShown(100, 100, 16, false)).toBe(100);
    expect(nextShown(0, 1000, 0, false)).toBe(1);
  });
  it("finishes faster once the answer is complete", () => {
    expect(nextShown(0, 400, 16, true)).toBeGreaterThan(nextShown(0, 400, 16, false));
  });
  it("keeps what two texts share", () => {
    expect(sharedPrefix("Hello world", "Hello there")).toBe(6);
    expect(sharedPrefix("", "abc")).toBe(0);
  });
});

describe("markdown blocks", () => {
  it("split at blank lines", () => {
    expect(splitBlocks("One.\n\nTwo\nlines.\n\n\nThree.")).toEqual(["One.", "Two\nlines.", "Three."]);
  });
  it("keep code fences whole", () => {
    expect(splitBlocks("Code:\n\n```js\nconst a = 1;\n\nconst b = 2;\n```\n\nAfter.")).toEqual(["Code:", "```js\nconst a = 1;\n\nconst b = 2;\n```", "After."]);
  });
  it("keep a list item's indented continuation with it", () => {
    expect(splitBlocks("1. First\n\n   more about first\n2. Second\n\nEnd.")).toEqual(["1. First\n\n   more about first\n2. Second", "End."]);
  });
  it("close a fence that is still open", () => {
    expect(closeOpenFence("Run:\n```sh\nls")).toBe("Run:\n```sh\nls\n```");
    expect(closeOpenFence("```\nx\n```")).toBe("```\nx\n```");
  });
});
