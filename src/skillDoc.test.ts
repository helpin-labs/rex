import { describe, expect, it } from "vitest";
import { composeSkillDoc, parseSkillDoc, skillSlug, titleFromName } from "./skillDoc";

describe("skill documents", () => {
  it("splits front matter, title and instructions", () => {
    const doc = parseSkillDoc("---\nname: publish-blog\ndescription: Publish a page. Use when: the user asks.\nlicense: MIT\n---\n\n# Publish to the hub\n\nHub URL: x\n\n## 1. Inputs\n");
    expect(doc).toEqual({ name: "publish-blog", description: "Publish a page. Use when: the user asks.", title: "Publish to the hub", instructions: "Hub URL: x\n\n## 1. Inputs", extra: ["license: MIT"] });
  });
  it("reads quoted and folded descriptions", () => {
    expect(parseSkillDoc('---\nname: a\ndescription: "Say \\"hi\\""\n---\nDo it').description).toBe('Say "hi"');
    expect(parseSkillDoc("---\nname: a\ndescription: >-\n  Two lines\n  folded\n---\nDo it").description).toBe("Two lines folded");
  });
  it("keeps a skill without a title or front matter usable", () => {
    expect(parseSkillDoc("1. Step one")).toMatchObject({ name: "", title: "", instructions: "1. Step one" });
  });
  it("writes back what it read, keeping unknown keys", () => {
    const text = composeSkillDoc({ name: "publish-blog", description: "Publish a page. Use when: the user asks.", title: "Publish", instructions: "Step 1", extra: ["license: MIT", "metadata:", "  author: Amad"] });
    expect(text).toBe('---\nname: publish-blog\ndescription: "Publish a page. Use when: the user asks."\nlicense: MIT\nmetadata:\n  author: Amad\n---\n\n# Publish\n\nStep 1\n');
    expect(parseSkillDoc(text)).toMatchObject({ description: "Publish a page. Use when: the user asks.", extra: ["license: MIT", "metadata:", "  author: Amad"] });
  });
  it("names folders like the host", () => {
    expect(skillSlug("Publish to the Content Hub!")).toBe("publish-to-the-content-hub");
    expect(titleFromName("research-report")).toBe("Research report");
  });
});
