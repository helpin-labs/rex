package rex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/helpin-ai/agent-runtime-go"
)

// fakeWorkers is a runtime with one job and three open workers: one
// working, one paused with a question, one waiting for a sign-in.
type fakeWorkers struct {
	mu      sync.Mutex
	steered []sdk.SteerRunRequest
	resumed map[string]sdk.ResumeRunRequest
	running map[string]bool // runs resumed since the start
}

func (f *fakeWorkers) handler(t *testing.T) http.HandlerFunc {
	runs := map[string]sdk.AgentRun{
		"w-working":  {ID: "w-working", Status: "running"},
		"w-question": {ID: "w-question", Status: "paused", PauseReason: sdk.PauseReasonHumanInput},
		"w-signin":   {ID: "w-signin", Status: "paused", PauseReason: sdk.PauseReasonAuth},
	}
	interactions := map[string][]sdk.AgentRunInteraction{
		"w-question": {{ID: "q1", InteractionKind: "request_user_input", Status: "pending", Title: "Which year?"}},
		"w-signin":   {{ID: "a1", InteractionKind: "authentication", Status: "pending"}},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case r.URL.Path == "/v1/work-jobs/job":
			json.NewEncoder(w).Encode(sdk.WorkJob{ID: "job", State: "running", ActiveRuns: []string{"w-working", "w-question", "w-signin"}})
		case r.URL.Path == "/v1/work-jobs/job/attempts":
			ended := time.Now()
			json.NewEncoder(w).Encode([]sdk.WorkAttempt{{RunID: "w-working"}, {RunID: "w-question"}, {RunID: "w-signin"}, {RunID: "w-finished", EndedAt: &ended}})
		case len(parts) == 3 && parts[1] == "runs":
			run := runs[parts[2]]
			if f.running[parts[2]] {
				run.Status, run.PauseReason = "running", ""
			}
			json.NewEncoder(w).Encode(run)
		case len(parts) == 4 && parts[3] == "interactions":
			json.NewEncoder(w).Encode(interactions[parts[2]])
		case len(parts) == 4 && parts[3] == "messages":
			json.NewEncoder(w).Encode([]sdk.AgentRunMessage{{Role: "assistant", Content: "The site lists two founding years. Which one should I use?"}})
		case len(parts) == 4 && parts[3] == "tool-calls":
			json.NewEncoder(w).Encode([]sdk.ToolCall{})
		case len(parts) == 4 && parts[3] == "steering" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode([]sdk.RunSteeringCommand{{Revision: 2, SteerRunRequest: sdk.SteerRunRequest{CommandID: "old"}}})
		case len(parts) == 4 && parts[3] == "steering":
			var req sdk.SteerRunRequest
			json.NewDecoder(r.Body).Decode(&req)
			f.steered = append(f.steered, req)
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(sdk.RunSteeringCommand{SteerRunRequest: req, Revision: 3, State: "accepted"})
		case len(parts) == 4 && parts[3] == "resume":
			var req sdk.ResumeRunRequest
			json.NewDecoder(r.Body).Decode(&req)
			f.resumed[parts[2]] = req
			json.NewEncoder(w).Encode(runs[parts[2]])
		default:
			json.NewEncoder(w).Encode([]any{})
		}
	}
}

func TestMessageJobWorkerSteersOrAnswersEachWorker(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	fake := &fakeWorkers{resumed: map[string]sdk.ResumeRunRequest{}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	host.Runtime = newTestRuntimeClient(t, server.URL, "")

	// A working worker is steered at its latest revision.
	if err := host.MessageJobWorker(ctx, "job", "w-working", WorkerMessage{CommandID: "c1", Content: " Skip archived repos. "}); err != nil {
		t.Fatal(err)
	}
	if len(fake.steered) != 1 || fake.steered[0].ExpectedRevision != 2 || fake.steered[0].Content != "Skip archived repos." || fake.steered[0].ExternalActorID != LocalActorID {
		t.Fatalf("steered %+v", fake.steered)
	}

	// A worker paused with a question gets the message as the answer.
	if err := host.MessageJobWorker(ctx, "job", "w-question", WorkerMessage{CommandID: "c2", Content: "Use the earlier year."}); err != nil {
		t.Fatal(err)
	}
	if got := fake.resumed["w-question"]; got.InteractionID != "q1" || got.Intent != "reply" || got.Content != "Use the earlier year." || got.ResumeID != "job-reply-c2" || got.MessageProvenance != "human" {
		t.Fatalf("resumed %+v", got)
	}

	// A sign-in cannot be answered with a message.
	if err := host.MessageJobWorker(ctx, "job", "w-signin", WorkerMessage{CommandID: "c3", Content: "go on"}); err == nil || !strings.Contains(err.Error(), "sign-in") {
		t.Fatalf("sign-in worker: %v", err)
	}
	// Finished workers and runs of other jobs are refused.
	for _, run := range []string{"w-finished", "someone-else"} {
		if err := host.MessageJobWorker(ctx, "job", run, WorkerMessage{CommandID: "c4", Content: "hi"}); err != ErrNotJobWorker {
			t.Fatalf("%s: %v", run, err)
		}
	}
	if len(fake.steered) != 1 || len(fake.resumed) != 1 {
		t.Fatalf("refused messages reached the runtime: steered %d resumed %d", len(fake.steered), len(fake.resumed))
	}
}

func TestJobWorkersShowWhatPausedWorkersNeed(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	fake := &fakeWorkers{resumed: map[string]sdk.ResumeRunRequest{}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	host.Runtime = newTestRuntimeClient(t, server.URL, "")

	view := host.jobView(ctx, JobRecord{ID: "job", BotID: "bot"}, true)
	byRun := map[string]JobWorker{}
	for _, worker := range view.Workers {
		byRun[worker.RunID] = worker
	}
	if len(byRun) != 3 {
		t.Fatalf("open workers: %+v", view.Workers)
	}
	question := byRun["w-question"]
	if len(question.Pending) != 1 || question.Pending[0].ID != "q1" || !strings.Contains(question.LastMessage, "Which one should I use?") {
		t.Fatalf("paused worker: %+v", question)
	}
	if working := byRun["w-working"]; working.LastMessage != "" || len(working.Pending) != 0 {
		t.Fatalf("working worker should carry no attention data: %+v", working)
	}
}

func TestRetriedWorkerReplyIsNotDeliveredAgainAsSteering(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	fake := &fakeWorkers{resumed: map[string]sdk.ResumeRunRequest{}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	host.Runtime = newTestRuntimeClient(t, server.URL, "")

	message := WorkerMessage{CommandID: "reply-1", Content: "Use the earlier year."}
	if err := host.MessageJobWorker(ctx, "job", "w-question", message); err != nil {
		t.Fatal(err)
	}
	// The reply resumed the worker; a retried send now finds it working.
	fake.mu.Lock()
	fake.running = map[string]bool{"w-question": true}
	fake.mu.Unlock()
	if err := host.MessageJobWorker(ctx, "job", "w-question", message); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumed) != 1 || len(fake.steered) != 0 {
		t.Fatalf("retry delivered again: resumed %d, steered %+v", len(fake.resumed), fake.steered)
	}
}
