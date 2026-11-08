package rex

import (
	"context"
	"encoding/json"
	sdk "github.com/helpin-ai/agent-runtime-go"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJobManagementRejectsForeignBotAndCanInspectFinishedWorker(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	if err := host.Store.CreateJob(ctx, JobRecord{ID: "job", BotID: "owner", ConversationID: "conversation", Title: "Research", State: "completed"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/v1/work-jobs/job":
			json.NewEncoder(w).Encode(sdk.WorkJob{ID: "job", State: "completed"})
		case "/v1/work-jobs/job/attempts":
			json.NewEncoder(w).Encode([]sdk.WorkAttempt{{RunID: "finished-worker"}})
		case "/v1/runs/finished-worker/steering":
			json.NewEncoder(w).Encode([]sdk.RunSteeringCommand{{State: "applied", Revision: 1}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host.Runtime = newTestRuntimeClient(t, server.URL, "")
	if _, err := host.manageJob(ctx, "foreign", manageJobInput{JobID: "job", Action: "inspect", RunID: "finished-worker"}); err == nil || calls != 0 {
		t.Fatal("foreign bot reached job")
	}
	result, err := host.manageJob(ctx, "owner", manageJobInput{JobID: "job", Action: "inspect", RunID: "finished-worker"})
	if err != nil {
		t.Fatal(err)
	}
	commands := result.([]sdk.RunSteeringCommand)
	if len(commands) != 1 || commands[0].State != "applied" {
		t.Fatalf("receipt: %+v", commands)
	}
}

func TestBotSteersAWorkerWithAPlainMessage(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	if err := host.Store.CreateJob(ctx, JobRecord{ID: "job", BotID: "owner", ConversationID: "conversation", Title: "Research", State: "running"}); err != nil {
		t.Fatal(err)
	}
	var steered []sdk.SteerRunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/work-jobs/job":
			json.NewEncoder(w).Encode(sdk.WorkJob{ID: "job", State: "running", ActiveRuns: []string{"worker"}})
		case r.URL.Path == "/v1/runs/worker/steering" && r.Method == http.MethodGet:
			commands := []sdk.RunSteeringCommand{{Revision: 1, State: "applied", SteerRunRequest: sdk.SteerRunRequest{CommandID: "job:job:spec:2"}}}
			for i, req := range steered {
				commands = append(commands, sdk.RunSteeringCommand{SteerRunRequest: req, Revision: int64(i + 2), State: "accepted"})
			}
			json.NewEncoder(w).Encode(commands)
		case r.URL.Path == "/v1/runs/worker/steering":
			var req sdk.SteerRunRequest
			json.NewDecoder(r.Body).Decode(&req)
			// The runtime's own rules: an ID and the latest revision.
			if req.CommandID == "" || req.ExpectedRevision != int64(len(steered)+1) {
				http.Error(w, `{"error":"invalid steering request"}`, http.StatusBadRequest)
				return
			}
			steered = append(steered, req)
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(sdk.RunSteeringCommand{SteerRunRequest: req, Revision: int64(len(steered) + 1), State: "accepted"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host.Runtime = newTestRuntimeClient(t, server.URL, "")

	input := manageJobInput{JobID: "job", Action: "steer", RunID: "worker", Message: "Use the signed-in browser for LinkedIn.", requestKey: "run-1\x00call-7"}
	result, err := host.manageJob(ctx, "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(steered) != 1 || steered[0].Content != "Use the signed-in browser for LinkedIn." || steered[0].ExternalActorID != "bot:owner" || steered[0].ExpectedRevision != 1 {
		t.Fatalf("steered %+v", steered)
	}
	if receipts := result.([]sdk.RunSteeringCommand); len(receipts) != 2 || receipts[1].State != "accepted" {
		t.Fatalf("receipts %+v", receipts)
	}
	// The runtime retrying the same tool call delivers it once.
	if _, err := host.manageJob(ctx, "owner", input); err != nil || len(steered) != 1 {
		t.Fatalf("retry: %v, steered %d", err, len(steered))
	}
	// The older command form still works, by its content.
	older := manageJobInput{JobID: "job", Action: "steer", RunID: "worker", Command: &sdk.SteerRunRequest{Content: "Stop expanding ICP prose."}, requestKey: "run-1\x00call-8"}
	if _, err := host.manageJob(ctx, "owner", older); err != nil || len(steered) != 2 || steered[1].ExpectedRevision != 2 {
		t.Fatalf("older form: %v, steered %+v", err, steered)
	}
	if _, err := host.manageJob(ctx, "owner", manageJobInput{JobID: "job", Action: "steer", RunID: "worker"}); err == nil {
		t.Fatal("an empty instruction was accepted")
	}
}
