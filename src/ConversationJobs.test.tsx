// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationJobs, ConversationJobHistory, PauseJobsButton, useBotJobs } from "./Jobs";
import { api } from "./api";
import type { Bot, JobView, WorkJob } from "./types";

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });
const bot = { id: "b", name: "Researcher", emoji: "search", color: "blue" } as Bot;
function job(id: string, state: WorkJob["state"] = "running"): JobView {
 return { id, bot_id: "b", conversation_id: "c", items_per_minute: 0, title: id, state, created_at: "2026-11-01T12:00:00Z", updated_at: "2026-11-01T12:00:00Z", current: [], job: {
  id, state, active_runs: state === "running" ? ["worker"] : [], counts: { total: 100, completed: 27, found: 27, failed: 0, running: 3, pending: 70, blocked: 0, retry_wait: 0, no_result: 0, exceptions: 0, cancelled: 0 },
  usage: { input_tokens: 20, output_tokens: 10, cached_input_tokens: 0, reserved_tokens: 0 }, spec: { objective: "Research", batch_size: 10, max_concurrency: 1 }, dataset_revision: 0, created_at: "", updated_at: "",
 } };
}
it("pins only active compact rows; history is collapsed and includes older jobs", () => {
 const jobs = [job("Active"), job("Old", "completed"), job("Paused", "paused")];
 const props = { jobs, bot, onChanged: vi.fn() };
 const { container } = render(<><ConversationJobs {...props} /><ConversationJobHistory {...props} /></>);
 const dock = screen.getByLabelText("Active jobs");
 expect(dock.querySelectorAll(".job-compact")).toHaveLength(1);
 expect(dock.textContent).toContain("27 / 100");
 expect(dock.textContent).not.toContain("Old");
 expect(container.querySelector("details")?.open).toBe(false);
 expect(screen.getByText("Earlier jobs · 2 · 1 paused")).toBeTruthy();
 expect(container.querySelector(".job-card")).toBeNull();
 fireEvent.click(screen.getByRole("button", { name: "View Active" }));
 expect(screen.getByLabelText("Batch job Active")).toBeTruthy();
 expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
});
it("does not pin anything when only finished or paused jobs remain", () => {
 const { container } = render(<ConversationJobs bot={bot} jobs={[job("Old", "completed"), job("Paused", "paused")]} onChanged={vi.fn()} />);
 expect(container.firstChild).toBeNull();
});
it("pauses all running jobs, preserves partial success and reports failures", async () => {
 const changed=vi.fn();
 const action=vi.spyOn(api,"jobAction").mockImplementation(async id => { if(id==="Bad") throw Error("Offline"); return job(id,"paused"); });
 render(<PauseJobsButton jobs={[job("Good"),job("Bad"),job("Paused","paused"),job("Done","completed")]} onChanged={changed} />);
 fireEvent.click(screen.getByRole("button",{name:"Pause jobs"}));
 await waitFor(()=>expect(screen.getByRole("alert").textContent).toContain("Bad: Offline"));
 expect(action.mock.calls).toEqual([["Good","pause"],["Bad","pause"]]);
 expect(changed).toHaveBeenCalledWith(expect.objectContaining({id:"Good",state:"paused"}));
});
it("resumes a paused job from history", async () => {
 const action=vi.spyOn(api,"jobAction").mockResolvedValue(job("Paused"));
 const changed=vi.fn();
 render(<ConversationJobHistory bot={bot} jobs={[job("Paused","paused")]} onChanged={changed} />);
 fireEvent.click(screen.getByText("Earlier jobs · 1 · 1 paused"));
 fireEvent.click(screen.getByRole("button",{name:"Resume Paused"}));
 await waitFor(()=>expect(action).toHaveBeenCalledWith("Paused","resume"));
 expect(changed).toHaveBeenCalled();
});
it("does not let an older poll overwrite a successful pause", async () => {
 let resolvePoll!: (jobs:JobView[])=>void;
 vi.spyOn(api,"jobs").mockImplementation(()=>new Promise(resolve=>{resolvePoll=resolve;}));
 function Harness(){const [jobs,replace]=useBotJobs("b"); return <><span>{jobs[0]?.state}</span><button onClick={()=>replace(job("Good","paused"))}>Set paused</button></>;}
 vi.useFakeTimers();
 render(<Harness />);
 await act(async()=>resolvePoll([job("Good")]));
 act(()=>vi.advanceTimersByTime(3000));
 fireEvent.click(screen.getByText("Set paused"));
 await act(async()=>resolvePoll([job("Good")]));
 expect(screen.getByText("paused")).toBeTruthy();
});
