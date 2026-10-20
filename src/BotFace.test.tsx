// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it } from "vitest";
import { BotFace, botColors, faceJob, faces, faceSVG, suggestFace } from "./BotFace";
import { Avatar } from "./ui";

afterEach(cleanup);

describe("blob avatars", () => {
  it("preserves stored faces, old icons and emoji", () => {
    for (const face of faces) expect(faceJob(face.job)).toBe(face.job);
    expect(faceJob("pen-line")).toBe("writer");
    expect(faceJob("✈️")).toBe("travel");
    expect(faceJob("🧑‍🍳")).toBe("chef");
    expect(faceJob("unknown-stored-value")).toBe("bot");
  });

  it.each([
    ["Flight watcher", "travel"], ["Researcher", "research"], ["Scribe", "story"],
    ["QA reviewer", "review"], ["Security scanner", "security"], ["Designer", "design"],
    ["Sales helper", "sales"], ["Tutor", "learning"], ["Automation", "automation"],
  ])("suggests the %s face", (name, job) => expect(suggestFace(name)).toBe(job));

  it("renders a distinct valid SVG for every role, using only a validated colour", () => {
    const drawings = new Set<string>();
    for (const face of faces) {
      const svg = faceSVG(face.job, "#4f6e9c");
      const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
      expect(doc.querySelector("parsererror")).toBeNull();
      expect(doc.querySelector("path")?.getAttribute("d")).toBeTruthy();
      expect(svg).toContain('fill="#4f6e9c"');
      expect(svg).not.toMatch(/NaN|undefined|Infinity/);
      drawings.add(svg);
      const injected = faceSVG(face.job, '\"/><script>alert(1)</script>');
      expect(injected).not.toContain("<script>");
      expect(injected).toContain('fill="#7a7f8c"');
    }
    expect(faces.length).toBe(20);
    expect(drawings.size).toBe(faces.length);
  });

  it("matches the host's advertised face IDs and selectable colours", () => {
    const source = readFileSync("internal/rex/setup.go", "utf8");
    const values = (name: string) => [...source.match(new RegExp(`var ${name} = \\[\\]string\\{([^}]+)}`))![1].matchAll(/"([^"\n]+)"/g)].map(match => match[1]);
    expect(values("botFaces")).toEqual(faces.map(face => face.job));
    expect(values("botColors")).toEqual(botColors);
  });

  it("keeps animation opt-in and gives the main assistant the app logo", () => {
    const {container, rerender} = render(<BotFace icon="review" color="#3e8e87" seed="qa" size={36} />);
    expect(container.querySelector(".working")).toBeNull();
    const timing = (container.firstElementChild as HTMLElement).style.getPropertyValue("--blink");
    rerender(<BotFace icon="review" color="#3e8e87" seed="qa" size={36} working />);
    expect(container.querySelector(".working")).toBeTruthy();
    expect((container.firstElementChild as HTMLElement).style.getPropertyValue("--blink")).toBe(timing);
    expect(container.querySelector(".typing")).toBeNull();
    rerender(<BotFace icon="review" color="#3e8e87" seed="qa" size={36} typing />);
    expect(container.querySelector(".working.typing .mouth")).toBeTruthy();
    rerender(<Avatar bot={{name:"Rex",role:"main",emoji:"compass",color:"#e8613a"}} size={36} />);
    expect(container.querySelector(".app-logo")).toBeTruthy();
    expect(container.querySelector(".bot-face")).toBeNull();
  });
});
