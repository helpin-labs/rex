// Streaming text arrives in bursts: some providers send a few characters
// at a time, others a sentence every few hundred milliseconds. The chat
// reveals it at a steady pace instead, fast enough to stay close behind
// what has arrived, and renders only the paragraph that is still growing.

// How long a backlog takes to drain while the model writes, and once it
// has finished: about one provider burst, so text flows without lagging.
export const CATCH_UP_MS = 350;
export const FINISH_MS = 100;
// The slowest pace, in characters per millisecond: 90 a second while the
// model writes, and fast once it has finished, so the end never crawls.
export const MIN_RATE = 0.09;
export const MIN_FINISH_RATE = 1.2;

/** How many characters to show after `elapsed` ms, given how many are shown and how many have arrived. */
export function nextShown(shown: number, total: number, elapsed: number, finished: boolean): number {
  if (shown >= total) return total;
  const backlog = total - shown;
  const rate = finished ? Math.max(MIN_FINISH_RATE, backlog / FINISH_MS) : Math.max(MIN_RATE, backlog / CATCH_UP_MS);
  return Math.min(total, shown + Math.max(1, rate * elapsed));
}

/** The length of the text two strings share at the start. */
export function sharedPrefix(a: string, b: string): number {
  const max = Math.min(a.length, b.length);
  let index = 0;
  while (index < max && a.charCodeAt(index) === b.charCodeAt(index)) index++;
  return index;
}

const fence = /^\s*(```|~~~)/;

/**
 * Splits Markdown into blocks at blank lines, so finished blocks need not be
 * parsed again. It never splits inside a code fence, or before an indented
 * line (a list item's continuation, or an indented code block).
 */
export function splitBlocks(text: string): string[] {
  const lines = text.split("\n");
  const blocks: string[] = [];
  let current: string[] = [];
  let inFence = false;
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index];
    if (fence.test(line)) inFence = !inFence;
    if (!inFence && line.trim() === "" && current.length) {
      let next = index + 1;
      while (next < lines.length && lines[next].trim() === "") next++;
      if (next < lines.length && /^[ \t]/.test(lines[next])) { current.push(line); continue; }
      blocks.push(current.join("\n"));
      current = [];
      continue;
    }
    if (line.trim() === "" && !current.length) continue;
    current.push(line);
  }
  if (current.length) blocks.push(current.join("\n"));
  return blocks;
}

/** Closes a code fence the model has opened but not yet closed, so it renders as code while it streams. */
export function closeOpenFence(text: string): string {
  let open = false;
  for (const line of text.split("\n")) if (fence.test(line)) open = !open;
  return open ? `${text}\n\`\`\`` : text;
}
