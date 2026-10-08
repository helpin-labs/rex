// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render } from "@testing-library/react";
import { useSmoothText } from "./useSmoothText";

// A controllable animation clock: each step runs the queued frame 16ms later.
let queue: ((now: number) => void)[] = [];
let now = 0;
const step = () => act(() => { now += 16; const run = queue; queue = []; run.forEach(callback => callback(now)); });

beforeEach(() => {
  queue = []; now = 0;
  vi.stubGlobal("requestAnimationFrame", (callback: (now: number) => void) => { queue.push(callback); return queue.length; });
  vi.stubGlobal("cancelAnimationFrame", () => undefined);
});
afterEach(() => vi.unstubAllGlobals());

function Probe({ text, live, seen }: { text: string; live: boolean; seen: number[] }) {
  const smooth = useSmoothText(text, live);
  seen.push(smooth.text.length);
  return <p>{smooth.text}</p>;
}

describe("useSmoothText", () => {
  it("reveals bursts in small steps and catches up", () => {
    const seen: number[] = [];
    const burst = "x".repeat(140);
    const view = render(<Probe text="" live seen={seen} />);
    let text = "";
    for (let round = 0; round < 3; round++) {
      text += burst;
      view.rerender(<Probe text={text} live seen={seen} />);
      for (let frame = 0; frame < 25; frame++) step(); // 400ms between bursts
    }
    const steps = seen.slice(1).map((value, index) => value - seen[index]).filter(delta => delta > 0);
    // Small steps every frame instead of 140-character jumps…
    expect(Math.max(...steps)).toBeLessThanOrEqual(16);
    expect(steps.length).toBeGreaterThan(60);
    // …staying within about one burst of what has arrived…
    expect(420 - seen.at(-1)!).toBeLessThan(140);
    // …and finishing promptly once the model is done.
    view.rerender(<Probe text={text} live={false} seen={seen} />);
    for (let frame = 0; frame < 12; frame++) step();
    expect(seen.at(-1)).toBe(420);
  });
  it("shows text that was never live at once", () => {
    const seen: number[] = [];
    render(<Probe text={"done ".repeat(50)} live={false} seen={seen} />);
    expect(seen.at(-1)).toBe(250);
  });
  it("finishes quickly after the model stops", () => {
    const seen: number[] = [];
    const view = render(<Probe text="" live seen={seen} />);
    view.rerender(<Probe text={"y".repeat(600)} live={false} seen={seen} />);
    for (let frame = 0; frame < 20; frame++) step();
    expect(seen.at(-1)).toBe(600);
  });
});
