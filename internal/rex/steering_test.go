package rex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/helpin-ai/agent-runtime-go"
)

func TestSteeringReachesOnlyTheConversationsActiveRun(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	bot := Bot{Name: "Main"}
	_ = bot.Normalize(t.TempDir())
	if err := host.Store.SaveBot(ctx, &bot); err != nil {
		t.Fatal(err)
	}
	conversation, err := host.Store.CreateConversation(ctx, bot.ID, "Chat", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Store.AttachRun(ctx, conversation.ID, "run-current"); err != nil {
		t.Fatal(err)
	}
	var received []sdk.SteerRunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run-current/steering":
			var req sdk.SteerRunRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.ExpectedRevision != int64(len(received)) {
				http.Error(w, `{"error":"steering revision conflict"}`, http.StatusConflict)
				return
			}
			received = append(received, req)
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(sdk.RunSteeringCommand{SteerRunRequest: req, RunID: "run-current", Revision: int64(len(received)), State: "accepted"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run-current/steering":
			json.NewEncoder(w).Encode([]sdk.RunSteeringCommand{{RunID: "run-current", Revision: 1, State: "applied"}})
		default:
			t.Errorf("unexpected runtime call %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host.Runtime = newTestRuntimeClient(t, server.URL, "")

	command, err := host.SteerConversationRun(ctx, conversation.ID, "run-current", SteerRequest{CommandID: "c1", Content: "  Use the April numbers instead.  "})
	if err != nil {
		t.Fatal(err)
	}
	if command.State != "accepted" || len(received) != 1 || received[0].Content != "Use the April numbers instead." || received[0].ExternalActorID != LocalActorID {
		t.Fatalf("command %+v, runtime received %+v", command, received)
	}

	// An instruction written for a run the conversation has moved on from
	// never reaches the runtime.
	if _, err := host.SteerConversationRun(ctx, conversation.ID, "run-old", SteerRequest{CommandID: "c2", Content: "stop"}); err != ErrRunChanged {
		t.Fatalf("old run: %v", err)
	}

	// A stale revision is the runtime's conflict, passed through as 409.
	_, err = host.SteerConversationRun(ctx, conversation.ID, "run-current", SteerRequest{CommandID: "c3", Content: "again"})
	if steeringStatus(err) != http.StatusConflict {
		t.Fatalf("stale revision: %v (status %d)", err, steeringStatus(err))
	}

	commands, err := host.ConversationRunSteering(ctx, conversation.ID, "run-current")
	if err != nil || len(commands) != 1 || commands[0].State != "applied" {
		t.Fatalf("receipts %+v, %v", commands, err)
	}
	if _, err := host.SteerConversationRun(ctx, conversation.ID, "run-current", SteerRequest{CommandID: "c4", Content: strings.Repeat(" ", 3)}); err == nil {
		t.Fatal("empty instruction accepted")
	}
}
