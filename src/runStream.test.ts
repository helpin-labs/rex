import { describe, expect, it } from "vitest";
import { applyRunEvent, applyRunEvents, emptyRunStreamState, latestActivityLabel } from "./runStream";
import type { RunEventEnvelope } from "./types";

function event(type: string, data: Record<string, unknown>, sequence = 1): RunEventEnvelope {
  return { event_id: `event-${sequence}`, sent_at: `2026-08-24T10:00:0${sequence}Z`, sequence_no: sequence, app_id: "personal_agent", run_id: "run-1", type, data };
}

describe("run stream projection", () => {
  it("streams assistant deltas and lets completion repair missing text", () => {
    const state = applyRunEvents(emptyRunStreamState(), [
      event("assistant_message_started", { message_id: "message-1" }, 1),
      event("assistant_message_delta", { message_id: "message-1", content: "Hel" }, 2),
      event("assistant_message_delta", { message_id: "message-1", content: "lo" }, 3),
      event("assistant_message_completed", { message_id: "message-1", content: "Hello, complete." }, 4),
    ]);
    expect(state.assistant[0]).toMatchObject({ content: "Hello, complete.", status: "completed" });
    expect(applyRunEvent(state, event("assistant_message_completed", { message_id: "message-1", content: "wrong" }, 4))).toBe(state);
  });

  it("keeps assistant text and tool calls in their actual timeline order", () => {
    const state = applyRunEvents(emptyRunStreamState(), [
      event("assistant_message_delta", { message_id: "message-1", content: "I will check." }, 1),
      event("tool_call_started", { tool_call_id: "tool-1", tool_name: "web_search" }, 2),
      event("tool_call_finished", { tool_call_id: "tool-1", tool_name: "web_search", output_summary: "Found it" }, 3),
      event("assistant_message_delta", { message_id: "message-1", content: " Here is the answer." }, 4),
      event("assistant_message_completed", { message_id: "message-1", content: "I will check. Here is the answer." }, 5),
    ]);
    expect(state.timeline.map(item => item.kind)).toEqual(["assistant", "tool", "assistant"]);
    expect(state.assistant.map(item => item.content)).toEqual(["I will check.", " Here is the answer."]);
  });

  it("projects reasoning, tool lifecycle, plan, activity, and run state", () => {
    const state = applyRunEvents(emptyRunStreamState(), [
      event("run.started", {}, 1),
      event("reasoning_message_delta", { message_id: "reason-1", content: "Checking context" }, 2),
      event("tool_call_started", { tool_call_id: "tool-1", tool_name: "memory__memory_search", args_text: "{\"q\":" }, 3),
      event("tool_call_args_delta", { tool_call_id: "tool-1", args_text: "{\"q\":\"Ada\"}" }, 4),
      event("activity_snapshot", { activity_id: "activity-1", content: "Searching memories" }, 5),
      event("plan_updated", { plan: [{ step: "Find context", status: "completed" }, { step: "Answer", status: "in_progress" }], note: "Two steps" }, 6),
      event("tool_call_finished", { tool_call_id: "tool-1", tool_name: "memory__memory_search", output_summary: "1 result", duration_ms: 120 }, 7),
      event("reasoning_message_completed", { message_id: "reason-1", content: "Checked context" }, 8),
      event("run.paused", { pause_reason: "turn_complete" }, 9),
    ]);
    expect(state.run_status).toBe("paused");
    expect(state.pause_reason).toBe("turn_complete");
    expect(state.reasoning[0]).toMatchObject({ content: "Checked context", status: "completed" });
    expect(state.tools[0]).toMatchObject({ args_text: "{\"q\":\"Ada\"}", result: "1 result", duration_ms: 120, status: "completed" });
    expect(state.activities[0].content).toBe("Searching memories");
    expect(state.plan).toHaveLength(2);
    expect(state.plan_note).toBe("Two steps");
  });

  it("uses the active tool as the current working label", () => {
    const state = applyRunEvent(emptyRunStreamState(), event("tool_call_started", { tool_call_id: "tool-1", tool_name: "web_search" }));
    expect(latestActivityLabel(state)).toBe("Using Web Search…");
  });
});

it("preserves running status and completed tools across recovery events", () => {
 const waiting=applyRunEvents(emptyRunStreamState(),[
 event("run.started",{},1),
 event("tool_call_finished",{tool_call_id:"saved",tool_name:"web_search",output_summary:"saved result"},2),
 event("run.recovery_wait",{reason:"connection_lost"},3),
 ]);
 expect(waiting.run_status).toBe("started");
 expect(waiting.recovery_reason).toBe("connection_lost");
 expect(waiting.tools[0].status).toBe("completed");
 const resumed=applyRunEvent(waiting,event("run.recovery_started",{},4));
 expect(resumed.recovery_reason).toBeUndefined();
 expect(resumed.tools[0]).toEqual(waiting.tools[0]);
});

it("records retry waiting time separately from active work", () => {
 const state=applyRunEvents(emptyRunStreamState(),[
 event("run.recovery_wait",{reason:"connection_lost"},1),
 event("run.recovery_started",{},6),
 ]);
 expect(state.recovery_wait_ms).toBe(5000);
 expect(state.recovery_started_at).toBeUndefined();
});
