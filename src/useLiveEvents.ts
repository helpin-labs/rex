import { useEffect, useState } from "react";
import { connect } from "./api";
import { runEventTypes } from "./runStream";
import type { RunEventEnvelope } from "./types";

// useLiveEvents follows a working run's live stream. Streaming text deltas
// are only sent live (they are not stored), so the active turn reads them
// from here; stored events come from the timeline. Events arrive many times
// a second while the model writes, so they are applied in small batches.

const FLUSH_MS = 120;
export function useLiveEvents(runId: string | undefined): RunEventEnvelope[] {
  const [events, setEvents] = useState<RunEventEnvelope[]>([]);
  useEffect(() => {
    setEvents([]);
    if (!runId) return;
    let source: EventSource | null = null;
    let cancelled = false;
    let pending: RunEventEnvelope[] = [];
    let timer = 0;
    const flush = () => {
      timer = 0;
      const batch = pending;
      pending = [];
      if (batch.length && !cancelled) setEvents(current => [...current, ...batch]);
    };
    void connect().then(({ url, token }) => {
      if (cancelled) return;
      source = new EventSource(`${url}/api/runs/${encodeURIComponent(runId)}/events?token=${encodeURIComponent(token)}`);
      for (const type of runEventTypes) {
        source.addEventListener(type, raw => {
          try {
            const event = JSON.parse((raw as MessageEvent<string>).data) as RunEventEnvelope;
            pending.push(event);
            if (!timer) timer = window.setTimeout(flush, FLUSH_MS);
          } catch { /* ignore malformed events */ }
        });
      }
    });
    return () => { cancelled = true; window.clearTimeout(timer); source?.close(); };
  }, [runId]);
  return events;
}
