// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { TurnView } from "./TurnView";
import type { Bot, RunEventEnvelope, Turn } from "./types";

afterEach(cleanup);

it("shows a historical failure from its stored timeline after a new run starts", () => {
  const error = "failed to read stream: stream error: stream ID 27; INTERNAL_ERROR; received from peer";
  const turn = { id: "old-turn", run_id: "old-run", user_text: "Research companies", assistant_text: "Twenty records are saved.", status: "failed", started_at: "2026-10-13T18:33:03Z", settled_at: "2026-10-13T18:36:41Z", memory_status: "pending", memory_facts: 0 } as Turn;
  const bot = { id: "researcher", name: "Lead Researcher", emoji: "search", color: "blue" } as Bot;
  const events: RunEventEnvelope[] = [{ event_id: "failed", app_id: "rex", run_id: "old-run", type: "run.failed", sequence_no: 1, sent_at: turn.settled_at, data: { error } }];
  const props = { turn, bot, events, working: false, memoryWhere: "Lead Researcher", overview: null, onShowMemory: vi.fn() };
  const { rerender } = render(<TurnView {...props} />);
  expect(screen.getByText(/connection dropped while streaming/)).toBeTruthy();
  expect(document.querySelector(".turn-failure")?.getAttribute("title")).toBe(error);
  expect(screen.queryByText("The bot could not finish this message.")).toBeNull();
  rerender(<TurnView {...props} error="429 Too Many Requests" />);
  expect(screen.getByText(/rate limiting/)).toBeTruthy();
});

it("shows automatic recovery without a failure or an active spinner", () => {
 const turn = { id:"waiting", run_id:"run", status:"working", user_text:"Continue", started_at:new Date().toISOString(), memory_facts:0 } as Turn;
 const bot = { id:"bot", name:"Trip", emoji:"search", color:"blue" } as Bot;
 const events: RunEventEnvelope[] = [{ event_id:"wait",app_id:"rex",run_id:"run",type:"run.recovery_wait",sequence_no:1,sent_at:turn.started_at,data:{reason:"connection_lost"} }];
 render(<TurnView turn={turn} bot={bot} events={events} working={true} memoryWhere="Trip" overview={null} onShowMemory={vi.fn()} />);
 expect(screen.getByText(/Will resume automatically/)).toBeTruthy();
 expect(document.querySelector(".turn-failure")).toBeNull();
 expect(document.querySelector(".spin.work-icon")).toBeNull();
});
