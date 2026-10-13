# Bot retrospectives — plan

Status: steps 1 and 3 implemented 2026-10-07 (`internal/rex/retro.go`,
`internal/rex/catalog.go`). Steps 2 and 4 are planned.

The implemented local revision is specified in [operational memory and retrospective
learning](PLAN-bot-operational-memory.md). It makes memory review and
consolidation the first remedies, adds capability research for skills and MCPs,
and permits instruction proposals only when memory and available capabilities
cannot close the demonstrated gap. That revision is implemented locally; its generic runtime changes must be released before Rex is pinned and shipped. Its memory-first remedy order supersedes the original instruction-first procedure below.

## Goal

Every bot improves itself over time. After some tasks it looks back at how
they went and suggests one improvement: better instructions, a new skill
written from what worked, or a skill from a trusted catalog. Nothing changes
until the user applies it.

This lives in Rex, not in each bot's instructions, so every bot gets it,
and Rex controls when it runs, what it costs and that the user decides.

## 1. When a bot looks back

After a task settles, Rex checks cheap signals (no model call):

- **Correction:** the user said "no", "wrong", "not what I asked", "again",
  "I told you", or repeated an earlier instruction.
- **Lasting preference:** "next time", "from now on", "always", "never",
  "don't …".
- **Blocked or failed:** the answer says it was blocked, skipped something,
  hit a CAPTCHA or sign-in, or could not finish; tool calls failed or were
  refused.
- **Routine:** a scheduled run that failed.

Any signal queues a retrospective. Without signals, a bot still gets a
periodic one after 10 settled tasks, or a week with at least 3 tasks.

Guards:

- It runs only when the bot is idle: no task running, and the last one ended
  at least 2 minutes ago.
- A cooldown of 3 hours per bot, except when the user asks for one.
- At most one open suggestion per bot; while one waits, no new look-back.
- Never during setup, and never for bots with "Suggest improvements" off.

## 2. What a retrospective is

A short, separate run on the bot's own model and provider, with no task
tools. Its only tool is `suggest_improvement` on Rex's `retro` MCP server.
It sees:

- the bot's name, current instructions, tools and skills;
- the tasks since the last look-back: what the user asked, what the bot
  answered, and which tool calls failed;
- why it was triggered;
- its last suggestions and whether the user applied or dismissed them.

It calls `suggest_improvement` once with one of:

| Kind | Step | What the user sees |
|---|---|---|
| `nothing` | 1 | Nothing. The most common answer. |
| `update_instructions` | 1 | The proposal card, with the reason and a diff. |
| `create_skill` | 2 | A new skill (SKILL.md and files) to add to this bot. |
| `install_skill` | 3 | A skill from an allowed catalog, pinned, file by file. |

Each suggestion has a one-sentence summary and a reason that points to the
tasks behind it. Facts about the user still go to memory automatically and
are not suggestions.

The run is a `routine` target in agent-runtime under its own agent
(`<bot id>--retro`) and is never shown as a conversation. Rex records it in
`retrospectives` (trigger, status, run, outcome).

## 3. What the user sees

The existing proposal card in the bot's conversation, marked as a look-back:
"Spain trip looked back at recent work", the reason, the change, Apply or
Discard. It is in the attention list, Dock badge and notifications like any
other proposal. Answers are kept in `proposal_history`, and the next
retrospective sees them, so a dismissed idea is not suggested again.

Edit bot has a "Suggest improvements" switch (on by default) and a "Look back
now" button.

## 4. Skills (steps 2 and 3)

- **Create:** when the same multi-step procedure worked two or three times,
  the retrospective writes it as a skill folder. Shown file by file; added to
  `~/Rex/skills` and turned on for the bot only when the user applies it.
- **Install:** only from allowed sources: a short built-in list (for example
  Anthropic's public skills repository) plus GitHub repositories the user adds
  in Settings. Pinned to a commit. Skills with scripts are flagged, and their
  scripts still go through the bot's command approvals. A skill never installs
  itself and never changes instructions.

## 5. Learning from outcomes (step 4)

Record which applied changes were followed by fewer corrections and failures,
and show that in the next retrospective, so a bot learns which kinds of
suggestions help.

## Build order

1. Signals, the retrospective run, instruction suggestions, history, the
   switch and "Look back now". **Done.**
2. Creating skills from repeated procedures.
3. Skill catalog and installing skills. **Done:** sources in Skills → Browse
   (Anthropic and Corey Haines' marketing set on by default; more marketing,
   sales and coding sets one switch away), `find_skills_in_catalog` and
   `suggest_skill` for setup and Rex, and `install_skill` in look-backs.
4. Outcome tracking.
