import { describe, expect, it } from "vitest";
import { itemLabel, jobActive, jobFraction, jobPhase } from "./Jobs";
import type { JobView, WorkJob } from "./types";

const counts = { total: 1000, pending: 840, running: 10, retry_wait: 3, blocked: 0, completed: 142, found: 120, no_result: 15, exceptions: 7, failed: 5, cancelled: 0 };
const view = (job: Partial<WorkJob>): JobView => ({
  id: "j", bot_id: "b", conversation_id: "c", title: "Schools", state: job.state || "running", created_at: "", updated_at: "", current: [], items_per_minute: 0,
  job: { id: "j", state: "running", spec: { objective: "", batch_size: 10, max_concurrency: 1 }, counts, usage: { input_tokens: 0, output_tokens: 0, cached_input_tokens: 0, reserved_tokens: 0 }, active_runs: ["r1"], dataset_revision: 0, created_at: "", updated_at: "", ...job },
});

describe("batch job display", () => {
  it("names each state so the user sees what to do", () => {
    expect(jobPhase(view({ state: "running", active_runs: ["a", "b"] })).label).toBe("Running · 2 workers");
    expect(jobPhase(view({ state: "waiting", waiting_reason: "exceptions" }))).toEqual({ label: "Needs you", tone: "warn" });
    expect(jobPhase(view({ state: "waiting", waiting_reason: "retry_backoff" })).tone).toBe("muted");
    expect(jobPhase(view({ state: "completed" })).tone).toBe("ok");
    expect(jobPhase({ ...view({}), job: undefined, error: "the bot engine is starting" }).label).toBe("the bot engine is starting");
  });
  it("counts finished items, including failed ones, against the total", () => {
    expect(jobFraction(view({}))).toBe("147 / 1,000");
  });
  it("treats paused and waiting jobs as active", () => {
    expect(jobActive(view({ state: "paused" }))).toBe(true);
    expect(jobActive(view({ state: "waiting" }))).toBe(true);
    expect(jobActive(view({ state: "cancelled" }))).toBe(false);
  });
  it("labels items with a readable name", () => {
    expect(itemLabel({ id: "nces-1", input: { name: "Mount Airy High", city: "Mount Airy", state: "NC " } })).toBe("Mount Airy High, Mount Airy, NC");
    expect(itemLabel({ id: "x", input: { website: "https://a.example" } })).toBe("https://a.example");
    expect(itemLabel({ id: "x", input: { n: 1 } })).toBe("x");
  });
});
