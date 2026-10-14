import type { Bot, Turn } from "./types";

// Suggestions for an empty conversation, from what the bot can do. They fill
// the composer rather than send, so the user finishes the sentence.
export function starterPrompts(bot: Pick<Bot, "role" | "capabilities" | "memory_mode">): string[] {
  const prompts: string[] = [];
  if (bot.role === "main") prompts.push("Create a bot that ", "Which of my bots should handle ");
  else prompts.push("What can you help me with?");
  if (bot.capabilities.web) prompts.push("Find the latest news about ");
  if (bot.capabilities.browser) prompts.push("Open this page and summarise it: ");
  if (bot.capabilities.files_read) prompts.push("Summarise the file I attach");
  if (bot.memory_mode !== "off") prompts.push("Remember this workflow: ");
  return prompts.slice(0, 4);
}

// Why a message failed, in words a person can act on. The raw error stays
// available on hover.
export function turnFailure(error?: string): string {
  if (!error) return "The bot could not finish this message.";
  if (/context length|context window|maximum context|too many tokens|prompt is too long/i.test(error)) return "The conversation is too long for this model. Start fresh, then try again.";
  if (/\b(401|403)\b|unauthori[sz]ed|invalid api key|incorrect api key|authentication/i.test(error)) return "The model provider rejected the key. Check it in Settings → Models.";
  if (/\b402\b|insufficient (credits|funds|quota)|billing|payment required/i.test(error)) return "The model provider account is out of credits.";
  if (/\b429\b|rate limit|too many requests|overloaded/i.test(error)) return "The model provider is busy or rate limiting. Try again in a moment.";
  if (/model.*(not found|does not exist|unavailable)|no such model|\b404\b/i.test(error)) return "This model isn't available. Pick another in the bot's settings.";
  if (/failed to read stream|stream ended before completion|connection reset by peer/i.test(error)) return "The model connection dropped while streaming. Earlier completed steps are saved.";
  if (/no such host|network is unreachable|connection refused|i\/o timeout|dial tcp|timed out/i.test(error)) return "Couldn't reach the model provider. Check the connection, then try again.";
  return error.length > 200 ? `${error.slice(0, 200)}…` : error;
}

/** The turns whose message or answer contains the query, oldest first. */
export function searchTurns(turns: Pick<Turn, "user_text" | "assistant_text">[], query: string): number[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return [];
  const found: number[] = [];
  turns.forEach((turn, index) => { if (`${turn.user_text}\n${turn.assistant_text}`.toLowerCase().includes(needle)) found.push(index); });
  return found;
}
