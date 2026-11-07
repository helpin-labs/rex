import type {
  RunActivity,
  RunAssistantSegment,
  RunEventEnvelope,
  RunPlanStep,
  RunReasoningSegment,
  RunStreamState,
  RunToolCall,
} from "./types";


/** Event names the runtime's live stream (SSE) sends. */
export const runEventTypes = [
  "run.recovery_wait", "run.recovery_started",
  "run.queued", "run.started", "run.resumed", "run.paused", "run.completed", "run.failed", "run.cancelled",
  "assistant_message_started", "assistant_message_delta", "assistant_message_completed",
  "reasoning_message_started", "reasoning_message_delta", "reasoning_message_completed",
  "tool_call_started", "tool_call_args_delta", "tool_call_result", "tool_call_finished",
  "plan_updated", "activity_snapshot", "activity_delta",
] as const;

export const terminalRunStatuses = new Set(["completed", "failed", "cancelled"]);

export function emptyRunStreamState(): RunStreamState {
  return {
    assistant: [], reasoning: [], tools: [], activities: [], plan: [], plan_note: "", timeline: [],
    seen_event_ids: [], event_count: 0,
  };
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function eventTime(event: RunEventEnvelope, receivedAt: string): string {
  return event.sent_at || receivedAt;
}

function appendDelta(current: string, incoming: string): string {
  if (!incoming) return current;
  if (current && incoming.startsWith(current)) return incoming;
  return current + incoming;
}

function appendAssistant(state: RunStreamState, messageId: string, at: string): RunAssistantSegment {
  const segment: RunAssistantSegment = {
    kind: "assistant", id: `${messageId}:${state.event_count}`, message_id: messageId,
    content: "", status: "started", started_at: at, order: state.event_count,
  };
  state.timeline.push(segment);
  state.assistant.push(segment);
  return segment;
}

function activeAssistant(state: RunStreamState, messageId: string, at: string): RunAssistantSegment {
  const last = state.timeline.at(-1);
  return last?.kind === "assistant" && last.message_id === messageId ? last : appendAssistant(state, messageId, at);
}

function appendReasoning(state: RunStreamState, messageId: string, at: string): RunReasoningSegment {
  const segment: RunReasoningSegment = {
    kind: "reasoning", id: `${messageId}:${state.event_count}`, message_id: messageId,
    content: "", status: "started", started_at: at, order: state.event_count,
  };
  state.timeline.push(segment);
  state.reasoning.push(segment);
  return segment;
}

function activeReasoning(state: RunStreamState, messageId: string, at: string): RunReasoningSegment {
  const last = state.timeline.at(-1);
  return last?.kind === "reasoning" && last.message_id === messageId ? last : appendReasoning(state, messageId, at);
}

function ensureTool(state: RunStreamState, data: Record<string, unknown>, at: string): RunToolCall {
  const id = text(data.tool_call_id) || `tool-${state.event_count}`;
  let tool = state.tools.find(item => item.id === id);
  if (!tool) {
    tool = {
      kind: "tool", id, parent_message_id: text(data.parent_message_id) || undefined,
      tool_name: text(data.tool_name) || "tool", args_text: "", result: "", error: "",
      status: "running", started_at: at, order: state.event_count,
    };
    state.tools.push(tool);
    state.timeline.push(tool);
  }
  if (text(data.tool_name)) tool.tool_name = text(data.tool_name);
  if (text(data.parent_message_id)) tool.parent_message_id = text(data.parent_message_id);
  return tool;
}

function parsePlan(value: unknown): RunPlanStep[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap(item => {
    if (!item || typeof item !== "object") return [];
    const row = item as Record<string, unknown>;
    const step = text(row.step);
    return step ? [{ step, status: text(row.status) || "pending" }] : [];
  });
}

export function applyRunEvent(current: RunStreamState, event: RunEventEnvelope, receivedAt = new Date().toISOString()): RunStreamState {
  if (event.event_id && current.seen_event_ids.includes(event.event_id)) return current;
  const timeline = current.timeline.map(item => item.kind === "plan"
    ? { ...item, steps: item.steps.map(step => ({ ...step })) }
    : { ...item });
  const state: RunStreamState = {
    ...current,
    timeline,
    assistant: timeline.filter((item): item is RunAssistantSegment => item.kind === "assistant"),
    reasoning: timeline.filter((item): item is RunReasoningSegment => item.kind === "reasoning"),
    tools: timeline.filter((item): item is RunToolCall => item.kind === "tool"),
    activities: timeline.filter((item): item is RunActivity => item.kind === "activity"),
    plan: current.plan.map(item => ({ ...item })),
    seen_event_ids: event.event_id ? [...current.seen_event_ids, event.event_id] : current.seen_event_ids,
    event_count: current.event_count + 1,
  };
  const data = event.data || {};
  const at = eventTime(event, receivedAt);

  if (event.type === "run.recovery_wait") {
    state.recovery_reason = text(data.reason);
    state.recovery_started_at ||= at;
    return state;
  }
  if ((event.type === "run.recovery_started" || event.type === "run.started" || terminalRunStatuses.has(event.type.slice(4))) && state.recovery_started_at) {
    state.recovery_wait_ms = (state.recovery_wait_ms || 0) + Math.max(0, Date.parse(at) - Date.parse(state.recovery_started_at));
    state.recovery_started_at = undefined;
  }
  if (event.type === "run.recovery_started") {
    state.recovery_reason = undefined;
    return state;
  }
  if (event.type.startsWith("run.")) {
    state.run_status = event.type.slice(4);
    state.recovery_reason = undefined;
    state.pause_reason = text(data.pause_reason) || state.pause_reason;
    if (event.type === "run.failed") state.error = text(data.error) || text(data.error_message) || "The run failed.";
  }

  switch (event.type) {
    case "assistant_message_started": {
      break;
    }
    case "assistant_message_delta": {
      const messageId = text(data.message_id) || state.assistant.at(-1)?.message_id || `assistant-${state.event_count}`;
      const segment = activeAssistant(state, messageId, at);
      segment.content = appendDelta(segment.content, text(data.content) || text(data.text));
      segment.status = "streaming";
      break;
    }
    case "assistant_message_completed": {
      const messageId = text(data.message_id) || state.assistant.at(-1)?.message_id || `assistant-${state.event_count}`;
      const matching = state.assistant.filter(item => item.message_id === messageId);
      const complete = text(data.content) || text(data.text);
      const accumulated = matching.map(item => item.content).join("");
      let segment = matching.at(-1) || appendAssistant(state, messageId, at);
      if (complete && matching.length <= 1) segment.content = complete;
      else if (complete.startsWith(accumulated) && complete.length > accumulated.length) {
        if (state.timeline.at(-1) !== segment) segment = appendAssistant(state, messageId, at);
        segment.content = appendDelta(segment.content, complete.slice(accumulated.length));
      } else if (complete && complete !== accumulated) segment.content = complete;
      segment.status = "completed";
      segment.completed_at = at;
      for (const item of matching) if (item !== segment) {
        item.status = "completed";
        item.completed_at = at;
      }
      break;
    }
    case "reasoning_message_started": {
      break;
    }
    case "reasoning_message_delta": {
      const messageId = text(data.message_id) || state.reasoning.at(-1)?.message_id || `reasoning-${state.event_count}`;
      const segment = activeReasoning(state, messageId, at);
      segment.content = appendDelta(segment.content, text(data.content) || text(data.text));
      segment.status = "streaming";
      break;
    }
    case "reasoning_message_completed": {
      const messageId = text(data.message_id) || state.reasoning.at(-1)?.message_id || `reasoning-${state.event_count}`;
      const segment = state.reasoning.filter(item => item.message_id === messageId).at(-1) || appendReasoning(state, messageId, at);
      const complete = text(data.content) || text(data.text);
      if (complete) segment.content = complete;
      segment.status = "completed";
      segment.completed_at = at;
      break;
    }
    case "tool_call_started": {
      const tool = ensureTool(state, data, at);
      tool.args_text = text(data.args_text) || text(data.tool_input) || tool.args_text;
      break;
    }
    case "tool_call_args_delta": {
      const tool = ensureTool(state, data, at);
      const snapshot = text(data.args_text);
      tool.args_text = snapshot || appendDelta(tool.args_text, text(data.args_delta));
      break;
    }
    case "tool_call_result": {
      const tool = ensureTool(state, data, at);
      tool.result = text(data.output_summary) || text(data.content) || tool.result;
      tool.error = text(data.error) || tool.error;
      break;
    }
    case "tool_call_finished": {
      const tool = ensureTool(state, data, at);
      tool.result = text(data.output_summary) || text(data.content) || tool.result;
      tool.error = text(data.error) || tool.error;
      tool.duration_ms = numberValue(data.duration_ms) ?? tool.duration_ms;
      tool.status = tool.error ? "failed" : "completed";
      tool.completed_at = at;
      break;
    }
    case "activity_snapshot":
    case "activity_delta": {
      const id = text(data.activity_id) || `activity-${state.event_count}`;
      let activity = state.activities.find(item => item.id === id);
      if (!activity) {
        activity = { kind: "activity", id, content: "", started_at: at, order: state.event_count };
        state.activities.push(activity);
        state.timeline.push(activity);
      }
      const content = text(data.content) || text(data.text);
      activity.content = event.type === "activity_snapshot" ? content : appendDelta(activity.content, content);
      break;
    }
    case "plan_updated": {
      state.plan = parsePlan(data.plan);
      state.plan_note = text(data.note) || text(data.explanation);
      state.timeline.push({
        kind: "plan", id: `plan-${state.event_count}`, steps: state.plan.map(step => ({ ...step })),
        note: state.plan_note, started_at: at, order: state.event_count,
      });
      break;
    }
  }
  return state;
}

export function applyRunEvents(state: RunStreamState, events: RunEventEnvelope[]): RunStreamState {
  return [...events]
    .sort((left, right) => (left.sequence_no || 0) - (right.sequence_no || 0))
    .reduce((next, event) => applyRunEvent(next, event, event.sent_at || new Date().toISOString()), state);
}

export function latestActivityLabel(state: RunStreamState): string {
  const runningTool = [...state.tools].reverse().find(tool => tool.status === "running");
  if (runningTool) return `Using ${humanToolName(runningTool.tool_name)}…`;
  const activity = state.activities.at(-1)?.content.trim();
  if (activity) return activity;
  if (state.reasoning.some(item => item.status !== "completed")) return "Thinking…";
  if (state.assistant.some(item => item.status !== "completed")) return "Writing response…";
  return state.run_status === "queued" ? "Waiting to start…" : "Working…";
}

export function humanToolName(value: string): string {
  const name = value.split("__").at(-1) || value;
  return name.replace(/[_-]+/g, " ").replace(/\b\w/g, letter => letter.toUpperCase());
}
