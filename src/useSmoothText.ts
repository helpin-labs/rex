import { useEffect, useRef, useState } from "react";
import { nextShown, sharedPrefix } from "./streaming";

// useSmoothText reveals streaming text at a steady pace (see streaming.ts).
// Text that was never live, such as earlier answers, shows at once. Once a
// text has streamed it keeps easing in until it has caught up, even after
// the model finished.
export function useSmoothText(target: string, live: boolean): { text: string; animating: boolean } {
  const streamed = useRef(live);
  if (live) streamed.current = true;
  const shown = useRef(live ? 0 : target.length);
  const previous = useRef(target);
  const targetRef = useRef(target);
  const finished = useRef(!live);
  const frame = useRef(0);
  const last = useRef(0);
  const [, setTick] = useState(0);

  // A new answer that doesn't continue the old one restarts from what they share.
  if (target !== previous.current) {
    if (!target.startsWith(previous.current.slice(0, Math.floor(shown.current)))) shown.current = sharedPrefix(previous.current, target);
    previous.current = target;
  }
  targetRef.current = target;
  finished.current = !live;

  useEffect(() => {
    if (!streamed.current || frame.current || shown.current >= targetRef.current.length) return;
    const tick = (now: number) => {
      const elapsed = last.current ? Math.min(64, now - last.current) : 16;
      last.current = now;
      shown.current = nextShown(shown.current, targetRef.current.length, elapsed, finished.current);
      setTick(value => value + 1);
      if (shown.current < targetRef.current.length) frame.current = requestAnimationFrame(tick);
      else { frame.current = 0; last.current = 0; }
    };
    frame.current = requestAnimationFrame(tick);
  }, [target, live]);
  useEffect(() => () => cancelAnimationFrame(frame.current), []);
  // Nothing animates in a hidden window: show everything that has arrived,
  // and again when the window comes back.
  useEffect(() => {
    const catchUp = () => { if (document.hidden && shown.current < targetRef.current.length) { shown.current = targetRef.current.length; setTick(value => value + 1); } };
    catchUp();
    document.addEventListener("visibilitychange", catchUp);
    return () => document.removeEventListener("visibilitychange", catchUp);
  }, [target]);

  if (!streamed.current) return { text: target, animating: false };
  const count = Math.floor(shown.current);
  return { text: target.slice(0, count), animating: live || count < target.length };
}

// useStableText keeps the last text shown while a new source catches up:
// when a run ends, the live stream closes a moment before the stored
// answer arrives, and the answer should not blink out.
export function useStableText(text: string, keep: boolean): string {
  const last = useRef(text);
  if (text) last.current = text;
  return text || (keep ? last.current : "");
}
