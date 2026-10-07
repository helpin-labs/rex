package rex

import (
	"context"
	"net/http"
	"strconv"
)

// The conversation timeline reads a run's stored events in sequence order;
// each turn records the run's last sequence when it started.

// RunEventsAfter returns a run's stored events after a sequence number, in
// order. Streaming deltas are not stored; the live stream carries those.
func (c *RuntimeClient) RunEventsAfter(ctx context.Context, runID string, after int64) ([]RunEventEnvelope, error) {
	events, err := c.ListRunEvents(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make([]RunEventEnvelope, 0, len(events))
	for _, event := range events {
		if event.SequenceNo > after {
			out = append(out, event)
		}
	}
	return out, nil
}

// LatestSequence is the newest stored event sequence number of a run.
func (c *RuntimeClient) LatestSequence(ctx context.Context, runID string) (int64, error) {
	events, err := c.ListRunEvents(ctx, runID)
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, event := range events {
		if event.SequenceNo > latest {
			latest = event.SequenceNo
		}
	}
	return latest, nil
}

// runTimeline answers GET /api/runs/{run_id}/timeline?after_sequence=N.
func (h *Host) runTimeline(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after_sequence"), 10, 64)
	events, err := h.Runtime.RunEventsAfter(r.Context(), r.PathValue("run_id"), after)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	next := after
	for _, event := range events {
		if event.SequenceNo > next {
			next = event.SequenceNo
		}
	}
	if events == nil {
		events = []RunEventEnvelope{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "next_sequence_no": next})
}
