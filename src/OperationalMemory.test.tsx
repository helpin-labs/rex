// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { OperationalMemory } from "./OperationalMemory";
import type { MemoryLearning } from "./types";
import { api } from "./api";

vi.mock("./api", () => ({ api: { historySearch: vi.fn(), historyRead: vi.fn(), consolidateMemory: vi.fn(), retryMemory: vi.fn() } }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });
const state: MemoryLearning = { episodes: [{ turn_id: "old", status: "failed", forgotten: false, facts: 0, attempts: 3, error: "model unavailable", created_at: new Date().toISOString(), updated_at: new Date().toISOString() }], counts: { indexed: 7, failed: 1, forgotten: 2 }, lessons: [{ id: "candidate", text: "A single observed incident", state: "candidate", task_count: 1, verified_tasks: 1, source_fact_ids: [], updated_at: "", is_stale: false }], available: false, model: "learning-model", consolidation: { attempts: 3, error: "service unavailable" }, context: { enabled: false, warning: "Verified context window required" } };

describe("operational memory inspection", () => {
  it("shows pending extraction and queued consolidation separately", () => {
    render(<OperationalMemory bank="bot:a" state={{...state, counts: {queued: 2, extracting: 1}, jobs: [{kind: "consolidation", state: "consolidation_pending"}]}} onRefresh={vi.fn()} />);
    expect(screen.getByText(/3 tasks awaiting learning/)).toBeTruthy();
    expect(screen.getByText("Consolidation queued.")).toBeTruthy();
  });
  it("separates saved tasks, supported lessons and recalled records, and shows failures honestly", () => {
    render(<OperationalMemory bank="bot:a" state={state} onRefresh={vi.fn()} />);
    expect(screen.getByText("8 saved tasks · 0 supported lessons")).toBeTruthy();
    expect(screen.getByText(/subset used for that task/)).toBeTruthy();
    expect(screen.getByText("Verified context window required")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Consolidate now" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/retry limit reached/)).toBeTruthy();
  });
  it("searches this bank and reads exact scoped source evidence", async () => {
    vi.mocked(api.historySearch).mockResolvedValue({ entries: [{ source_id: "tool/old", turn_id: "old", kind: "tool_failure", text: "Recorded error", created_at: "" }], next_cursor: 10 });
    vi.mocked(api.historyRead).mockResolvedValue({ source_id: "tool/old", turn_id: "old", kind: "tool_failure", text: "Exact recorded error", created_at: "", offset: 0, truncated: true, next_offset: 100 });
    render(<OperationalMemory bank="bot:a" state={state} onRefresh={vi.fn()} />);
    fireEvent.click(screen.getByText(/Saved task history/));
    fireEvent.change(screen.getByRole("textbox", { name: "Search recorded task history" }), { target: { value: "old work" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    await waitFor(() => expect(api.historySearch).toHaveBeenCalledWith("bot:a", "old work", 0));
    fireEvent.click(await screen.findByRole("button", { name: "Read source" }));
    await waitFor(() => expect(api.historyRead).toHaveBeenCalledWith("bot:a", "tool/old", 0));
    expect(await screen.findByText("Exact recorded error")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Read next part" }));
    await waitFor(() => expect(api.historyRead).toHaveBeenCalledWith("bot:a", "tool/old", 100));
  });
});
