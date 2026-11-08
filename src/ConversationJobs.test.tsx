// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { JobCard, CurrentJobs, ConversationJobHistory, JobStrip, useBotJobs, workerStatus } from "./Jobs";
import { api } from "./api";
import type { Bot, JobView, JobWorker, WorkJob } from "./types";

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });
const bot = { id: "b", name: "Researcher", emoji: "search", color: "blue" } as Bot;
function job(id: string, state: WorkJob["state"] = "running", workers: JobWorker[] = []): JobView {
 return { id, bot_id: "b", conversation_id: "c", items_per_minute: 0, title: id, state, created_at: "2026-11-01T12:00:00Z", updated_at: "2026-11-01T12:00:00Z", current: [], workers, job: {
  id, state, spec_revision: 1, active_runs: workers.map(worker => worker.run_id), counts: { total: 100, completed: 27, found: 27, failed: 0, running: 3, pending: 70, blocked: 0, retry_wait: 0, no_result: 0, exceptions: 0, cancelled: 0 },
  usage: { input_tokens: 20, output_tokens: 10, cached_input_tokens: 0, reserved_tokens: 0 }, spec: { objective: "Research", batch_size: 10, max_concurrency: 1 }, dataset_revision: 0, created_at: "", updated_at: "",
 } };
}
const worker = (run_id: string, extra: Partial<JobWorker> = {}): JobWorker => ({ run_id, status: "running", started_at: "2026-11-01T12:00:00Z", items: [{ id: "i1", label: "Acme" }], done: 0, steps: [], ...extra });

it("shows unfinished jobs in full and finished ones as compact lines that open in place", () => {
 const props = { bot, onChanged: vi.fn(), jobs: [job("Active", "running", [worker("w1")]), job("Old", "completed"), job("Paused", "paused")] };
 const { container } = render(<><ConversationJobHistory {...props} /><CurrentJobs {...props} /></>);
 const current = screen.getByLabelText("Current jobs");
 expect(current.querySelectorAll(".job-card")).toHaveLength(2);
 expect(current.textContent).toContain("Worker 1");
 expect(current.textContent).not.toContain("Old");
 expect(container.querySelector("details")?.open).toBe(false);
 expect(screen.getByText("Earlier jobs · 1")).toBeTruthy();
 fireEvent.click(screen.getByRole("button", { name: /Old/ }));
 expect(screen.getByLabelText("Batch job Old")).toBeTruthy();
 expect(document.querySelector("[role=dialog]")).toBeNull();
});

it("pauses and resumes each job from the strip above the composer", async () => {
 const changed = vi.fn();
 const action = vi.spyOn(api, "jobAction").mockImplementation(async (id, kind) => job(id, kind === "pause" ? "paused" : "running"));
 render(<JobStrip jobs={[job("Good"), job("Paused", "paused"), job("Done", "completed")]} onChanged={changed} />);
 expect(screen.getByLabelText("Running jobs").querySelectorAll(".job-strip-row")).toHaveLength(2);
 fireEvent.click(screen.getByRole("button", { name: "Pause Good" }));
 fireEvent.click(screen.getByRole("button", { name: "Resume Paused" }));
 await waitFor(() => expect(action.mock.calls).toEqual([["Good", "pause"], ["Paused", "resume"]]));
 expect(changed).toHaveBeenCalledTimes(2);
});

it("opens a paused worker by itself, says why it stopped and offers a reply", async () => {
 const question = { id: "q1", run_id: "w2", interaction_kind: "request_user_input", status: "pending", title: "Which founding year?", request_payload: { question: "Which founding year?" } };
 const waiting = job("Research", "waiting", [
  worker("w1"),
  worker("w2", { status: "paused", pause_reason: "human_input", last_message: "The site lists **1998** and 2001.", pending: [question as never] }),
 ]);
 waiting.job!.waiting_reason = "worker_input";
 const onMessage = vi.fn();
 const respond = vi.spyOn(api, "respond").mockResolvedValue({} as never);
 vi.spyOn(api, "job").mockResolvedValue(waiting);
 render(<JobCard view={waiting} bot={bot} onChanged={vi.fn()} onMessage={onMessage} />);
 // Workers stay visible while the job waits on them.
 expect(screen.getByText("Worker 1")).toBeTruthy();
 expect(screen.getByText("Has a question for you")).toBeTruthy();
 // What the worker said renders as Markdown, like the chat.
 expect(screen.getByText("1998").tagName).toBe("STRONG");
 fireEvent.click(screen.getByRole("button", { name: "Reply to worker 2" }));
 expect(onMessage).toHaveBeenCalledWith({ jobId: "Research", jobTitle: "Research", runId: "w2", worker: 2 });
 fireEvent.click(screen.getByRole("button", { name: "Message workers" }));
 expect(onMessage).toHaveBeenLastCalledWith({ jobId: "Research", jobTitle: "Research" });
 expect(respond).not.toHaveBeenCalled();
});

it("says which workers need the user and which can take a message", () => {
 expect(workerStatus(worker("a", { status: "paused", pause_reason: "authentication" }))).toMatchObject({ attention: true, canMessage: false });
 expect(workerStatus(worker("b", { status: "paused", pause_reason: "awaiting_user_message" }))).toMatchObject({ text: "Stopped and waiting for you", attention: true, canMessage: true });
 expect(workerStatus(worker("c", { status: "paused", pause_reason: "human_approval" }))).toMatchObject({ attention: true, canMessage: false });
 expect(workerStatus(worker("d"))).toMatchObject({ text: "Starting…", attention: false, canMessage: true });
});

it("does not let an older poll overwrite a successful pause", async () => {
 let resolvePoll!: (jobs: JobView[]) => void;
 vi.spyOn(api, "jobs").mockImplementation(() => new Promise(resolve => { resolvePoll = resolve; }));
 function Harness() { const [jobs, replace] = useBotJobs("b"); return <><span>{jobs[0]?.state}</span><button onClick={() => replace(job("Good", "paused"))}>Set paused</button></>; }
 vi.useFakeTimers();
 render(<Harness />);
 await act(async () => resolvePoll([job("Good")]));
 act(() => vi.advanceTimersByTime(3000));
 fireEvent.click(screen.getByText("Set paused"));
 await act(async () => resolvePoll([job("Good")]));
 expect(screen.getByText("paused")).toBeTruthy();
});

it("offers a message button on every worker without opening it", () => {
 const onMessage = vi.fn();
 render(<JobCard view={job("Research", "running", [worker("w1"), worker("w2")])} bot={bot} onChanged={vi.fn()} onMessage={onMessage} />);
 expect(screen.getByRole("button", { name: /^Worker 1/ }).getAttribute("aria-expanded")).toBe("false");
 fireEvent.click(screen.getByRole("button", { name: "Message worker 2" }));
 expect(onMessage).toHaveBeenCalledWith({ jobId: "Research", jobTitle: "Research", runId: "w2", worker: 2 });
});

it("cancels a job only after the user confirms in the card", async () => {
 const changed = vi.fn();
 const action = vi.spyOn(api, "jobAction").mockResolvedValue(job("Research", "cancelled"));
 render(<JobCard view={job("Research")} bot={bot} onChanged={changed} />);
 fireEvent.click(screen.getByRole("button", { name: "Cancel job" }));
 expect(action).not.toHaveBeenCalled();
 // The desktop webview shows no confirm dialogs, so the card asks itself.
 expect(screen.getByRole("alertdialog", { name: "Cancel this job?" })).toBeTruthy();
 fireEvent.click(screen.getByRole("button", { name: "Keep running" }));
 expect(screen.queryByRole("alertdialog")).toBeNull();
 fireEvent.click(screen.getByRole("button", { name: "Cancel job" }));
 fireEvent.click(screen.getByRole("button", { name: "Cancel job" }));
 await waitFor(() => expect(action).toHaveBeenCalledWith("Research", "cancel"));
 expect(changed).toHaveBeenCalledWith(expect.objectContaining({ state: "cancelled" }));
});
