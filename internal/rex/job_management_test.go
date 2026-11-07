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
