export type Capabilities = {
  files_read: boolean;
  files_write: boolean;
  shell: boolean;
  web: boolean;
  browser: boolean;
  memory: boolean;
};

export type Provider = "local" | "anthropic" | "openai" | "openrouter" | "chatgpt";

export type Bot = {
  id: string;
  name: string;
  emoji: string;
  color: string;
  instructions: string;
  provider: Provider;
  model: string;
  context_window?: number;
  capabilities: Capabilities;
  browser_backend?: "" | "standard" | "extension" | "cloud";
  approval_mode: "ask" | "auto" | "allow_all";
  workspace_dir: string;
  memory_mode: "private" | "shared" | "off";
  skills: string[];
  mcp_connections: string[];
  position: number;
  created_at?: string;
  updated_at?: string;
  // True while a new bot interviews the user and configures itself.
  setup?: boolean;
  // "main" is Rex itself, the bot that hands work to the others.
  role?: string;
  // Stops the bot from looking back at its tasks to suggest improvements.
  retro_off?: boolean;
};

export type Status = {
  status: string;
  runtime_ready: boolean;
  /** Set while a new bot engine waits for the previous one to exit. */
  runtime_waiting?: string;
  data_dir: string;
  workspace_root: string;
  memory: { available: boolean; status: string };
  onboarded: boolean;
  active_runs: number;
  write_tools: boolean;
  // True when a web search key is set, so bots can use web_search.
  web_search?: boolean;
  // True when bots browse in Kernel's cloud, which can't be taken over.
  cloud_browsers?: boolean;
  // The Mac account's name, for the profile button.
  user_name?: string;
};

export type ProviderConfig = { base_url: string; configured: boolean; api_key?: string; clear_key?: boolean };

/** The ChatGPT subscription sign-in; pending holds the code to enter. */
export type ChatGPTStatus = { connected: boolean; expires_at?: string; pending?: { verification_url: string; user_code: string; expires_at: string; interval_seconds: number } };

export type Settings = {
  local: ProviderConfig;
  anthropic: ProviderConfig;
  openai: ProviderConfig;
  openrouter: ProviderConfig;
  default_provider: Provider;
  chatgpt?: ChatGPTStatus;
  kernel?: ProviderConfig;
  kernel_browser?: boolean;
  default_model: string;
  memory: { enabled: boolean; provider: "local" | "openai"; embedding_model: string; embedding_source?: "" | "bundled" | "server" };
  browser_enabled: boolean;
  browser_path: string;
  browser_hidden: boolean;
  typesafe: ProviderConfig;
  tinyfish: ProviderConfig;
  exa: ProviderConfig;
  brave: ProviderConfig;
  typesafe_auto_approve: boolean;
  notifications_off: boolean;
  onboarded: boolean;
};

export type ModelInfo = {
  id: string; embedding: boolean;
  // Details the provider gives, where it gives them (OpenRouter most).
  name?: string; context?: number; vision?: boolean;
  // False only when the provider says the model can't call tools.
  tools?: boolean;
  // US dollars per million input and output tokens.
  price_in?: number; price_out?: number;
};

export type ProviderTest = { ok: boolean; reachable: boolean; tool_calls: boolean; models: number; message: string; latency_ms: number };

export type Conversation = {
  id: string;
  bot_id: string;
  title: string;
  kind: "chat" | "routine";
  active_run_id?: string;
  created_at: string;
  updated_at: string;
  context_start?: string;
  // The bot list's view of the thread: its latest message.
  preview?: string;
  preview_from?: "user" | "bot";
  last_at?: string;
  working?: boolean;
};

export type Run = {
  id: string;
  status: string;
  pause_reason?: string;
  error_message?: string;
  created_at: string;
  updated_at: string;
};

export type Message = {
  id: string;
  run_id: string;
  role: string;
  message_type?: string;
  content: string;
  created_at: string;
};

export type Interaction = {
  id: string;
  run_id: string;
  interaction_kind: string;
  status: string;
  title?: string;
  summary?: string;
  request_payload?: Record<string, unknown>;
  created_at?: string;
};

export type Turn = {
  id: string;
  conversation_id: string;
  bot_id: string;
  run_id: string;
  user_text: string;
  assistant_text: string;
  status: "working" | "settled" | "failed" | "cancelled";
  started_at: string;
  settled_at?: string;
  memory_bank: string;
  memory_status: string;
  memory_error?: string;
  memory_facts: number;
  start_sequence: number;
  // Starts a new part of the thread: an earlier conversation's title,
  // "Fresh start", or a routine.
  section?: string;
  // Set when Rex wrote the message: "rex" for a task the main bot handed
  // over, "report:<bot>" for a bot reporting back to the main bot.
  origin?: string;
  delegated_by?: string;
  // The memories the bot was given for this turn and where they came from.
  recalled?: { text: string; bank: string }[];
};

export type ConversationDetail = {
  conversation: Conversation;
  runs: Run[];
  messages: Message[];
  interactions: Interaction[];
  turns: Turn[];
};

export type RunEventEnvelope = {
  event_id?: string;
  sent_at?: string;
  sequence_no?: number;
  app_id: string;
  run_id: string;
  host_run_id?: string;
  type: string;
  data?: Record<string, unknown>;
};

export type RunAssistantSegment = {
  kind: "assistant";
  id: string;
  message_id: string;
  content: string;
  status: "started" | "streaming" | "completed";
  started_at: string;
  completed_at?: string;
  order: number;
  display_phase?: "thought" | "final";
};

export type RunReasoningSegment = {
  kind: "reasoning";
  id: string;
  message_id: string;
  content: string;
  status: "started" | "streaming" | "completed";
  started_at: string;
  completed_at?: string;
  order: number;
};

export type RunToolCall = {
  kind: "tool";
  id: string;
  parent_message_id?: string;
  tool_name: string;
  args_text: string;
  result: string;
  error: string;
  duration_ms?: number;
  status: "running" | "completed" | "failed";
  started_at: string;
  completed_at?: string;
  order: number;
};

export type RunActivity = { kind: "activity"; id: string; content: string; started_at: string; order: number };
export type RunPlanStep = { step: string; status: string };
export type RunPlanSegment = { kind: "plan"; id: string; steps: RunPlanStep[]; note: string; started_at: string; order: number };
export type RunTimelineSegment = RunAssistantSegment | RunReasoningSegment | RunToolCall | RunActivity | RunPlanSegment;

export type RunStreamState = {
  assistant: RunAssistantSegment[];
  reasoning: RunReasoningSegment[];
  tools: RunToolCall[];
  activities: RunActivity[];
  plan: RunPlanStep[];
  plan_note: string;
  timeline: RunTimelineSegment[];
  run_status?: string;
  pause_reason?: string;
  error?: string;
  seen_event_ids: string[];
  event_count: number;
};

export type MemoryFact = { id: string; text: string; type: string; entities: string[]; mentioned_at: string; replaced?: boolean; evidence_kind?: string; verified?: boolean; source_ids?: string[] };
export type MemorySource = {
  document_id: string;
  kind: "chat" | "note" | "bot" | "episode";
  title: string;
  excerpt: string;
  conversation_id?: string;
  status: string;
  error?: string;
  created_at: string;
  facts: MemoryFact[] | null;
};
export type MemoryBank = { bank: string; label: string; bot_ids: string[] };
export type MemoryOverview = { available: boolean; status: string; banks: MemoryBank[] };

// A skill folder: SKILL.md (body, front matter included) and its files.
export type SkillOrigin = { repo: string; commit: string; dir: string; catalog_id: string; installed_at: string };
export type Skill = { id: string; title: string; description?: string; body: string; files?: string[]; version?: string; updated_at?: string; source?: SkillOrigin };

// The skill catalog: GitHub collections Rex indexes and installs from.
export type SkillSource = { id: string; repo: string; enabled: boolean; builtin: boolean; note: string; stars: number; license: string; commit: string; skill_count: number; indexed_at?: string; error?: string; description: string };
export type CatalogSkill = { id: string; source_id: string; repo: string; dir: string; name: string; description: string; license?: string; has_scripts: boolean; files: string[]; commit: string; url: string; installed?: string };

export type Routine = {
  id: string;
  bot_id: string;
  title: string;
  prompt: string;
  schedule_kind: "interval" | "daily" | "cron";
  interval_minutes: number;
  daily_time: string;
  // Five-field cron expression in local time, for schedule_kind "cron".
  cron?: string;
  enabled: boolean;
  conversation_id: string;
  next_run_at?: string;
  last_run_at?: string;
  consecutive_failures: number;
  paused_reason?: string;
  retry_at?: string;
};

export type RoutineRun = {
  id: string;
  routine_id: string;
  scheduled_for: string;
  attempt: number;
  status: string;
  run_id?: string;
  error?: string;
  created_at: string;
  finished_at?: string;
};

export type MCPTool = { name: string; description: string; access: "read" | "write"; enabled: boolean };
export type MCPConnection = {
  id: string;
  name: string;
  url: string;
  auth: "none" | "bearer" | "oauth" | "google";
  token?: string;
  clear_token?: boolean;
  has_token: boolean;
  // True when an OAuth connection is signed in.
  signed_in?: boolean;
  // The signed-in address, for Google apps.
  account?: string;
  tools: MCPTool[];
  status: string;
};

// Whether Rex can sign in to Google: with a client the user added
// ("settings") or one built into Rex.
export type GoogleStatus = { configured: boolean; source: "settings" | "built-in" | ""; client_id?: string };

export type WorkspaceFile = { path: string; size: number; modified: string };

export type AttentionItem = {
  id: string;
  kind: "approval" | "question" | "reconnect" | "routine" | "missed" | "proposal";
  conversation_id: string;
  bot_id: string;
  title: string;
  detail?: string;
  at: string;
};

// New instructions a bot proposed for itself, waiting for the user.
// source "retrospective": the bot suggested it after looking back at recent tasks.
export type InstructionProposal = { id: string; bot_id: string; instructions: string; summary: string; source?: string; reason?: string; created_at: string; kind?: "instructions" | "install_skill" | "connect_mcp"; app?: { id: string; name: string; description: string; url: string; auth: string; access?: string; tokenHint?: string }; evidence?: { sources?: string[]; gap?: string; memory_remedy?: string; memory_insufficient?: string; capability_remedy?: string; capability_insufficient?: string; documentation?: string[]; access?: string; setup_cost?: string; expected?: string }; catalog_id?: string; skill?: CatalogSkill };

// A site a bot may sign in to. Passwords never reach the app or the bot.
export type BotLogin = { id: string; bot_id: string; site: string; username: string; has_2fa: boolean; created_at: string; last_used_at?: string };

export type TaskEpisode = { turn_id: string; status: string; forgotten: boolean; facts: number; model?: string; error?: string; created_at: string; updated_at: string; attempts: number; retry_at?: string };
export type OperationalLesson = { id: string; text: string; state: string; task_count: number; verified_tasks: number; source_fact_ids: string[]; source_ids?: string[]; updated_at: string; is_stale: boolean };
export type MemoryLearning = { jobs?: { kind: string; state: string }[]; episodes: TaskEpisode[]; counts: Record<string, number>; lessons: OperationalLesson[]; available: boolean; model: string; consolidation: { last_success?: string; retry_at?: string; error?: string; attempts: number }; context: { enabled: boolean; context_window?: number; trigger_tokens?: number; warning?: string; model?: string } };
export type HistoryEntry = { source_id: string; turn_id: string; kind: string; text: string; created_at: string; truncated?: boolean };
export type HistoryPage = { entries: HistoryEntry[]; next_cursor?: number };
export type HistoryExcerpt = HistoryEntry & { offset: number; truncated: boolean; next_offset?: number };

export type CommandSandboxStatus = { supported: boolean; state: string; message: string; busy: boolean };

/** A runtime work job: one contract run over a list of items. */
export type WorkJobCounts = { total: number; pending: number; running: number; retry_wait: number; blocked: number; completed: number; found: number; no_result: number; exceptions: number; failed: number; cancelled: number };
export type WorkJob = {
  id: string;
  state: "queued" | "running" | "waiting" | "paused" | "verifying" | "completed" | "failed" | "cancelled";
  waiting_reason?: string;
  blocked_run_id?: string;
  error?: string;
  spec: { objective: string; batch_size: number; max_concurrency: number };
  counts: WorkJobCounts;
  usage: { input_tokens: number; output_tokens: number; cached_input_tokens: number; reserved_tokens: number };
  active_runs: string[];
  dataset_revision: number;
  created_at: string;
  updated_at: string;
  completed_at?: string;
};
/** Rex's record of a job plus the engine's live view. */
export type JobView = {
  id: string;
  bot_id: string;
  conversation_id: string;
  title: string;
  state: string;
  dataset_path?: string;
  created_at: string;
  updated_at: string;
  job?: WorkJob;
  /** Items workers are on right now. */
  current: { id: string; label: string }[];
  /** Running workers: their batch and latest steps, newest first. */
  workers?: JobWorker[];
  items_per_minute: number;
  /** A limit the bot's browser mode puts on workers. */
  browser_note?: string;
  /** Questions and approvals workers are waiting on, per worker run. */
  pending?: { run_id: string; interaction: Interaction }[];
  error?: string;
};
export type JobWorker = {
  run_id: string;
  started_at: string;
  items: { id: string; label: string }[];
  done: number;
  steps: { tool_name: string; args_text: string; status: string; error?: string; created_at: string }[];
};
export type WorkItem = {
  id: string;
  state: string;
  input: unknown;
  attempts: number;
  outcome?: "completed" | "no_result" | "exception";
  result?: unknown;
  exception?: { kind: string; detail: string };
  run_id?: string;
  last_error?: string;
  updated_at: string;
};
export type WorkItemPage = { items: WorkItem[]; next_after?: string };
