import type { ChatGPTStatus,
  CommandSandboxStatus, AttentionItem, Bot, BotLogin, InstructionProposal, Conversation, ConversationDetail, GoogleStatus, MCPConnection, MemoryOverview, MemorySource, MemoryLearning, HistoryPage, HistoryExcerpt, ModelInfo, ProviderTest,
  Routine, RoutineRun, Run, RunEventEnvelope, RunSteeringCommand, Settings, Skill, Status, Turn, WorkspaceFile, CatalogSkill, SkillSource, JobView, WorkItemPage,
} from "./types";

type Connection = { url: string; token: string };

let connection: Connection | null = null;

// The desktop shell hands the webview the host URL and a per-launch token.
// In a plain browser (development) they come from the page URL instead.
export async function connect(): Promise<Connection> {
  if (connection) return connection;
  const tauri = (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
  if (tauri) {
    const { invoke } = await import("@tauri-apps/api/core");
    for (let attempt = 0; attempt < 100; attempt++) {
      const value = await invoke<Connection | null>("host_connection");
      if (value?.url) { connection = value; return value; }
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    throw new Error("Rex's backend did not start");
  }
  const params = new URLSearchParams(window.location.search);
  const url = params.get("host") || localStorage.getItem("rex.host") || "http://127.0.0.1:18797";
  const token = params.get("token") || localStorage.getItem("rex.token") || "";
  try { localStorage.setItem("rex.host", url); localStorage.setItem("rex.token", token); } catch { /* storage unavailable */ }
  connection = { url, token };
  return connection;
}

export class ApiError extends Error {
  constructor(message: string, readonly status: number) { super(message); }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const { url, token } = await connect();
  const headers: Record<string, string> = { Authorization: `Bearer ${token}` };
  if (options.body && !(options.body instanceof FormData)) headers["Content-Type"] = "application/json";
  const response = await fetch(url + path, { ...options, headers: { ...headers, ...(options.headers as Record<string, string>) } });
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new ApiError(body.error || `Request failed (${response.status})`, response.status);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

const json = (method: string, body?: unknown): RequestInit => ({ method, body: body === undefined ? undefined : JSON.stringify(body) });

export const api = {
  status: () => request<Status>("/api/status"),
  commandSandbox: () => request<CommandSandboxStatus>("/api/command-sandbox"),
  setupCommandSandbox: () => request<CommandSandboxStatus>("/api/command-sandbox/setup", { method: "POST" }),
  attention: () => request<AttentionItem[]>("/api/attention"),
  settings: () => request<Settings>("/api/settings"),
  saveSettings: (settings: Settings) => request<Settings>("/api/settings", json("PUT", settings)),
  testProvider: (provider: string, model: string) => request<ProviderTest>("/api/settings/test", json("POST", { provider, model })),
  models: (provider: string) => request<ModelInfo[]>(`/api/providers/${provider}/models`),
  chatgpt: () => request<ChatGPTStatus>("/api/chatgpt"),
  chatgptLogin: () => request<ChatGPTStatus>("/api/chatgpt/login", { method: "POST" }),
  chatgptPoll: () => request<ChatGPTStatus>("/api/chatgpt/login/poll", { method: "POST" }),
  chatgptSignOut: () => request<ChatGPTStatus>("/api/chatgpt", { method: "DELETE" }),

  bots: () => request<Bot[]>("/api/bots"),
  logins: (botId: string) => request<BotLogin[]>(`/api/bots/${encodeURIComponent(botId)}/logins`),
  saveLogin: (botId: string, id: string | undefined, login: { site: string; username: string; password?: string; two_step_key?: string }) =>
    request<BotLogin>(`/api/bots/${encodeURIComponent(botId)}/logins${id ? `/${id}` : ""}`, json(id ? "PUT" : "POST", login)),
  deleteLogin: (botId: string, id: string) => request<void>(`/api/bots/${encodeURIComponent(botId)}/logins/${id}`, json("DELETE")),
  proposals: () => request<InstructionProposal[]>("/api/proposals"),
  jobs: (botId?: string) => request<JobView[]>(`/api/jobs${botId ? `?bot_id=${encodeURIComponent(botId)}` : ""}`),
  job: (id: string) => request<JobView>(`/api/jobs/${encodeURIComponent(id)}`),
  jobItems: (id: string, state = "", after = "", limit = 100) =>
    request<WorkItemPage>(`/api/jobs/${encodeURIComponent(id)}/items?state=${encodeURIComponent(state)}&after=${encodeURIComponent(after)}&limit=${limit}`),
  jobAction: (id: string, action: "pause" | "resume" | "cancel") => request<JobView>(`/api/jobs/${encodeURIComponent(id)}/${action}`, json("POST", {})),
  messageWorker: (id: string, runId: string, command_id: string, content: string) => request<JobView>(`/api/jobs/${encodeURIComponent(id)}/workers/${encodeURIComponent(runId)}/message`, json("POST", { command_id, content })),
  steerJob: (id: string, expected_revision: number, content: string) => request<JobView>(`/api/jobs/${encodeURIComponent(id)}/steer`, json("POST", { expected_revision, content })),
  revealJobResults: (id: string) => request<void>(`/api/jobs/${encodeURIComponent(id)}/reveal`, json("POST", {})),
  lookBack: (botId: string) => request<void>(`/api/bots/${botId}/retrospective`, json("POST", {})),
  answerProposal: (id: string, action: "apply" | "discard" | "review") => request<void>(`/api/proposals/${id}`, json("POST", { action })),
  draftBot: (name: string, face: string, color: string) => request<Bot>("/api/bots/draft", json("POST", { name, face, color })),
  createBot: (bot: Partial<Bot>) => request<Bot>("/api/bots", json("POST", bot)),
  saveBot: (bot: Bot) => request<Bot>(`/api/bots/${encodeURIComponent(bot.id)}`, json("PUT", bot)),
  deleteBot: (id: string) => request<void>(`/api/bots/${encodeURIComponent(id)}`, json("DELETE")),
  files: (botId: string) => request<{ root: string; files: WorkspaceFile[]; total?: number }>(`/api/bots/${encodeURIComponent(botId)}/files`),
  readFile: (botId: string, path: string) => request<{ content?: string; binary?: boolean }>(`/api/bots/${encodeURIComponent(botId)}/file?path=${encodeURIComponent(path)}`),
  reveal: (botId: string, path = "") => request<void>(`/api/bots/${encodeURIComponent(botId)}/reveal`, json("POST", { path })),
  upload: (botId: string, file: File) => {
    const form = new FormData();
    form.append("file", file);
    return request<WorkspaceFile>(`/api/bots/${encodeURIComponent(botId)}/files`, { method: "POST", body: form });
  },
  setupBrowserExtension: (botId: string) => request<{ message: string }>(`/api/bots/${encodeURIComponent(botId)}/browser/extension`, json("POST", {})),
  takeOverBrowser: (botId: string) => request<{ held: boolean; message?: string }>(`/api/bots/${encodeURIComponent(botId)}/browser/takeover`, json("POST", {})),
  browserLive: (botId: string) => request<{ active: boolean; ws_url?: string; held: boolean; backend?: string }>(`/api/bots/${encodeURIComponent(botId)}/browser/live`),
  releaseBrowser: (botId: string) => request<{ held: boolean }>(`/api/bots/${encodeURIComponent(botId)}/browser/release`, json("POST", {})),

  conversations: (botId?: string) => request<Conversation[]>(`/api/conversations${botId ? `?bot_id=${encodeURIComponent(botId)}` : ""}`),
  createConversation: (botId: string, title = "") => request<Conversation>("/api/conversations", json("POST", { bot_id: botId, title })),
  conversation: (id: string) => request<ConversationDetail>(`/api/conversations/${id}`),
  renameConversation: (id: string, title: string) => request(`/api/conversations/${id}`, json("PATCH", { title })),
  startFresh: (id: string) => request<Conversation>(`/api/conversations/${id}/fresh`, json("POST")),
  deleteConversation: (id: string) => request<void>(`/api/conversations/${id}`, json("DELETE")),
  send: (id: string, content: string) => request<{ run: Run; turn: Turn }>(`/api/conversations/${id}/messages`, json("POST", { content })),
  editMessage: (id: string, turnId: string, content: string) => request<{ run: Run; turn: Turn }>(`/api/conversations/${id}/turns/${turnId}/edit`, json("POST", { content })),
  runSteering: (id: string, runId: string) => request<RunSteeringCommand[]>(`/api/conversations/${id}/runs/${encodeURIComponent(runId)}/steering`),
  steerRun: (id: string, runId: string, command: { command_id: string; expected_revision: number; content: string }) =>
    request<RunSteeringCommand>(`/api/conversations/${id}/runs/${encodeURIComponent(runId)}/steering`, json("POST", command)),
  respond: (runId: string, interactionId: string, intent: string, content = "") => request<Run>(`/api/runs/${runId}/interactions/${interactionId}`, json("POST", { intent, content })),
  cancel: (runId: string) => request<Run>(`/api/runs/${encodeURIComponent(runId)}/cancel`, json("POST", {})),
  timeline: (runId: string, after = 0) => request<{ events: RunEventEnvelope[]; next_sequence_no: number }>(`/api/runs/${encodeURIComponent(runId)}/timeline?after_sequence=${after}`),

  memory: () => request<MemoryOverview>("/api/memory"),
  memorySources: (bank: string) => request<MemorySource[]>(`/api/memory/banks/${encodeURIComponent(bank)}`),
  memoryLearning: (bank: string) => request<MemoryLearning>(`/api/memory/banks/${encodeURIComponent(bank)}/learning`),
  historySearch: (bank: string, query: string, cursor = 0) => request<HistoryPage>(`/api/memory/banks/${encodeURIComponent(bank)}/history/search`, json("POST", { query, cursor })),
  historyRead: (bank: string, source_id: string, offset = 0) => request<HistoryExcerpt>(`/api/memory/banks/${encodeURIComponent(bank)}/history/read`, json("POST", { source_id, offset })),
  consolidateMemory: (bank: string) => request(`/api/memory/banks/${encodeURIComponent(bank)}/consolidate`, json("POST", {})),
  addMemoryNote: (bank: string, content: string, replaces = "") => request(`/api/memory/banks/${encodeURIComponent(bank)}/notes`, json("POST", { content, replaces })),
  forget: (bank: string, documentId: string) => request<void>(`/api/memory/banks/${encodeURIComponent(bank)}/forget`, json("POST", { document_id: documentId })),
  recall: (bank: string, query: string) => request<{ results: { text: string; document_id: string; score: number }[] | null }>(`/api/memory/banks/${encodeURIComponent(bank)}/recall`, json("POST", { query })),
  retryMemory: (turnId: string) => request<void>(`/api/memory/turns/${turnId}/retry`, json("POST", {})),

  skills: () => request<Skill[]>("/api/skills"),
  saveSkill: (skill: Skill) => request<Skill>(skill.id ? `/api/skills/${skill.id}` : "/api/skills", json(skill.id ? "PUT" : "POST", skill)),
  deleteSkill: (id: string) => request<void>(`/api/skills/${id}`, json("DELETE")),
  importSkill: (file: File) => request<Skill>("/api/skills/import", { method: "POST", body: file, headers: { "Content-Type": "application/zip" } }),
  revealSkills: () => request<void>("/api/skills/reveal", json("POST", {})),
  skillSources: () => request<SkillSource[]>("/api/skill-sources"),
  addSkillSource: (repo: string) => request<SkillSource>("/api/skill-sources", json("POST", { repo })),
  setSkillSource: (id: string, enabled: boolean) => request<SkillSource>(`/api/skill-sources/${encodeURIComponent(id)}`, json("PATCH", { enabled })),
  removeSkillSource: (id: string) => request<void>(`/api/skill-sources/${encodeURIComponent(id)}`, json("DELETE")),
  refreshSkillSource: (id: string) => request<SkillSource>(`/api/skill-sources/${encodeURIComponent(id)}/refresh`, json("POST", {})),
  catalog: (query: string) => request<CatalogSkill[]>(`/api/catalog?q=${encodeURIComponent(query)}`),
  catalogPreview: (id: string) => request<{ content: string }>(`/api/catalog/preview?id=${encodeURIComponent(id)}`),
  installCatalogSkill: (id: string, botId = "") => request<Skill>("/api/catalog/install", json("POST", { id, bot_id: botId })),

  routines: () => request<Routine[]>("/api/routines"),
  saveRoutine: (routine: Partial<Routine>) => request<Routine>(routine.id ? `/api/routines/${routine.id}` : "/api/routines", json(routine.id ? "PUT" : "POST", routine)),
  deleteRoutine: (id: string) => request(`/api/routines/${id}`, json("DELETE")),
  runRoutine: (id: string) => request<RoutineRun>(`/api/routines/${id}/run`, json("POST", {})),
  previewSchedule: (schedule: Partial<Routine>) => request<{ next_runs: string[]; error?: string }>("/api/routines/preview", json("POST", schedule)),
  answerMissed: (runId: string, action: "run" | "skip") => request<void>(`/api/routine-runs/${runId}/missed`, json("POST", { action })),
  routineRuns: (id: string) => request<RoutineRun[]>(`/api/routines/${id}/runs`),

  connections: () => request<MCPConnection[]>("/api/connections"),
  startSignIn: (id: string) => request<{ url: string }>(`/api/connections/${id}/oauth/start`, json("POST", {})),
  signOut: (id: string) => request<void>(`/api/connections/${id}/oauth/signout`, json("POST", {})),
  saveConnection: (connection: Partial<MCPConnection>) => request<MCPConnection>(connection.id ? `/api/connections/${connection.id}` : "/api/connections", json(connection.id ? "PUT" : "POST", connection)),
  google: () => request<GoogleStatus>("/api/google"),
  saveGoogleClient: (client: { client_id: string; client_secret?: string }) => request<GoogleStatus>("/api/google", json("PUT", client)),
  deleteConnection: (id: string) => request(`/api/connections/${id}`, json("DELETE")),
  browsers: () => request<{ name: string; path: string }[]>("/api/browsers"),
  revealLogs: () => request<void>("/api/logs/reveal", json("POST", {})),
};
