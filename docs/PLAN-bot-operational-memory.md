# Rex bot operational memory and retrospective learning

Status: implemented locally on 2026-10-13 on `feat/bot-operational-memory`.
The companion runtime changes are on `feat/operational-memory-context` in
`/private/tmp/rex-operational-runtime`. Rex builds through an ignored `go.work`;
release those generic runtime changes and update Rex's dependency pin before
shipping or building this branch without the local workspace. The rebuilt desktop
app was restarted locally on 2026-10-13; live data was backed up and migrated,
and incremental task capture started. This extends [bot retrospectives](PLAN-retrospectives.md) and changes
the memory direction described in [the original plan](PLAN.md).

Rex bots need to remember their work: what they attempted, what actually happened,
what helped, what failed, and what they learned. Personal preference extraction
is not the default purpose of this system. Existing saved memories remain
available during migration, but new automatic learning focuses on bot operations.

All recorded history remains available through scoped search and reading tools.
The model receives a bounded working context and can retrieve older evidence when
needed. Memory consolidation and conversation compaction create useful derived
records; neither operation deletes the original history.

## Local implementation and checks

The implementation adds durable episode capture and retries, scoped history
search/read, operational extraction, proof-aware lesson states and version
snapshots, idle consolidation with persisted cooldown/backoff, and a single
serialized learning lane. Local chat can interrupt model learning; capture and
history lookup continue independently. Failed and cancelled tasks are included.
Older terminal turns are incrementally reconciled in bounded batches.

Extraction, explicit-note conflict checks and private-bank consolidation now
use the owning bot's model and provider through runtime helper runs with no
tools, MCPs or skills. Shared-task extraction uses its originating bot; shared
bank consolidation uses Rex's model. Default model resolution, credentials and
ChatGPT OAuth follow the ordinary bot-run path. The separate learning-model
setting is removed. Embeddings remain independent. Small model windows receive
bounded evidence; a provider or model failure is surfaced rather than switching
to another model. Learning waits for active chats and reviews and is cancellable.
The rebuilt app was restarted while idle. Live verification showed Lead
Researcher and the newsletter bot selecting `gpt-5.6-sol` through ChatGPT OAuth,
with two completed newsletter learning helper runs on that provider/model.
Scribe alone continues to use its configured `openai/gpt-oss-20b` model. Existing
facts retain their historical model provenance; this change does not discard
or automatically reinterpret them.

History reads now consult the saved index directly. A separate background worker
captures task history even while the learning model is busy, refreshing active
snapshots about every 30 seconds. Updates write only changed source entries; the
search index uses matching SQLite row IDs and transactional triggers. Capture
loads known passwords once per batch before redacting and saving evidence. Read
pages can grow to 16,000 characters when the verified model context permits it.
Regression checks cover migration, scoping, redaction, active-task scheduling and
a 40,000-row archive with a 1,200-entry snapshot; a three-row delta took about 2 ms
in the local synthetic test. These are local test timings, not a production SLA.
After an idle restart with the corrected build, live checks against 41,599
recorded entries measured source reads at 1–2 ms, whole-task reads at 34–43 ms,
and search at about 4 ms. The SQLite index upgrade created a backup first. The
companion bounded ChatGPT transport retry and historical failure display fixes
were activated in the same build.

Retrospectives review memory and current capabilities before proposing a change.
Instruction proposals require an explicit lasting work contract or repeated
failures after the same supported lesson was recalled. MCP proposals hand off to
Connect apps. No review installs skills, connects servers or edits instructions.
The memory panel shows saved tasks, candidate/supported lessons, model health,
compaction status, learning errors and bounded source readers. Existing facts
and explicit notes remain available.

Local validation uses fixture tests and an isolated sidecar smoke test in
`scripts/e2e-operational-memory.py`. The smoke test runs real file tools and the
runtime extraction/consolidation path, using one deterministic model by default;
it verifies restart and forgetting without touching live bot profiles. An
explicit real task model can be selected through the existing e2e environment.
ChatGPT metadata discovery was separately verified against the configured
account: `gpt-6.1-sol` reported a 500,000-token context.

The available local GPT-OSS model reported a loaded context of only 4,096 tokens.
That is too small for the tested bot's fixed prompt and file tools. Rex now
respects that limit and reports the overflow rather than guessing a larger
window. Load/configure a larger actual context to run that model with those
tools. Unknown limits remain visible and require verified configuration.

Forgetting starts a new working context for bots using the affected bank and
cancels any active run carrying that context. Recorded chat remains visible;
scoped retrieval, derived lessons and old lesson versions exclude the source.
SQLite upgrades create a consistent backup before applying the new migration.
The rebuilt desktop app is running locally with the migrated live data. Startup
verification confirmed both services ready and the first five historical tasks
captured. Historical backfill continues in bounded background batches.

## Current problems

The investigation on 2026-10-13 found 118 saved facts across 11 bot banks. Of 145
turns processed for memory, 76 produced zero facts. Memory was available and had
no current failed-extraction backlog. These counts are a snapshot of an active
application, not a target for the new system.

| Problem | Current implementation | Required change |
|---|---|---|
| Narrow learning | `memory.go` extracts only the user's current message and asks for personal facts | Capture task evidence and derive operational lessons |
| Lost failures | Failed and cancelled turns are skipped | Preserve outcomes, attempted actions and unresolved work |
| Missing context | Extraction receives one message; new runs carry only a limited recent transcript | Resolve corrections against their task and expose searchable recorded history |
| Inactive consolidation | `MemoryService.attach` exists but is never called; the worker requires `m.host != nil` | Attach the host and test the actual idle scheduling path |
| Inactive chat compaction | Rex sends no `native_context` policy | Resolve model limits and enable the runtime's bounded compaction |
| Extraction model mismatch | Memory uses its configured endpoint; hosted bots can fall back to the first local bot's model | Make the chosen learning model explicit and observable |
| Prompt focused retrospectives | Look backs mainly produce instruction or skill proposals from eight recent turns | Review memory, evidence and capabilities first; use older history when relevant |

Relevant code is in `internal/rex/memory.go`, `extractor.go`, `memory_router.go`,
`host.go`, `threads.go`, `callbacks.go`, `retro.go`, `self.go` and `bots.go`.
Agent-runtime v0.13.12 already supplies durable run history, checkpoints,
`experience` memory facts, source-backed observations and native compaction.
Reuse those components rather than introduce another vector database or daemon.

## What the bot remembers

Use three connected layers, with different purposes and lifetimes.

| Layer | Contents | Example | How it is used |
|---|---|---|---|
| Recorded history | Messages, recorded tool calls and results, run events, interactions and artifact references | A failed navigation followed by a successful retry | Search and read exact evidence |
| Task episodes | Request, relevant requirements, attempts, outcomes, checkpoints, produced artifacts and unresolved work | Batch 03 processed 120 rows; 17 remain unverified | Continue work and avoid duplicate actions |
| Operational lessons | Supported success patterns, failure patterns and scoped procedures derived from episodes | When a listing navigation invalidates DOM references, read a fresh snapshot before continuing | Improve the next relevant task |

Record every terminal task episode, including tasks with no reusable lesson.
Store all observed successes and failures as evidence. A single incident can
remain useful without becoming a general rule. Routine runs and tasks delegated
by Rex are included, with their origin recorded; a delegated instruction is not
misrepresented as a direct user statement.

Keep temporary incident state separate from a durable procedure. A service
timeout is an incident with a time and environment, not proof that the service
should never be used again. A user correction about how this bot performs its
job is operational evidence, even when it is phrased as a question or is short.

Do not add automatic personal profiling. Preserve existing personal facts and
explicit notes as legacy sources, and keep user controls for viewing and
forgetting them. Operational memory must not grant permissions, override the
current request, or silently change the bot's instructions.

## Capture task evidence before model extraction

On settlement, failure or cancellation, persist an episode capture job before
starting any model work. Chat completion must not wait for extraction.

1. Identify the task with the existing bot, conversation, turn and run IDs. Use
   message and tool sequence boundaries to distinguish turns sharing a run.
2. Record its terminal status, origin, request, relevant task requirements and
   artifact references. Record partial completion and open work explicitly.
3. Fetch recorded evidence through `RuntimeClient` and runtime APIs. Rex must
   not make production code depend on reading runtime's SQLite schema directly.
4. Separate tool-confirmed outcomes, user confirmation, assistant claims and
   inferred explanations. A successful tool call alone does not prove the
   whole task met its acceptance criteria.
5. Record cancelled work as interrupted. Preserve verified completed actions;
   never infer that unobserved actions succeeded or that cancellation means
   the user disliked the approach.
6. Extract lessons asynchronously from this evidence. Preserve links to the
   exact messages, calls and artifacts supporting each lesson.

The episode is available for history lookup even when extraction fails. Job
states distinguish captured, queued, extracting, indexed, no lesson, retrying
and failed. A retry or host restart must not repeat an external task action.

Use stable source IDs and revisions so a retry updates one episode instead of
creating duplicates. Add a durable job table in `rex.db`, reusing the existing
worker and notification mechanism. Include source revision, status, attempts,
next attempt time and last error. Recover interrupted jobs on startup and use
bounded retries with backoff; expose exhausted failures instead of hiding them.

At startup, reconcile terminal turns missing jobs, covering a crash between
settling a turn and enqueueing its memory work. A changed source revision
invalidates its old derived records before reprocessing.

## Operational extraction and evidence rules

Replace the personal-facts prompt for new task episodes with an operational
schema. Reuse runtime memory's `experience` fact type and metadata for source
references; do not invent another embedding format.

Extract these kinds of evidence:

- Completed work, produced artifacts, verified results and unfinished work.
- Approaches that succeeded, with the relevant conditions and environment.
- Failed approaches, observed symptoms and confirmed recovery steps.
- User corrections and accepted task decisions, linked to what was corrected.
- Capability gaps, such as an unavailable browser action, missing MCP tool,
  missing connection, inaccessible data or absent skill.
- Candidate explanations, clearly labelled as inference rather than a proven
  cause. For example, a stream ending unexpectedly does not establish why.

Each candidate needs a scope, source references, evidence kind, time and relevant
environment. Add backend, tool or skill version only when it affects the lesson.
The host supplies ownership and status; the model cannot choose another bot's
bank or declare an unsuccessful action verified.

Never treat retrieved memories, a compaction summary or an old transcript
embedded in a prompt as new independent evidence. Trace those statements back
to their original sources. Exclude memory recall and history-reading results
from new proof counts unless a new action independently confirms the result.

Validate the extraction schema and referenced source IDs before retaining facts.
Reject unsupported claims and keep the episode with an extraction error when
the model returns invalid output. Redact credentials and saved passwords using
the existing helpers. External page text and MCP results remain untrusted data.

Configure a dedicated learning model or explicitly use a compatible bot model.
Keep the existing simple-schema fallback, adapted to operational evidence.
Record the actual model, fallback, duration and error. Never silently send a
hosted model identifier to an incompatible local endpoint. Initially allow one
learning model job at a time; local chat takes priority when both need the same
model server. Capture and history indexing continue while model work waits.

## Full recorded history through bounded retrieval

Replace the latest-four-runs assumption with a searchable index of all recorded
history for the bot. Keep bounded recent carry-forward context for continuity;
the index provides access to older work without appending every transcript to
every model call.

Add two tools to the existing scoped memory MCP server:

| Tool | Inputs | Output |
|---|---|---|
| `history_search` | Query, optional time or conversation filters, bounded page size and cursor | Matching task or message excerpts with stable source IDs and outcome status |
| `history_read` | Source ID, optional range and cursor | Bounded recorded messages, calls or artifact references, with neighbouring context and a continuation cursor |

The host derives the bot and app scope from the authenticated connection. Check
source ownership on every read; model-supplied IDs do not grant access. Access
to another bot's history follows existing delegation and sharing boundaries.
Rex's main bot receives an authorized high-level task summary, not automatic
access to all private transcripts.

Start with a SQLite FTS5 index in `rex.db`, using existing runtime APIs to read
the source records. Reuse memory embeddings for semantic episode and lesson
recall. Index terminal tasks, update revisions and incrementally backfill older
recorded runs. Keep recent unfinished work directly addressable even before its
learning extraction completes. Limit pages by bytes and tokens, not just rows.

Return whether an excerpt or original tool result was truncated. A missing or
deleted artifact is shown as unavailable; the bot must not invent its contents.
Only history Rex actually recorded can be recovered. Large files and artifacts
stay behind scoped readers rather than being copied into each memory record.

## Assemble useful context before each task

Build a small context packet from the current request, active project state and
relevant prior work. Retrieval should use task identity and recent work as well
as the latest user text, so requests such as "continue" or "next batch" are
resolved against the task they continue.

Include unfinished work and verified last outcomes first, then related failure
and success lessons with their conditions and source links. Relevant recent
corrections should not disappear merely because they scored below an arbitrary
top-eight cutoff. Rank and deduplicate within a token budget, and expose the
history tools for additional evidence.

Initially target up to 4,000 tokens for recalled operational context, reduced
when the model's input budget is small. Treat this as a tested starting default,
not permission to exceed the resolved model window. Include counts and cursors
when more evidence exists. Current task requirements remain authoritative;
retrieved history and lessons are reference data.

The UI must distinguish saved episodes, reusable lessons and records recalled
for the current task. A low recall count is not a low saved-memory count.

## Enable conversation compaction with real model limits

Enable runtime `native_context` for normal task runs and retrospective runs.
Resolve limits from the actual model selected for the run, including an empty
bot model that resolves to a default. Use available provider or local-server
metadata and a verified mapping where a provider does not expose limits.
Unknown limits require explicit configuration; do not guess a universal window.

Reserve output tokens and safety headroom before defining the input budget.
Use the runtime's existing bounded defaults as the starting policy: a trigger
of at most 64,000 estimated input tokens, up to 16,000 recent tokens and up to
4,000 summary tokens, scaled down for smaller windows. Configure a bounded
anchor for current user requirements and validate all budgets together.

Freeze the resolved policy for the run and preserve it through continuation,
checkpoint restore and runtime restart. If this needs a generic runtime change,
implement it in agent-runtime and release it before Rex pins that version.
Keep summarization and checkpoint ownership in runtime.

Compaction retains the current request, approvals, constraints, unfinished work,
recent tool-call/result groups and source references. Older work becomes a
summary pointing to the archive. Record before and after estimates, generation,
cost and failure status. Preserve the original context when summarization fails;
use the existing bounded overflow recovery rather than silently dropping history.

"Start fresh" still starts a clean working context. Older records remain
available through explicit history search unless the user deletes or forgets
their sources. Resuming a compacted run must not replay completed actions.

## Memory consolidation procedure

Attach `MemoryService` to its host in `NewHost`, then consolidate operational
facts using runtime's existing source-backed observation storage. Consolidation
merges related evidence into useful lessons; it does not reduce the raw history
or erase source facts.

| Stage | Procedure |
|---|---|
| Select | Choose unprocessed evidence for one bot, with a stable watermark and a bounded batch of up to 25 facts |
| Group | Find related existing lessons; group by task, procedure and relevant environment |
| Evaluate | Separate independent supporting outcomes, counterexamples, user corrections and inferred causes |
| Consolidate | Create or update a scoped observation; merge duplicates, preserve limitations and supersede obsolete advice |
| Validate | Check proof IDs, ownership, source revisions, forgotten state and consistency with observed outcomes |
| Commit | Atomically store the derived records and processing watermark, using runtime's existing bank-state conflict checks |
| Review | Feed changed lessons and unresolved gaps to the retrospective queue |

A repeated retry in one task is not several independent successes or failures.
Count distinct task episodes. Duplicate messages, resumed runs, copied reports
and recalled memories do not increase support. Apply these rules in host
validation rather than accepting a model-generated confidence score.

Keep a single observed incident as a candidate lesson with its evidence. Promote
a reusable pattern after at least two independent verified outcomes, or one
explicit user correction or accepted decision that establishes how this bot
should perform the task. This is an initial promotion rule to validate against
fixtures. Tool confirmations establish only what they actually checked.

Contradictory evidence can narrow, downgrade or supersede a lesson. Keep links to
the earlier version and the counterexample. A lesson is a scoped claim such as
"this recovery worked for this browser backend", not a global claim that every
similar failure has the same cause. Transient service incidents expire from
active advice when no longer relevant; their history remains searchable.

Examples of the expected classification:

| Evidence | Record or lesson | Next use |
|---|---|---|
| A batch manifest confirms completed rows and unresolved rows | An episode with exact progress and the manifest reference | Resume the remaining work instead of repeating the batch |
| Independent browser tasks recover stale element errors by reading a fresh snapshot | A procedure scoped to that backend and navigation behaviour | Refresh references when the same condition occurs |
| One stream fails with incomplete JSON and no confirmed cause | An incident with an unknown cause | Find related evidence before assuming a model or browser fault |
| The user rejects a report that omitted required sources | A correction linked to the request and report | Recall the requirement and check source coverage before delivery |
| Repeated tasks require a tool absent from the connected MCPs | A capability gap with evidence | Research and propose a suitable connection rather than rewrite instructions |

Use existing observations for lesson text and proof IDs. Add only the host
annotations needed to track lesson state, distinct-task support and review
status, keyed to the existing observation. Inspect the current public runtime
API before introducing a table or extending its schema.

## Consolidation schedule and recovery

Use one durable learning queue shared with memory work and retrospective review,
with distinct job kinds. Reuse the existing worker instead of creating a second
uncoordinated consolidation loop.

- Every terminal task immediately records its episode and queues extraction.
- Every successful extraction makes its bot eligible for consolidation.
- Run model consolidation after no task runs are active and Rex has been idle
  for three minutes. A worker checks every 30 seconds; enqueueing can wake it.
- A full batch of 25 new facts makes a bot due promptly. Smaller batches can
  consolidate on the next eligible idle pass; do not wait forever for 25 facts.
- Preserve a 30-minute cooldown per bot after a successful consolidation.
  Persist watermarks and cooldowns across restart. A failed attempt must not
  consume the successful-pass cooldown.
- Service bots fairly, with one bounded model job at a time. Start with a
  two-minute deadline per consolidation call. Recheck active task status before
  starting each job, and yield when the model server is needed for chat.
- Bound extraction work per cycle as well. Recheck due consolidation and review
  jobs between batches rather than drain an endless extraction queue first.
- Persist retry state with bounded backoff, and show pending age and exhausted
  failures. A model or configuration change can make failed jobs eligible again.
- On the first eligible idle pass each day, check for unprocessed evidence and
  overdue review. This is a backlog check, not a mandatory full-bank rewrite.
- Provide "Consolidate now" through the existing endpoint, with progress and
  the same source validation. An idle app with no new evidence makes no call.

For continuous operation, evidence still accumulates durably while model jobs
wait. Show that delay clearly. No transcript is discarded because Rex was busy,
closed, offline or interrupted during extraction.

## Retrospectives start with memory

Retrospectives become the review stage of operational learning. Reuse current
signal detection, the three-hour per-bot cooldown, periodic review after ten
tasks or a week with at least three tasks, and the existing proposal history.
Corrections, failures, repeated incidents and capability gaps can queue review
earlier. The "Look back now" action remains available.

Review the newest evidence watermark, related lessons, unresolved work and
previously applied or dismissed proposals. Use the full-history tools when a
pattern requires older evidence; eight recent turns are a starting excerpt,
not the complete accessible record. Share an evidence packet with consolidation
instead of asking two models to independently infer the same lessons.

Do not recursively queue retrospectives for retrospective or consolidation
runs. Their derived records are review results, not independent proof that a
task procedure succeeded. Operational task episodes remain the evidence base.

Evaluate each gap in this order:

1. **Missing or wrong memory.** Add evidence, reconcile a contradiction, narrow
   a lesson or consolidate related episodes.
2. **Retrieval or application failure.** Check whether the relevant lesson was
   retrieved and applicable. Fix a host recall defect as a product issue; a bot
   prompt rewrite cannot repair missing context or failed storage.
3. **Existing capability used poorly.** Retrieve a supported procedure, use an
   available tool or skill correctly, or request normal human takeover when
   that is the required step.
4. **Missing capability.** Research a relevant skill or MCP and propose enabling
   it when evidence shows it would close the gap.
5. **Instruction gap.** Propose a minimal bot instruction change only when
   memory, retrieval and available capabilities cannot close the demonstrated
   gap, or repeated evidence shows the bot cannot apply the retrieved lesson.

A successful retrospective can end with updated memory and no proposal. Report
what was learned or consolidated, with source links. It need not manufacture a
prompt change merely to produce an outcome.

Capture and memory consolidation continue when "Suggest improvements" is off,
provided operational memory is enabled. That switch controls proposals, not
whether enabled memory learns from completed tasks. Memory off disables model
learning and recall; recorded chat history follows existing history controls.

## Research skills and MCP capabilities from evidence

Use existing installed skills, the configured skill catalog, connected MCP tool
lists and the Apps catalog before recommending a new capability. A retrospective
gets bounded read-only discovery tools, not the bot's full task execution tools.
Where configured, permit web search and fetch for official capability and setup
documentation. Use generic capability queries rather than send private task
history to public search or prospective vendors.

For each candidate, check what the tool actually does, whether Rex can run it,
required authentication and permissions, overlap with existing capabilities and
material costs. Cite the documentation or catalog entry and record when it was
checked. Do not suggest a similarly named package without verifying it closes
the demonstrated gap. If discovery is unavailable, report that limitation rather
than claim the tool was researched.

Extend existing proposals with an MCP connection or tool-enablement kind where
needed. Each proposal includes:

- The repeated or concrete problem and its source episodes.
- Why current memory, tools and skills do not resolve it.
- The proposed skill or MCP, exact catalog or package identity, supported
  capability, documentation and required access.
- Expected behaviour after enabling it, and any user setup or cost.

Ask through Rex's existing proposal UI. A suggestion never installs code,
connects a server, grants permissions, shares history or changes credentials.
On acceptance, use the existing skill installation or Apps connection flow;
credentials stay in the normal credential manager. Authentication and required
approvals are still enforced by the host and runtime.

Deduplicate open proposals and respect dismissals. Retry a dismissed suggestion
only when new evidence or a meaningful capability change justifies it, and show
that reason. Distinguish a missing tool from an expired login or host bug.

## Prompt changes are the last resort

For a prompt proposal, require the retrospective to state the specific gap,
which memory and capability remedies were tried, and why they were insufficient.
Provide the smallest change with before and after behaviour and evidence.
Preserve existing instructions that are unrelated to the gap.

Routine procedures and incidental errors belong in memory or skills, not in an
ever-growing core prompt. Do not repeat lessons in the prompt solely because
they are important. Applied instruction or skill changes become versioned
evidence, so later review can compare similar task outcomes before and after.

"Core prompt" here means the bot's editable instructions. Rex's product-wide
operating rules and runtime permissions require a product code change; a
retrospective cannot modify them. All instruction changes remain user-approved.
Memory records themselves remain reference data, never an authorization layer.

## Forgetting and provenance

Keep source revision and forgetting checks across capture, extraction, recall,
history search, consolidation, summaries and retrospective proposals. Removing
a source invalidates its dependent lessons and derived summaries. Rebuild a
lesson only from the remaining valid evidence.

Persist tombstones so re-extraction, backfill and old assistant replies cannot
restore forgotten information. Automatically supplied history and model history
tools exclude forgotten sources. Human-visible chat can remain visible under
the existing memory-forgetting behaviour; deleting history is a separate action.
Rebuild affected carried context and summaries before the next model request.
Map legacy turn and note document IDs to canonical sources, so a new operational
document ID cannot bypass an existing forgetting tombstone during migration.

Do not broadly apply the current personal-fact "newer replaces older" test to
operational incidents. Two different task outcomes can both be valid. Supersede
a scoped lesson only when the supporting evidence establishes the change.

## Ownership and implementation scope

| Area | Owner and changes |
|---|---|
| Runtime history and checkpoints | Agent-runtime; keep originals, pagination and compaction durability |
| Context policy and summarization | Agent-runtime validates and executes; Rex resolves limits and supplies policy |
| Fact embeddings and observations | Reuse `agent-runtime/memory` and its proof validation |
| Episode capture and queue | Rex `store.go`, `host.go`, `memory.go` and a focused operational extraction module |
| History access and context assembly | Rex `RuntimeClient`, `callbacks.go`, `threads.go` and a scoped history index |
| Retrospective decisions | Rex `retro.go`, reusing catalog, self proposals, proposal history and Apps flows |
| User inspection | Existing memory panel, turn metadata, history views and proposal UI |

Expose source, evidence kind, lesson state, supporting and contradicting tasks,
last consolidation, pending jobs and actual learning model. Link each lesson to
readable episodes and each proposal to the gap it addresses. Show "captured",
"queued" and "learned" truthfully; accepting a memory note is not proof that
model extraction has finished.

## Migration and rollout

1. **Repair scheduling.** Attach the host, add a scheduling regression test and
   expose consolidation progress. Preserve current facts and source IDs.
2. **Repair working context.** Resolve model limits, enable and freeze native
   context policies, and verify compaction plus resume. Release any required
   generic agent-runtime changes before pinning them in Rex.
3. **Capture all task episodes.** Add durable jobs, terminal-status handling,
   evidence classification and idempotent recovery. Preserve older personal
   memory without automatic reinterpretation.
4. **Expose recorded history.** Add scoped search and reads, incremental indexing
   and bounded context assembly. Verify that older tasks can be found beyond
   the current four-run carry-forward limit.
5. **Extract and consolidate operational lessons.** Add operational prompts,
   proof validation, pattern lifecycle and scheduling watermarks. Adapt conflict
   handling to scoped evidence.
6. **Integrate memory review with retrospectives.** Add the remedy order, shared
   evidence and explicit no-proposal outcomes. Update retrospective documentation
   to match the implemented behaviour.
7. **Add evidence-driven capability proposals.** Extend read-only discovery and
   the existing skill or Apps approval flows. Enforce the prompt-change gate.
8. **Backfill and evaluate.** First process a bounded recent sample from Hemnet,
   Lead Researcher and the newsletter bot. Review errors and source fidelity,
   then backfill remaining recorded history incrementally with durable cursors.

Back up SQLite data before schema migration. Backfill uses new stable operational
document IDs, respects tombstones and never re-runs old tasks. Keep source
records and existing memories intact. Feature rollout must not silently reduce
the user's historical access or modify bot instructions.

## Required checks

Use fixture evidence and fake model responses for routine tests. Live provider
checks use an isolated profile and bounded model calls.

| Check | Acceptance condition |
|---|---|
| Successful and failed tasks | Both retain an episode with correct status, verified work and open issues |
| Cancellation and partial work | Confirmed actions survive; unfinished actions are not marked done or replayed |
| Corrections and brief replies | "No, wrong source" resolves against the task rather than being discarded for length |
| Claim versus evidence | "Published" without confirmation remains an assistant claim |
| Good and bad patterns | Repeated independent outcomes build support; retries and copied evidence do not |
| Contradictions | A counterexample narrows or downgrades the pattern and keeps its provenance |
| Queue recovery | Crash, restart and retry neither lose an episode nor create duplicate facts |
| Consolidation scheduling | Host attachment, idle gates, fair batches, cooldown, backoff and restart watermarks work |
| Full recorded history | Search finds an old task beyond four runs; paged reads return exact evidence |
| Scope | Another bot's source ID or cursor cannot bypass authorization |
| Model limits | Actual model overrides and defaults get the correct frozen policy; unknown limits are visible |
| Compaction | Summary stays within budget, retains protected context and exposes archived source references |
| Restart after compaction | The same working state resumes without replaying completed tool actions |
| Forgetting | Source removal invalidates lessons and summaries; backfill and recall cannot resurrect it |
| Retrospective memory remedy | Missing memory is repaired without proposing a prompt rewrite |
| Retrieval and product defects | Missing recall or unavailable storage is identified as a host issue |
| Capability discovery | A proposed skill or MCP has verified capability and evidence; no installation happens during review |
| Prompt proposal gate | No instruction proposal when memory or an existing capability closes the gap |
| Applied and dismissed proposals | Review uses outcome history and does not repeat unsupported dismissed suggestions |

Track episode capture coverage, extraction failures, useful lessons with valid
proofs, retrieval usefulness, duplicate task actions, recurring failure rates,
compaction before and after estimates, consolidation age and model cost. Higher
fact counts alone are not a success measure. Rex succeeds when a bot resumes
correctly, uses a supported successful procedure, avoids a documented failure,
and proposes a capability or instruction change only for an evidenced gap.

Initial scope excludes automatic instruction edits, autonomous MCP connections,
automatic skill installation, cross-user memory sharing and a separate learning
service. The existing user-reviewed proposal flows remain the way changes are
applied.
