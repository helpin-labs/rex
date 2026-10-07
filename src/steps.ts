import { describeRoutine } from "./schedule";
import type { Routine, RunToolCall } from "./types";

// Readable labels for a bot's steps ("Read inbox/notes.txt", "Run python3
// scripts/fetch.py"), in the style of Helpin's transcript.

function parseArgs(text: string): Record<string, unknown> {
  try {
    const value = JSON.parse(text);
    return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
  } catch {
    return {};
  }
}

const str = (value: unknown) => (typeof value === "string" ? value.trim() : "");
const clip = (value: string, max = 60) => (value.length > max ? `${value.slice(0, max - 1)}…` : value);
const quoted = (value: string) => `“${clip(value, 50)}”`;

/** Bookkeeping calls that never appear as user-visible work. */
export function isControlTool(name: string): boolean {
  return ["update_plan", "finish_turn", "get_context", "request_user_input", "request_approval"].includes(name);
}

export function stepLabel(tool: RunToolCall): string {
  const name = tool.tool_name;
  const args = parseArgs(tool.args_text);
  switch (name) {
    case "read_files": {
      const files = Array.isArray(args.files) ? args.files : Array.isArray(args.paths) ? args.paths : [];
      const first = files.length ? str((files[0] as Record<string, unknown>)?.path ?? files[0]) : str(args.path);
      return first ? `Read ${clip(first)}${files.length > 1 ? ` +${files.length - 1} more` : ""}` : "Read files";
    }
    case "list_directory": return `List ${clip(str(args.path) || "the folder")}`;
    case "write_file": return `Write ${clip(str(args.path) || "a file")}`;
    case "edit_file": return `Edit ${clip(str(args.path) || "a file")}`;
    case "apply_patch": return "Apply patch";
    case "run_command": {
      const program = str(args.program) || str(args.command);
      const rest = Array.isArray(args.args) ? args.args.map(String).join(" ") : "";
      return program ? `Run ${clip(`${program} ${rest}`.trim(), 70)}` : "Run a command";
    }
    case "web_search": return str(args.query) ? `Search the web for ${quoted(str(args.query))}` : "Search the web";
    case "fetch_url": return str(args.url) ? `Read ${clip(str(args.url))}` : "Read a web page";
    case "crawl_url": return str(args.url) ? `Crawl ${clip(str(args.url))}` : "Crawl a site";
    case "browser_open": return str(args.url) ? `Open ${clip(str(args.url))}` : "Open a page";
    case "browser_snapshot": return "Look at the page";
    case "browser_read": return "Read the page";
    case "browser_act": {
      const action = str(args.action) || "Use";
      return `${action.charAt(0).toUpperCase()}${action.slice(1)} on the page`;
    }
    case "browser_screenshot": return "Take a screenshot";
    case "mcp__memory__recall": return str(args.query) ? `Search memory for ${quoted(str(args.query))}` : "Search memory";
    case "mcp__memory__remember": return str(args.content) ? `Remember ${quoted(str(args.content))}` : "Save to memory";
    case "mcp__setup__configure_myself": return str(args.name) ? `Set itself up as ${quoted(str(args.name))}` : "Set itself up";
    case "mcp__setup__enable_tools": return "Turn on tools";
    case "mcp__self__update_my_instructions": return "Propose new instructions";
    case "mcp__setup__create_routine": return str(args.title) ? `Add the routine ${quoted(str(args.title))}` : "Add a routine";
  }
  const mcp = /^mcp__(.+?)__(.+)$/.exec(name);
  if (mcp) return `${mcp[2].replace(/_/g, " ")} · ${mcp[1]}`;
  return name.replace(/_/g, " ");
}

export function formatElapsed(milliseconds: number): string {
  const seconds = Math.max(0, Math.round(milliseconds / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  return minutes < 60 ? `${minutes}m ${seconds % 60}s` : `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

const toolNames: Record<string, string> = { browser: "Browser", files_read: "Read files", files_write: "Create and edit files", shell: "Run commands and Python" };

/** Plain-language approval prompts for a new bot's setup tools. */
export function describeApproval(toolName: string, input: unknown): { title: string; lines: string[] } | null {
  const args = typeof input === "string" ? parseArgs(input) : (input && typeof input === "object" ? input as Record<string, unknown> : {});
  if (toolName === "mcp__setup__enable_tools") {
    const tools = Object.keys(toolNames).filter(key => args[key] === true).map(key => toolNames[key]);
    return { title: "Turn on tools for this bot?", lines: tools.length ? tools : ["No tools named"] };
  }
  if (toolName === "mcp__setup__create_routine") {
    const when = str(args.schedule) === "daily" ? `Every day at ${str(args.daily_time) || "?"}`
      : str(args.schedule) === "cron" ? describeRoutine({ schedule_kind: "cron", cron: str(args.cron) } as Routine) : `Every ${String(args.interval_minutes ?? "?")} minutes`;
    return { title: `Add the routine “${str(args.title) || "Untitled"}”?`, lines: [when, str(args.prompt)].filter(Boolean) };
  }
  return null;
}

/** Sentences removed from and added to a bot's instructions, in order. */
export function instructionChanges(before: string, after: string): { kind: "add" | "remove"; text: string }[] {
  // Instructions are often one paragraph, so compare sentence by sentence.
  const lines = (text: string) => text.split(/\n+|(?<=[.!?])\s+(?=[A-Z0-9"“(*-])/).map(line => line.trim()).filter(Boolean);
  const old = lines(before), next = lines(after);
  return [
    ...old.filter(line => !next.includes(line)).map(text => ({ kind: "remove" as const, text })),
    ...next.filter(line => !old.includes(line)).map(text => ({ kind: "add" as const, text })),
  ];
}

/** Whether a message seems to contain a password, e.g. "password: hunter2". */
export function looksLikePassword(text: string): boolean {
  return /\b(password|passwd|pwd|passcode|pass code|lösenord)\b\s*(is|:|=|-)?\s*\S{4,}/i.test(text);
}
